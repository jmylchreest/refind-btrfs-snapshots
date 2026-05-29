package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/bootloader"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/cliconfig"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/config"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/diff"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/discovery"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/runner"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/uki"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var generateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Clone UKIs per btrfs snapshot under uki.output_dir",
	RunE:  runGenerate,
}

var ukiFlagToKey = map[string]string{
	"dry-run": "dry_run",
	"yes":     "yes",
}

func init() {
	rootCmd.AddCommand(generateCmd)
	generateCmd.Flags().Bool("dry-run", false, "Show what would be written without making changes")
	generateCmd.Flags().BoolP("yes", "y", false, "Automatically approve all changes without prompting")
	generateCmd.Flags().Bool("force", false, "Force generation even if booted from a snapshot")
}

func loadConfig(cmd *cobra.Command) (*config.Config, error) {
	return cliconfig.Load(cmd, "/etc/uki-btrfs-snapshots.yaml", ukiFlagToKey)
}

func runGenerate(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	if !cfg.UKI.WriteEntries.IsTrue() {
		log.Warn().Msg("uki.write_entries is false in config — nothing to do. Enable it in /etc/uki-btrfs-snapshots.yaml.")
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

	ukiSets := filterUKIBootSets(bootSets, cfg.UKI.EntryPrefix)
	if len(ukiSets) == 0 {
		log.Info().Str("esp", espPath).Msg("No source UKIs found on ESP — nothing to clone")
		return nil
	}

	btrfsMgr := btrfs.NewManager(cfg.Snapshot.SearchDirectories, cfg.Snapshot.MaxDepth, cfg.Advanced.Naming.RwsnapFormat, cfg.Display.LocalTime.IsTrue())
	rootFS, err := btrfsMgr.GetRootFilesystem()
	if err != nil {
		return fmt.Errorf("locate root btrfs filesystem: %w", err)
	}

	force, _ := cmd.Flags().GetBool("force")
	if !force && cfg.Behavior.ExitOnSnapshotBoot.IsTrue() && btrfsMgr.IsSnapshotBootFromRootFS(rootFS) {
		log.Warn().Str("subvolume", rootFS.Subvolume.Path).Msg("Currently booted from a snapshot. Use --force to override or set behavior.exit_on_snapshot_boot=false in config.")
		return fmt.Errorf("refusing to generate UKI clones while booted from snapshot")
	}

	snapshots, err := collectSnapshots(btrfsMgr, cfg.Snapshot.SelectionCount)
	if err != nil {
		return fmt.Errorf("discover snapshots: %w", err)
	}
	if len(snapshots) == 0 {
		log.Info().Msg("No snapshots found — nothing to do")
		return nil
	}

	r := runner.New(cfg.DryRun.IsTrue())
	gen := uki.NewGenerator()
	out, err := gen.Generate(bootloader.Input{
		Cfg:                cfg,
		ESPPath:            espPath,
		RootFS:             rootFS,
		ProcessedSnapshots: snapshots,
		SourceUKIs:         ukiSets,
	})
	if err != nil {
		return fmt.Errorf("uki generator: %w", err)
	}

	if len(out.BinaryWrites) == 0 && len(out.Diffs) == 0 {
		log.Info().Msg("No changes needed — UKI clones are up to date")
		return nil
	}

	patch := diff.NewPatchDiff()
	for _, d := range out.Diffs {
		patch.AddFile(d)
	}

	if r.IsDryRun() {
		diff.ShowPatchWithPager(patch, !cfg.AutoApprove.IsTrue())
		log.Info().
			Int("clones", len(out.BinaryWrites)).
			Msg("[DRY RUN] Would apply all UKI changes shown above")
		return nil
	}

	if !cfg.AutoApprove.IsTrue() {
		if !diff.ConfirmPatchChanges(patch, false) {
			log.Info().Msg("User declined changes — operation cancelled")
			return nil
		}
	} else {
		diff.ShowPatchWithPager(patch, false)
	}

	if err := uki.Apply(out, r); err != nil {
		return fmt.Errorf("apply UKI clones: %w", err)
	}
	log.Info().Int("clones", len(out.BinaryWrites)).Msg("UKI clones written")
	return nil
}

// filterUKIBootSets keeps UKI-layout sets that aren't already our own
// managed clones — feeding a clone back as a source would double the
// clone count every run.
func filterUKIBootSets(bootSets []*kernel.BootSet, managedPrefix string) []*kernel.BootSet {
	var out []*kernel.BootSet
	for _, bs := range bootSets {
		if bs.Layout != kernel.LayoutUKI || bs.UKI == nil {
			continue
		}
		if managedPrefix != "" && strings.HasPrefix(filepath.Base(bs.UKI.Path), managedPrefix) {
			continue
		}
		out = append(out, bs)
	}
	return out
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
