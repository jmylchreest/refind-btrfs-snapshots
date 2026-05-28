package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/bls"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/bootloader"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/cliconfig"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/config"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/diff"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/discovery"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/fstab"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/runner"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/snapshotfs"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var generateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Write BLS Type #1 entries for btrfs snapshots",
	RunE:  runGenerate,
}

var blsFlagToKey = map[string]string{
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
	return cliconfig.Load(cmd, "/etc/bls-btrfs-snapshots.yaml", blsFlagToKey)
}

func runGenerate(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	if !cfg.BLS.WriteEntries.IsTrue() {
		log.Warn().Msg("bls.write_entries is false in config — nothing to do. Enable it in /etc/bls-btrfs-snapshots.yaml.")
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

	force, _ := cmd.Flags().GetBool("force")
	if !force && cfg.Behavior.ExitOnSnapshotBoot.IsTrue() && btrfsMgr.IsSnapshotBootFromRootFS(rootFS) {
		log.Warn().Str("subvolume", rootFS.Subvolume.Path).Msg("Currently booted from a snapshot. Use --force to override or set behavior.exit_on_snapshot_boot=false in config.")
		return fmt.Errorf("refusing to generate BLS entries while booted from snapshot")
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
	planner := kernel.NewPlanner(fstab.NewManager(), checker, bootSets, rootFS)
	plans := planner.Plan(snapshots)

	entriesDir := filepath.Join(espPath, strings.TrimPrefix(cfg.BLS.EntriesDir, "/"))
	sourceEntries := extractSourceEntries(entriesDir, cfg.BLS.EntryPrefix, bootSets)
	if len(sourceEntries) == 0 {
		log.Warn().Msg("No source boot entries available. The bls binary derives cmdlines from existing BLS entries on the ESP, then /etc/kernel/cmdline, then /proc/cmdline. None produced usable templates.")
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
	for _, u := range snapshotfs.UpdateFstabs(snapshots, rootFS, fstab.NewManager()) {
		patch.AddFile(u.Diff)
	}
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

// extractSourceEntries returns the templates the BLS generator clones per
// snapshot. Primary source is existing non-managed BLS entries on the ESP
// (what systemd-boot already reads); when that's empty, we synthesise one
// per non-UKI BootSet using a cmdline read from /etc/kernel/cmdline or
// /proc/cmdline.
func extractSourceEntries(entriesDir, managedPrefix string, bootSets []*kernel.BootSet) []bootloader.SourceEntry {
	scanned := bls.ScanEntriesDir(entriesDir)
	var sources []bootloader.SourceEntry
	for _, e := range scanned {
		if managedPrefix != "" && strings.HasPrefix(e.ID, managedPrefix) {
			continue
		}
		if e.Linux == "" {
			continue // not a Linux boot entry (could be Windows, EFI shell, etc.)
		}
		sources = append(sources, bootloader.SourceEntry{
			Title:   e.Title,
			Loader:  e.Linux,
			Initrd:  append([]string(nil), e.Initrd...),
			Options: e.OptionsString(),
		})
	}
	if len(sources) > 0 {
		log.Debug().Int("count", len(sources)).Msg("Using existing BLS entries as source templates")
		return sources
	}

	cmdline, src, err := readFallbackCmdline()
	if err != nil {
		log.Warn().Err(err).Msg("No existing BLS entries and no cmdline fallback available")
		return nil
	}
	log.Info().Str("source", src).Msg("No existing BLS entries on ESP — synthesising sources from BootSets")

	for _, bs := range bootSets {
		if bs.Layout == kernel.LayoutUKI || bs.Kernel == nil {
			continue
		}
		se := bootloader.SourceEntry{
			Title:   bs.DisplayName(),
			Loader:  bs.Kernel.Path,
			Options: cmdline,
		}
		if bs.Initramfs != nil {
			se.Initrd = append(se.Initrd, bs.Initramfs.Path)
		}
		for _, mc := range bs.Microcode {
			se.Initrd = append(se.Initrd, mc.Path)
		}
		sources = append(sources, se)
	}
	return sources
}

// readFallbackCmdline returns the kernel command line from the first
// readable source. /etc/kernel/cmdline is the kernel-install / mkinitcpio
// canonical location; /proc/cmdline is the running kernel as a last resort,
// stripped of bootloader-injected initrd= and BOOT_IMAGE= tokens.
func readFallbackCmdline() (string, string, error) {
	if b, err := os.ReadFile("/etc/kernel/cmdline"); err == nil {
		return strings.TrimSpace(string(b)), "/etc/kernel/cmdline", nil
	}
	if b, err := os.ReadFile("/proc/cmdline"); err == nil {
		return stripProcCmdline(strings.TrimSpace(string(b))), "/proc/cmdline", nil
	}
	return "", "", fmt.Errorf("no readable cmdline at /etc/kernel/cmdline or /proc/cmdline")
}

func stripProcCmdline(s string) string {
	keep := make([]string, 0, len(strings.Fields(s)))
	for _, tok := range strings.Fields(s) {
		lower := strings.ToLower(tok)
		if strings.HasPrefix(lower, "initrd=") || strings.HasPrefix(lower, "boot_image=") {
			continue
		}
		keep = append(keep, tok)
	}
	return strings.Join(keep, " ")
}
