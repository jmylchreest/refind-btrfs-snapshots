// Package snapshotfs computes diffs that align in-snapshot filesystem state
// (currently just /etc/fstab) with the snapshot's own subvolume. Helpers are
// pure — callers apply diffs via the shared runner — so running twice on an
// already-aligned snapshot produces zero diffs.
package snapshotfs

import (
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/diff"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/fstab"
	"github.com/rs/zerolog/log"
)

// FstabUpdate pairs a snapshot with its fstab diff so callers can build a
// summary log keyed off Snapshot.Path (the diff's filesystem path differs).
type FstabUpdate struct {
	Snapshot *btrfs.Snapshot
	Diff     *diff.FileDiff
}

// UpdateSnapshotFstab returns the fstab diff for a single snapshot. The diff
// is nil when no change is needed (idempotent). Errors here are returned, not
// logged, so callers can decide how to handle a single failure.
func UpdateSnapshotFstab(snap *btrfs.Snapshot, rootFS *btrfs.Filesystem, mgr *fstab.Manager) (*FstabUpdate, error) {
	d, err := mgr.UpdateSnapshotFstabDiff(snap, rootFS)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, nil
	}
	return &FstabUpdate{Snapshot: snap, Diff: d}, nil
}

// UpdateFstabs is a convenience wrapper that calls UpdateSnapshotFstab for
// each snapshot. Per-snapshot errors are logged at warn and the loop
// continues so one bad snapshot doesn't block the rest. Callers that need
// custom error handling, ordering, or parallelism should iterate themselves
// and call UpdateSnapshotFstab directly.
func UpdateFstabs(snapshots []*btrfs.Snapshot, rootFS *btrfs.Filesystem, mgr *fstab.Manager) []FstabUpdate {
	var out []FstabUpdate
	for _, snap := range snapshots {
		u, err := UpdateSnapshotFstab(snap, rootFS, mgr)
		if err != nil {
			log.Warn().Err(err).Str("snapshot", snap.Path).Msg("Failed to update snapshot fstab")
			continue
		}
		if u != nil {
			out = append(out, *u)
		}
	}
	return out
}
