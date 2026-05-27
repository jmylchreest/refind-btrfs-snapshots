package generator

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/diff"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/refind"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/snapshotfs"
	"github.com/rs/zerolog/log"
)

// BuildPatch turns a discovered Plan into a unified patch plus an operation
// summary: it updates snapshot fstabs, parses the live rEFInd config, writes
// snapshot entries into matching refind_linux.conf files, and optionally
// generates the managed include file. Returns an empty patch (and zero-value
// summary) when there's nothing to write.
func (p *Pipeline) BuildPatch(plan *Plan) (*diff.PatchDiff, *OperationSummary, error) {
	patch := diff.NewPatchDiff()
	summary := &OperationSummary{
		IncludedSnapshots: make([]string, 0),
		AddedSnapshots:    make([]string, 0),
		RemovedSnapshots:  plan.Removed,
		StaleSnapshots:    make([]string, 0),
		UpdatedFstabs:     make([]string, 0),
		UpdatedConfigs:    make([]string, 0),
		WritableChanges:   make([]string, 0),
	}

	for _, bp := range plan.BootPlans {
		if s := bp.FormatStaleSummary(); s != "" && bp.IsStale() {
			summary.StaleSnapshots = append(summary.StaleSnapshots, s)
		}
	}

	for _, u := range snapshotfs.UpdateFstabs(plan.ProcessedSnapshots, plan.RootFS, p.Fstab) {
		patch.AddFile(u.Diff)
		summary.UpdatedFstabs = append(summary.UpdatedFstabs, u.Snapshot.Path+"/etc/fstab")
	}

	refindParser := refind.NewParserWithScanner(p.ESPPath, p.KernelScanner)
	configPath := p.resolveRefindConfigPath(refindParser)

	config, err := refindParser.ParseConfig(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse rEFInd config: %w", err)
	}

	sourceEntries := bootableEntries(config.Entries, plan.RootFS)
	if len(sourceEntries) == 0 {
		return nil, nil, fmt.Errorf("no suitable boot entries found in rEFInd config")
	}
	log.Info().
		Int("total_entries", len(config.Entries)).
		Int("valid_entries", len(sourceEntries)).
		Msg("Checking valid entries")

	generator := refind.NewGeneratorWithBootPlans(p.ESPPath, p.Cfg.Advanced.Naming.MenuFormat, p.Cfg.Display.LocalTime.IsTrue(), p.KernelScanner, p.BootSets, plan.BootPlans)
	refindLinuxEntries, otherEntries := splitSourcesByConfigType(sourceEntries)

	updatedRefindLinuxConf := p.applyRefindLinuxUpdates(generator, refindLinuxEntries, plan, patch, summary)
	p.maybeApplyManagedConfig(generator, refindParser, configPath, otherEntries, sourceEntries, updatedRefindLinuxConf, plan, patch, summary)

	for _, snapshot := range plan.ProcessedSnapshots {
		summary.IncludedSnapshots = append(summary.IncludedSnapshots, p.formatSnapshotName(snapshot))
	}

	return patch, summary, nil
}

// resolveRefindConfigPath picks the rEFInd config file path: auto-detect
// when the user left the default, or honour their override (resolving
// relative paths against the ESP).
func (p *Pipeline) resolveRefindConfigPath(parser *refind.Parser) string {
	path := p.Cfg.Refind.ConfigPath

	if path == "/EFI/refind/refind.conf" {
		if detected, err := parser.FindRefindConfigPath(); err == nil {
			log.Info().Str("path", detected).Msg("Auto-detected rEFInd config")
			return detected
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(p.ESPPath, path)
		}
		log.Debug().Str("path", path).Msg("Using configured rEFInd config path")
		return path
	}

	if !filepath.IsAbs(path) {
		path = filepath.Join(p.ESPPath, path)
	}
	log.Info().Str("path", path).Msg("Using custom rEFInd config path")
	return path
}

func bootableEntries(entries []*refind.MenuEntry, rootFS *btrfs.Filesystem) []*refind.MenuEntry {
	var out []*refind.MenuEntry
	for _, entry := range entries {
		if refind.IsBootable(entry, rootFS) {
			out = append(out, entry)
		}
	}
	return out
}

// splitSourcesByConfigType separates source entries by which kind of config
// file they came from. refind_linux.conf entries are updated in-place;
// menuentry-style entries feed the managed include file.
func splitSourcesByConfigType(entries []*refind.MenuEntry) (refindLinux, other []*refind.MenuEntry) {
	for _, entry := range entries {
		if entry.SourceFile != "" && strings.HasSuffix(entry.SourceFile, "refind_linux.conf") {
			refindLinux = append(refindLinux, entry)
		} else {
			other = append(other, entry)
		}
	}
	return refindLinux, other
}

// applyRefindLinuxUpdates writes snapshot entries into each refind_linux.conf
// file that has at least one source entry matching the root subvolume.
// Returns true if any file was updated, so the caller can decide whether to
// also generate the managed include file.
func (p *Pipeline) applyRefindLinuxUpdates(gen *refind.Generator, refindLinuxEntries []*refind.MenuEntry, plan *Plan, patch *diff.PatchDiff, summary *OperationSummary) bool {
	rootSubvol := ""
	if plan.RootFS.Subvolume != nil {
		rootSubvol = strings.TrimPrefix(plan.RootFS.Subvolume.Path, "/")
	}

	// Only process entries whose subvol matches the root filesystem so we
	// don't pick up previously-generated snapshot entries from prior runs.
	filesByPath := make(map[string][]*refind.MenuEntry)
	for _, entry := range refindLinuxEntries {
		if entry.BootOptions == nil || entry.BootOptions.Subvol == "" {
			continue
		}
		if strings.TrimPrefix(entry.BootOptions.Subvol, "/") != rootSubvol {
			continue
		}
		filesByPath[entry.SourceFile] = append(filesByPath[entry.SourceFile], entry)
	}

	paths := make([]string, 0, len(filesByPath))
	for path := range filesByPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	updated := false
	for _, path := range paths {
		entries := filesByPath[path]
		log.Info().Str("source_file", path).Int("entries", len(entries)).Msg("Updating refind_linux.conf with snapshots")

		configDiff, err := gen.UpdateRefindLinuxConfWithAllEntries(plan.ProcessedSnapshots, entries, plan.RootFS)
		if err != nil {
			log.Error().Err(err).Str("source_file", path).Msg("Failed to update refind_linux.conf")
			continue
		}
		if configDiff == nil {
			continue
		}

		patch.AddFile(configDiff)
		summary.UpdatedConfigs = append(summary.UpdatedConfigs, configDiff.Path)
		updated = true
		for _, snapshot := range plan.ProcessedSnapshots {
			summary.AddedSnapshots = append(summary.AddedSnapshots, p.formatSnapshotName(snapshot))
		}
	}
	return updated
}

// maybeApplyManagedConfig writes the refind-btrfs-snapshots.conf include
// file when needed: either because refind_linux.conf wasn't updated and
// there are menuentry-style sources, or because the user passed
// --generate-include explicitly.
func (p *Pipeline) maybeApplyManagedConfig(gen *refind.Generator, parser *refind.Parser, configPath string, otherEntries, sourceEntries []*refind.MenuEntry, updatedRefindLinuxConf bool, plan *Plan, patch *diff.PatchDiff, summary *OperationSummary) {
	force := p.Cfg.GenerateInclude.IsTrue()
	shouldGenerate := (!updatedRefindLinuxConf && len(otherEntries) > 0 && len(plan.ProcessedSnapshots) > 0) || force

	if !shouldGenerate {
		if updatedRefindLinuxConf && len(otherEntries) > 0 {
			log.Info().
				Int("skipped_entries", len(otherEntries)).
				Msg("Skipping managed config generation - refind_linux.conf files were updated for this root volume")
		}
		return
	}

	managedConfigPath := parser.GetManagedConfigPath(configPath)
	entriesToUse := otherEntries
	if force && len(otherEntries) == 0 {
		entriesToUse = sourceEntries
	}

	log.Info().
		Int("entries", len(entriesToUse)).
		Int("snapshots", len(plan.ProcessedSnapshots)).
		Str("config_path", managedConfigPath).
		Bool("forced", force).
		Msg("Generating managed rEFInd config")

	configDiff, err := gen.GenerateManagedConfigDiff(entriesToUse, plan.ProcessedSnapshots, plan.RootFS, managedConfigPath)
	if err != nil {
		log.Error().Err(err).Msg("Failed to generate managed config")
		return
	}
	if configDiff == nil {
		return
	}

	patch.AddFile(configDiff)
	summary.UpdatedConfigs = append(summary.UpdatedConfigs, configDiff.Path)
	if len(summary.AddedSnapshots) == 0 {
		for _, snapshot := range plan.ProcessedSnapshots {
			summary.AddedSnapshots = append(summary.AddedSnapshots, p.formatSnapshotName(snapshot))
		}
	}
}

func (p *Pipeline) formatSnapshotName(snapshot *btrfs.Snapshot) string {
	return btrfs.FormatSnapshotTimeForMenu(snapshot.SnapshotTime, p.Cfg.Advanced.Naming.MenuFormat, p.Cfg.Display.LocalTime.IsTrue())
}
