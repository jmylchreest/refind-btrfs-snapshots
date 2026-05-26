package btrfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/runner"
	"github.com/rs/zerolog/log"
)

// MakeSnapshotWritable changes a snapshot's read-only property to false
func (m *Manager) MakeSnapshotWritable(snapshot *Snapshot, r runner.Runner) error {
	return m.setSnapshotReadOnly(snapshot, false, r)
}

// MakeSnapshotReadOnly changes a snapshot's read-only property to true
func (m *Manager) MakeSnapshotReadOnly(snapshot *Snapshot, r runner.Runner) error {
	return m.setSnapshotReadOnly(snapshot, true, r)
}

// setSnapshotReadOnly sets the snapshot's read-only property
func (m *Manager) setSnapshotReadOnly(snapshot *Snapshot, readOnly bool, r runner.Runner) error {
	if snapshot == nil || snapshot.Subvolume == nil {
		return fmt.Errorf("invalid snapshot provided")
	}

	roValue := "false"
	desc := "writable"
	if readOnly {
		roValue = "true"
		desc = "read-only"
	}

	err := r.Command("btrfs", []string{"property", "set", snapshot.FilesystemPath, "ro", roValue},
		fmt.Sprintf("Make snapshot %s: %s", desc, snapshot.Path))
	if err != nil {
		return fmt.Errorf("failed to make snapshot %s: %w", desc, err)
	}

	if !r.IsDryRun() {
		snapshot.IsReadOnly = readOnly
	}

	return nil
}

// CreateWritableSnapshot creates a writable snapshot from a read-only snapshot
func (m *Manager) CreateWritableSnapshot(snapshot *Snapshot, destDir string, r runner.Runner) (*Snapshot, error) {
	if snapshot == nil || snapshot.Subvolume == nil {
		return nil, fmt.Errorf("invalid snapshot provided")
	}

	formattedTime := FormatSnapshotTimeForRwsnap(snapshot.SnapshotTime, m.rwsnapFormat, m.useLocalTime)
	snapshotName := fmt.Sprintf("rwsnap_%s_ID%d", formattedTime, snapshot.ID)
	destPath := filepath.Join(destDir, snapshotName)

	if err := r.MkdirAll(destDir, 0755, fmt.Sprintf("Create writable snapshot directory: %s", destDir)); err != nil {
		return nil, fmt.Errorf("failed to create destination directory: %w", err)
	}

	err := r.Command("btrfs", []string{"subvolume", "snapshot", snapshot.Path, destPath},
		fmt.Sprintf("Create writable snapshot: %s -> %s", snapshot.Path, destPath))
	if err != nil {
		return nil, fmt.Errorf("failed to create writable snapshot: %w", err)
	}

	if r.IsDryRun() {
		writable := &Snapshot{
			Subvolume:    snapshot.Subvolume,
			OriginalPath: snapshot.Path,
			SnapshotTime: snapshot.SnapshotTime,
		}
		writable.Path = destPath
		return writable, nil
	}

	newSnapshot, err := m.getSubvolumeInfo(destPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get new snapshot info: %w", err)
	}

	writable := &Snapshot{
		Subvolume:    newSnapshot,
		OriginalPath: snapshot.Path,
		SnapshotTime: snapshot.SnapshotTime,
	}

	return writable, nil
}

// GetSnapshotFstabPath returns the path to the fstab file in a snapshot
func GetSnapshotFstabPath(snapshot *Snapshot) string {
	return filepath.Join(snapshot.FilesystemPath, "etc", "fstab")
}

// findSnapshotsInDir recursively finds snapshots in a directory
func (m *Manager) findSnapshotsInDir(dir string, fs *Filesystem, depth int) ([]*Snapshot, error) {
	if depth > m.maxDepth {
		return nil, nil
	}

	var snapshots []*Snapshot

	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return snapshots, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %s: %w", dir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		entryPath := filepath.Join(dir, entry.Name())

		snapperSnapshotPath := filepath.Join(entryPath, "snapshot")
		snapperInfoPath := filepath.Join(entryPath, "info.xml")

		if _, err := os.Stat(snapperSnapshotPath); err == nil {
			if _, err := os.Stat(snapperInfoPath); err == nil {
				subvol, err := m.getSubvolumeInfo(snapperSnapshotPath)
				if err == nil {
					if m.isSnapshotOfRoot(subvol, fs.Subvolume) {
						info, err := entry.Info()
						if err != nil {
							log.Warn().Err(err).Str("path", entryPath).Msg("Failed to get file info")
							continue
						}

						snapshot := &Snapshot{
							Subvolume:      subvol,
							OriginalPath:   fs.Subvolume.Path,
							FilesystemPath: snapperSnapshotPath,
							SnapshotTime:   info.ModTime(),
						}

						m.applySnapperMetadata(snapshot, entryPath)
						snapshots = append(snapshots, snapshot)
						continue
					}
				}
			}
		}

		subvol, err := m.getSubvolumeInfo(entryPath)
		if err != nil {
			if depth < m.maxDepth {
				subSnapshots, err := m.findSnapshotsInDir(entryPath, fs, depth+1)
				if err != nil {
					log.Warn().Err(err).Str("path", entryPath).Msg("Failed to search subdirectory")
					continue
				}
				snapshots = append(snapshots, subSnapshots...)
			}
			continue
		}

		isSnapshot := m.isSnapshotOfRoot(subvol, fs.Subvolume)
		log.Debug().
			Str("path", entryPath).
			Str("subvol_path", subvol.Path).
			Bool("is_snapshot_flag", subvol.IsSnapshot).
			Bool("is_valid_snapshot", isSnapshot).
			Uint64("subvol_id", subvol.ID).
			Uint64("parent_id", subvol.ParentID).
			Msg("Evaluated potential snapshot")

		if isSnapshot {
			info, err := entry.Info()
			if err != nil {
				log.Warn().Err(err).Str("path", entryPath).Msg("Failed to get file info")
				continue
			}

			snapshot := &Snapshot{
				Subvolume:      subvol,
				OriginalPath:   fs.Subvolume.Path,
				FilesystemPath: entryPath,
				SnapshotTime:   info.ModTime(),
			}

			m.applySnapperMetadata(snapshot, entryPath)
			snapshots = append(snapshots, snapshot)
		}
	}

	return snapshots, nil
}

// isSnapshotOfRoot determines if a subvolume is a snapshot of the root subvolume
func (m *Manager) isSnapshotOfRoot(subvol, root *Subvolume) bool {
	if subvol == nil {
		return false
	}

	if root == nil {
		return subvol.IsSnapshot || m.looksLikeSnapshot(subvol)
	}

	if subvol.IsSnapshot {
		if subvol.ParentID == root.ID {
			return true
		}

		if subvol.ParentID == root.ParentID && root.ParentID != 0 {
			return true
		}
	}

	if m.looksLikeSnapshot(subvol) {
		if root.Generation > 0 && subvol.Generation > 0 && subvol.Generation <= root.Generation {
			return true
		}

		return true
	}

	return false
}

// looksLikeSnapshot uses heuristics to identify potential snapshots
func (m *Manager) looksLikeSnapshot(subvol *Subvolume) bool {
	if subvol == nil {
		return false
	}

	path := strings.ToLower(subvol.Path)
	snapshotPatterns := []string{
		"snapshot", ".snapshot", "snapshots", ".snapshots",
		"backup", "bkp", "@snapshot", "snap",
	}

	for _, pattern := range snapshotPatterns {
		if strings.Contains(path, pattern) {
			return true
		}
	}

	for _, searchDir := range m.searchDirs {
		if strings.HasPrefix(subvol.Path, searchDir) || strings.HasPrefix(subvol.Path, strings.TrimPrefix(searchDir, "/")) {
			return true
		}
	}

	return false
}

// CleanupOldSnapshots removes old writable snapshots from the destination directory
func (m *Manager) CleanupOldSnapshots(destDir string, keepCount int, r runner.Runner) error {
	if keepCount < 0 {
		return fmt.Errorf("keepCount must be non-negative")
	}

	log.Debug().Str("dest_dir", destDir).Int("keep_count", keepCount).Msg("Cleaning up old snapshots")

	entries, err := os.ReadDir(destDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("failed to read destination directory: %w", err)
	}

	var snapshots []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "rwsnap_") {
			snapshots = append(snapshots, entry.Name())
		}
	}

	slices.Sort(snapshots)

	if len(snapshots) > keepCount {
		toRemove := snapshots[:len(snapshots)-keepCount]
		for _, snapshot := range toRemove {
			snapshotPath := filepath.Join(destDir, snapshot)
			log.Info().Str("path", snapshotPath).Msg("Removing old snapshot")

			if _, err := m.getSubvolumeInfo(snapshotPath); err != nil {
				log.Warn().Err(err).Str("path", snapshotPath).Msg("Not a valid subvolume, skipping deletion")
				continue
			}

			if err := r.Command("btrfs", []string{"subvolume", "delete", snapshotPath}, "Remove old snapshot"); err != nil {
				log.Warn().Err(err).Str("path", snapshotPath).Msg("Failed to remove old snapshot")
			}
		}
	}

	return nil
}

// CleanupSnapshotWritability ensures only selected snapshots are writable
func (m *Manager) CleanupSnapshotWritability(allSnapshots []*Snapshot, selectedSnapshots []*Snapshot, r runner.Runner) error {
	log.Debug().Int("total", len(allSnapshots)).Int("selected", len(selectedSnapshots)).Msg("Cleaning up snapshot writability")

	selectedPaths := make(map[string]bool)
	for _, snapshot := range selectedSnapshots {
		selectedPaths[snapshot.Path] = true
	}

	for _, snapshot := range allSnapshots {
		if !selectedPaths[snapshot.Path] && !snapshot.IsReadOnly {
			if err := m.MakeSnapshotReadOnly(snapshot, r); err != nil {
				log.Warn().Err(err).Str("path", snapshot.Path).Msg("Failed to make snapshot read-only")
			}
		}
	}

	return nil
}
