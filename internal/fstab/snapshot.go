package fstab

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/diff"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/params"
	"github.com/rs/zerolog/log"
)

// UpdateSnapshotFstabDiff generates a diff for fstab changes without applying them
func (m *Manager) UpdateSnapshotFstabDiff(snapshot *btrfs.Snapshot, rootFS *btrfs.Filesystem) (*diff.FileDiff, error) {
	if snapshot == nil || snapshot.Subvolume == nil {
		return nil, fmt.Errorf("invalid snapshot provided")
	}

	fstabPath := btrfs.GetSnapshotFstabPath(snapshot)
	log.Debug().Str("path", fstabPath).Str("snapshot", snapshot.Path).Msg("Generating fstab diff")

	if _, err := os.Stat(fstabPath); errors.Is(err, os.ErrNotExist) {
		log.Warn().Str("path", fstabPath).Msg("Fstab file does not exist in snapshot")
		return nil, nil
	}

	originalContent, err := os.ReadFile(fstabPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read original fstab: %w", err)
	}

	fstab, err := m.ParseFstab(fstabPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse snapshot fstab: %w", err)
	}

	modified := false
	modifiedEntries := make(map[string]bool)
	for _, entry := range fstab.Entries {
		if m.isRootMount(entry, rootFS) {
			if m.updateRootEntry(entry, snapshot, rootFS) {
				modified = true
				modifiedEntries[entry.Original] = true
			}
		}
	}

	if !modified {
		log.Debug().Str("path", fstabPath).Msg("No changes needed in fstab")
		return nil, nil
	}

	newContent, err := m.generateFstabContentWithModifications(fstab, modifiedEntries)
	if err != nil {
		return nil, fmt.Errorf("failed to generate fstab content: %w", err)
	}

	return &diff.FileDiff{
		Path:     fstabPath,
		Original: string(originalContent),
		Modified: newContent,
		IsNew:    false,
	}, nil
}

// isRootMount determines if an fstab entry is for the root filesystem
func (m *Manager) isRootMount(entry *Entry, rootFS *btrfs.Filesystem) bool {
	if entry.Mountpoint != "/" {
		return false
	}
	if entry.FSType != "btrfs" {
		return false
	}
	return m.deviceMatches(entry.Device, rootFS)
}

// updateRootEntry updates a root mount entry for the snapshot
func (m *Manager) updateRootEntry(entry *Entry, snapshot *btrfs.Snapshot, rootFS *btrfs.Filesystem) bool {
	modified := false

	subvolPath := snapshot.Path
	if !strings.HasPrefix(subvolPath, "/") {
		subvolPath = "/" + subvolPath
	}
	newOptions := m.updateSubvolOption(entry.Options, subvolPath)
	if newOptions != entry.Options {
		entry.Options = newOptions
		modified = true
	}

	newOptions = m.updateSubvolidOption(entry.Options, snapshot.ID)
	if newOptions != entry.Options {
		entry.Options = newOptions
		modified = true
	}

	return modified
}

// updateSubvolOption updates the subvol option in mount options
func (m *Manager) updateSubvolOption(options, newSubvol string) string {
	parser := params.NewCommaParameterParser()
	return parser.Update(options, "subvol", newSubvol)
}

// updateSubvolidOption updates the subvolid option in mount options
func (m *Manager) updateSubvolidOption(options string, newSubvolid uint64) string {
	parser := params.NewCommaParameterParser()
	return parser.Update(options, "subvolid", fmt.Sprintf("%d", newSubvolid))
}

// deviceMatches checks if the fstab device specification matches the filesystem
func (m *Manager) deviceMatches(device string, rootFS *btrfs.Filesystem) bool {
	return rootFS.MatchesDevice(device)
}
