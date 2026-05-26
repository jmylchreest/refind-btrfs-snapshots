package btrfs

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rs/zerolog/log"
)

// Manager handles btrfs filesystem operations
type Manager struct {
	searchDirs   []string
	maxDepth     int
	rwsnapFormat string
	useLocalTime bool
}

// NewManager creates a new btrfs manager.
// rwsnapFormat is the time.Format layout used for naming writable snapshot
// copies (e.g. "2006-01-02_15-04-05"); useLocalTime renders the timestamp
// in local time instead of UTC.
func NewManager(searchDirs []string, maxDepth int, rwsnapFormat string, useLocalTime bool) *Manager {
	if rwsnapFormat == "" {
		rwsnapFormat = "2006-01-02_15-04-05"
	}
	return &Manager{
		searchDirs:   searchDirs,
		maxDepth:     maxDepth,
		rwsnapFormat: rwsnapFormat,
		useLocalTime: useLocalTime,
	}
}

// DetectBtrfsFilesystems discovers all btrfs filesystems on the system
func (m *Manager) DetectBtrfsFilesystems() ([]*Filesystem, error) {
	log.Debug().Msg("Detecting btrfs filesystems")

	mounts, err := m.getMountedFilesystems()
	if err != nil {
		return nil, fmt.Errorf("failed to get mounted filesystems: %w", err)
	}

	var filesystems []*Filesystem

	for _, mount := range mounts {
		if mount.Fstype != "btrfs" {
			continue
		}
		fs := &Filesystem{
			UUID:       mount.UUID,
			PartUUID:   mount.PartUUID,
			Label:      mount.Label,
			PartLabel:  mount.PartLabel,
			Device:     mount.Device,
			MountPoint: mount.Mountpoint,
		}

		subvol, err := m.getRootSubvolume(mount.Mountpoint)
		if err != nil {
			log.Warn().Err(err).Str("mountpoint", mount.Mountpoint).Msg("Failed to get root subvolume")
		} else {
			fs.Subvolume = subvol
		}

		filesystems = append(filesystems, fs)
	}

	log.Info().Int("count", len(filesystems)).Msg("Found btrfs filesystems")
	return filesystems, nil
}

// FindSnapshots finds all snapshots for the given filesystem
func (m *Manager) FindSnapshots(fs *Filesystem) ([]*Snapshot, error) {
	log.Debug().Str("filesystem", fs.GetBestIdentifier()).Str("id_type", fs.GetIdentifierType()).Msg("Finding snapshots")

	var allSnapshots []*Snapshot

	for _, searchDir := range m.searchDirs {
		searchPath := searchDir
		if !filepath.IsAbs(searchPath) {
			searchPath = filepath.Join(fs.MountPoint, searchDir)
		}

		snapshots, err := m.findSnapshotsInDir(searchPath, fs, 0)
		if err != nil {
			log.Warn().Err(err).Str("search_dir", searchPath).Msg("Failed to find snapshots in directory")
			continue
		}

		allSnapshots = append(allSnapshots, snapshots...)
	}

	slices.SortFunc(allSnapshots, func(a, b *Snapshot) int {
		return b.SnapshotTime.Compare(a.SnapshotTime)
	})

	log.Debug().Int("count", len(allSnapshots)).Str("filesystem", fs.GetBestIdentifier()).Str("id_type", fs.GetIdentifierType()).Msg("Found snapshots")
	return allSnapshots, nil
}

// GetRootFilesystem finds the filesystem that contains the root mount point
func (m *Manager) GetRootFilesystem() (*Filesystem, error) {
	filesystems, err := m.DetectBtrfsFilesystems()
	if err != nil {
		return nil, err
	}

	for _, fs := range filesystems {
		if fs.MountPoint == "/" {
			return fs, nil
		}
	}

	return nil, fmt.Errorf("no btrfs filesystem mounted at root")
}

// IsSnapshotBootFromRootFS checks if we're booted from a snapshot using an existing root filesystem.
// This is a heuristic check based on subvolume path naming and the IsSnapshot flag.
func (m *Manager) IsSnapshotBootFromRootFS(rootFS *Filesystem) bool {
	if rootFS.Subvolume == nil {
		return false
	}

	path := rootFS.Subvolume.Path
	return strings.Contains(path, "snapshot") ||
		strings.Contains(path, "rwsnap") ||
		rootFS.Subvolume.IsSnapshot
}
