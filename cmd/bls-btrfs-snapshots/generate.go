package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/bls"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/bootloader"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/config"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/diff"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/discovery"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/fstab"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/refind"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/runner"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var generateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Write BLS Type #1 entries for btrfs snapshots",
	RunE:  runGenerate,
}

func init() {
	rootCmd.AddCommand(generateCmd)
	generateCmd.Flags().Bool("dry-run", false, "Show what would be written without making changes")
	generateCmd.Flags().BoolP("yes", "y", false, "Automatically approve all changes without prompting")
}

// configCandidates resolves the config path: --config flag wins, otherwise
// prefer the binary-specific file and fall back to refind-btrfs-snapshots.yaml.
func configCandidates(flagPath string) []string {
	if flagPath != "" {
		return []string{flagPath}
	}
	return []string{
		"/etc/bls-btrfs-snapshots.yaml",
		"/etc/refind-btrfs-snapshots.yaml",
	}
}

func loadConfig(cmd *cobra.Command) (*config.Config, error) {
	flagPath, _ := cmd.Flags().GetString("config")
	for _, p := range configCandidates(flagPath) {
		if _, err := os.Stat(p); err == nil {
			cfg, err := config.Load(p, nil)
			if err != nil {
				return nil, err
			}
			log.Debug().Str("config_file", p).Msg("Loaded config")
			applyFlagOverrides(cfg, cmd)
			return cfg, nil
		}
	}
	cfg, err := config.Load("", nil)
	if err != nil {
		return nil, err
	}
	applyFlagOverrides(cfg, cmd)
	return cfg, nil
}

func applyFlagOverrides(cfg *config.Config, cmd *cobra.Command) {
	if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun {
		cfg.DryRun = config.Truthy(true)
	}
	if yes, _ := cmd.Flags().GetBool("yes"); yes {
		cfg.AutoApprove = config.Truthy(true)
	}
}

func runGenerate(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	if !cfg.BLS.WriteEntries.IsTrue() {
		log.Warn().Msg("bls.write_entries is false in config — nothing to do. Enable it in /etc/bls-btrfs-snapshots.yaml (or /etc/refind-btrfs-snapshots.yaml).")
		return nil
	}

	espPath, err := discovery.ResolveESP(discovery.ESPOptions{
		UUID:       cfg.ESP.UUID,
		AutoDetect: cfg.ESP.AutoDetect.IsTrue(),
		MountPoint: cfg.ESP.MountPoint,
	})
	if err != nil {
		return fmt.Errorf("resolve ESP: %w", err)
	}

	bootSets, _ := discovery.DetectBootSets(
		discovery.ESPOptions{UUID: cfg.ESP.UUID, AutoDetect: cfg.ESP.AutoDetect.IsTrue(), MountPoint: cfg.ESP.MountPoint},
		patternsFromConfig(cfg.Kernel.BootImagePatterns),
	)

	btrfsMgr := btrfs.NewManager(cfg.Snapshot.SearchDirectories, cfg.Snapshot.MaxDepth, cfg.Advanced.Naming.RwsnapFormat, cfg.Display.LocalTime.IsTrue())
	rootFS, err := btrfsMgr.GetRootFilesystem()
	if err != nil {
		return fmt.Errorf("locate root btrfs filesystem: %w", err)
	}

	snapshots, err := collectSnapshots(btrfsMgr, cfg.Snapshot.SelectionCount)
	if err != nil {
		return fmt.Errorf("discover snapshots: %w", err)
	}
	if len(snapshots) == 0 {
		log.Info().Msg("No snapshots found — nothing to do")
		return nil
	}

	staleAction := kernel.ParseStaleAction(cfg.Kernel.StaleSnapshotAction)
	checker := kernel.NewChecker(staleAction)
	ukiStrategy := kernel.ParseUKIStrategy(cfg.UKI.SnapshotStrategy)
	planner := kernel.NewPlanner(fstab.NewManager(), checker, bootSets, rootFS, ukiStrategy)
	plans := planner.Plan(snapshots)

	sourceEntries, err := extractSourceEntries(espPath, cfg.Refind.ConfigPath, rootFS)
	if err != nil {
		log.Warn().Err(err).Msg("Could not parse rEFInd source entries — BLS output may be empty")
	}
	if len(sourceEntries) == 0 {
		log.Warn().Msg("No source boot entries found. The bls binary derives cmdlines from the system's primary bootloader config (currently expected: refind.conf).")
	}

	r := runner.New(cfg.DryRun.IsTrue())
	gen := bls.NewGenerator()
	out, err := gen.Generate(bootloader.Input{
		Cfg:                cfg,
		ESPPath:            espPath,
		RootFS:             rootFS,
		ProcessedSnapshots: snapshots,
		BootPlans:          plans,
		SourceEntries:      sourceEntries,
	})
	if err != nil {
		return fmt.Errorf("bls generator: %w", err)
	}

	patch := diff.NewPatchDiff()
	for _, d := range out.Diffs {
		patch.AddFile(d)
	}

	if len(patch.Files) == 0 {
		log.Info().Msg("No changes needed - BLS entries are up to date")
		return nil
	}

	if r.IsDryRun() {
		diff.ShowPatchWithPager(patch, !cfg.AutoApprove.IsTrue())
		log.Info().Msg("[DRY RUN] Would apply all changes shown above")
		return nil
	}

	if !cfg.AutoApprove.IsTrue() {
		if !diff.ConfirmPatchChanges(patch, false) {
			log.Info().Msg("User declined changes - operation cancelled")
			return nil
		}
	} else {
		diff.ShowPatchWithPager(patch, false)
	}

	if err := diff.Apply(patch, r); err != nil {
		return fmt.Errorf("apply BLS entries: %w", err)
	}
	log.Info().Int("entries", len(out.Diffs)).Msg("BLS entries written")
	return nil
}

// patternsFromConfig drops invalid roles and returns the typed list the
// kernel scanner accepts.
func patternsFromConfig(cfgPatterns []config.PatternConfig) []kernel.PatternConfig {
	var out []kernel.PatternConfig
	for _, p := range cfgPatterns {
		role, err := kernel.ParseImageRole(p.Role)
		if err != nil {
			log.Warn().Err(err).Str("glob", p.Glob).Msg("Invalid role in boot_image_patterns, skipping")
			continue
		}
		out = append(out, kernel.PatternConfig{
			Glob:        p.Glob,
			Role:        role,
			StripPrefix: p.StripPrefix,
			StripSuffix: p.StripSuffix,
			KernelName:  p.KernelName,
		})
	}
	return out
}

// collectSnapshots walks all detected btrfs filesystems and returns the
// deduplicated, newest-first set of snapshots, trimmed to selectionCount.
func collectSnapshots(mgr *btrfs.Manager, selectionCount int) ([]*btrfs.Snapshot, error) {
	filesystems, err := mgr.DetectBtrfsFilesystems()
	if err != nil {
		return nil, err
	}
	var all []*btrfs.Snapshot
	seen := make(map[string]bool)
	for _, fs := range filesystems {
		snaps, err := mgr.FindSnapshots(fs)
		if err != nil {
			log.Warn().Err(err).Str("fs", fs.GetBestIdentifier()).Msg("Snapshot discovery failed")
			continue
		}
		for _, s := range snaps {
			if !seen[s.Path] {
				seen[s.Path] = true
				all = append(all, s)
			}
		}
	}
	slices.SortFunc(all, func(a, b *btrfs.Snapshot) int { return b.SnapshotTime.Compare(a.SnapshotTime) })
	if selectionCount > 0 && len(all) > selectionCount {
		all = all[:selectionCount]
	}
	return all, nil
}

// extractSourceEntries reads the system's rEFInd configuration to get the
// canonical kernel + cmdline pairs. This is a v1 simplification — when
// discovery is upgraded to populate BootSet.Cmdline directly, this lookup
// will move into the discovery layer.
func extractSourceEntries(espPath, configPath string, rootFS *btrfs.Filesystem) ([]bootloader.SourceEntry, error) {
	parser := refind.NewParser(espPath)

	if configPath == "" || configPath == "/EFI/refind/refind.conf" {
		if found, err := parser.FindRefindConfigPath(); err == nil {
			configPath = found
		}
	}
	if !filepath.IsAbs(configPath) {
		configPath = filepath.Join(espPath, configPath)
	}

	cfg, err := parser.ParseConfig(configPath)
	if err != nil {
		return nil, err
	}

	var sources []bootloader.SourceEntry
	for _, entry := range cfg.Entries {
		if !refind.IsBootable(entry, rootFS) {
			continue
		}
		sources = append(sources, bootloader.SourceEntry{
			Title:   entry.Title,
			Loader:  entry.Loader,
			Initrd:  entry.Initrd,
			Options: entry.Options,
		})
	}
	return sources, nil
}
