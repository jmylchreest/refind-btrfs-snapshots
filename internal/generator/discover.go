package generator

import (
	"fmt"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/rs/zerolog/log"
)

// Discover runs the snapshot discovery and selection phase: gets the root
// filesystem, refuses to proceed if booted from a snapshot (unless --force),
// finds and selects snapshots, processes them for writability per the
// configured method, then filters out snapshots whose every boot plan is
// stale (when stale_snapshot_action=delete). Returns a Plan with the
// surviving snapshots and their boot plans.
func (p *Pipeline) Discover() (*Plan, error) {
	rootFS, err := p.Btrfs.GetRootFilesystem()
	if err != nil {
		return nil, fmt.Errorf("failed to get root filesystem: %w", err)
	}

	if !p.Cfg.Force && p.Cfg.Behavior.ExitOnSnapshotBoot {
		if p.Btrfs.IsSnapshotBootFromRootFS(rootFS) {
			log.Warn().Msg("Currently booted from a snapshot. Use --force to override or disable this check in config.")
			return nil, fmt.Errorf("refusing to generate configs while booted from snapshot")
		}
	}

	logRootFilesystem(rootFS)
	logLiveBootMode(p.Fstab, rootFS)

	snapshots, err := p.Btrfs.FindSnapshots(rootFS)
	if err != nil {
		return nil, fmt.Errorf("failed to find snapshots: %w", err)
	}
	if len(snapshots) == 0 {
		log.Info().Msg("No snapshots found")
	}

	selected := selectSnapshots(snapshots, p.Cfg.Snapshot.SelectionCount)
	log.Info().
		Int("total", len(snapshots)).
		Int("selected", len(selected)).
		Msg("Selected snapshots for processing")

	processed, err := p.processWritability(snapshots, selected)
	if err != nil {
		return nil, err
	}
	if len(processed) == 0 {
		log.Warn().Msg("No snapshots available for processing")
	}

	staleAction := kernel.ParseStaleAction(p.Cfg.Kernel.StaleSnapshotAction)
	var checker *kernel.Checker
	if len(p.BootSets) > 0 {
		checker = kernel.NewChecker(staleAction)
	}
	planner := kernel.NewPlanner(p.Fstab, checker, p.BootSets, rootFS)
	bootPlans := planner.Plan(processed)

	var removed []string
	if staleAction == kernel.ActionDelete {
		processed, removed = filterDeletedStale(processed, bootPlans)
		if len(processed) == 0 {
			log.Warn().Msg("All snapshots were stale and removed (stale_snapshot_action=delete)")
		}
		bootPlans = planner.Plan(processed)
	}

	return &Plan{
		RootFS:             rootFS,
		ProcessedSnapshots: processed,
		BootPlans:          bootPlans,
		Removed:            removed,
	}, nil
}

// selectSnapshots applies the configured selection count. Zero or negative
// means "all snapshots".
func selectSnapshots(snapshots []*btrfs.Snapshot, selectionCount int) []*btrfs.Snapshot {
	if selectionCount <= 0 {
		return snapshots
	}
	if selectionCount > len(snapshots) {
		selectionCount = len(snapshots)
	}
	return snapshots[:selectionCount]
}

// processWritability turns selected snapshots into a list of writable ones
// per the configured writable_method. For "toggle" it flips the read-only
// flag in place; for "copy" it creates writable copies in destination_dir.
func (p *Pipeline) processWritability(allSnapshots, selected []*btrfs.Snapshot) ([]*btrfs.Snapshot, error) {
	method := p.Cfg.Snapshot.WritableMethod
	log.Info().Str("method", method).Msg("Using writable snapshot method")

	switch method {
	case "toggle":
		for _, snap := range selected {
			if snap.IsReadOnly {
				if err := p.Btrfs.MakeSnapshotWritable(snap, p.Runner); err != nil {
					log.Error().Err(err).Str("path", snap.Path).Msg("Failed to make snapshot writable")
				}
			}
		}
		if p.Cfg.Behavior.CleanupOldSnapshots {
			if err := p.Btrfs.CleanupSnapshotWritability(allSnapshots, selected, p.Runner); err != nil {
				log.Warn().Err(err).Msg("Failed to cleanup snapshot writability")
			}
		}
		return selected, nil

	case "copy":
		destDir := p.Cfg.Snapshot.DestinationDir
		var processed []*btrfs.Snapshot
		for _, snap := range selected {
			if snap.IsReadOnly {
				log.Info().Str("source", snap.Path).Msg("Creating writable snapshot")
				copy, err := p.Btrfs.CreateWritableSnapshot(snap, destDir, p.Runner)
				if err != nil {
					log.Error().Err(err).Str("source", snap.Path).Msg("Failed to create writable snapshot")
					continue
				}
				processed = append(processed, copy)
			} else {
				processed = append(processed, snap)
			}
		}
		if p.Cfg.Behavior.CleanupOldSnapshots {
			if err := p.Btrfs.CleanupOldSnapshots(destDir, p.Cfg.Snapshot.SelectionCount, p.Runner); err != nil {
				log.Warn().Err(err).Msg("Failed to cleanup old snapshots")
			}
		}
		return processed, nil

	default:
		// Validate caught this at startup; unreachable in practice.
		return nil, fmt.Errorf("invalid writable_method: %s (must be 'toggle' or 'copy')", method)
	}
}

// filterDeletedStale drops snapshots whose every BootPlan is stale and
// has Action=delete. Returns the surviving snapshots plus the paths that
// were removed for the operation summary.
func filterDeletedStale(snapshots []*btrfs.Snapshot, plans []*kernel.BootPlan) (kept []*btrfs.Snapshot, removed []string) {
	plansBySnapshot := kernel.GroupBySnapshot(plans)
	for _, snapshot := range snapshots {
		snapPlans := plansBySnapshot[snapshot.Path]
		allSkip := len(snapPlans) > 0
		for _, plan := range snapPlans {
			if !plan.ShouldSkip() {
				allSkip = false
				break
			}
		}
		if allSkip {
			log.Info().Str("snapshot", snapshot.Path).Msg("Removing stale snapshot from generation (delete action)")
			removed = append(removed, snapshot.Path)
		} else {
			kept = append(kept, snapshot)
		}
	}
	return kept, removed
}
