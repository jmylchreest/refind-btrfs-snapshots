package fstab

import (
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/rs/zerolog/log"
)

// BootMountInfo describes how /boot is mounted according to an fstab.
type BootMountInfo struct {
	// HasSeparateBootMount is true if /boot has an explicit fstab entry
	// on a non-btrfs filesystem (e.g., vfat ESP partition).
	HasSeparateBootMount bool

	// BootOnSameBtrfs is true if /boot is either:
	//   - not separately mounted (part of the root btrfs subvolume), or
	//   - mounted from the same btrfs filesystem as root.
	// When true, snapshot boot images are self-contained.
	BootOnSameBtrfs bool

	// Entry is the fstab entry for /boot, if one exists. Nil if /boot
	// is not separately mounted.
	Entry *Entry
}

// AnalyzeBootMount inspects a parsed fstab to determine how /boot is mounted.
// rootFS is used to check whether a btrfs /boot mount is on the same filesystem
// as the root subvolume. If rootFS is nil, any btrfs /boot mount is assumed to
// be on a different filesystem (conservative).
func (m *Manager) AnalyzeBootMount(fstab *Fstab, rootFS *btrfs.Filesystem) *BootMountInfo {
	if fstab == nil {
		return &BootMountInfo{BootOnSameBtrfs: true}
	}

	for _, entry := range fstab.Entries {
		if entry.Mountpoint != "/boot" {
			continue
		}

		if entry.FSType != "btrfs" {
			log.Debug().
				Str("device", entry.Device).
				Str("fstype", entry.FSType).
				Msg("Snapshot fstab has /boot on non-btrfs filesystem (ESP mode)")
			return &BootMountInfo{
				HasSeparateBootMount: true,
				BootOnSameBtrfs:      false,
				Entry:                entry,
			}
		}

		if rootFS != nil && rootFS.MatchesDevice(entry.Device) {
			log.Debug().
				Str("device", entry.Device).
				Msg("Snapshot fstab has /boot on same btrfs filesystem as root")
			return &BootMountInfo{
				HasSeparateBootMount: true,
				BootOnSameBtrfs:      true,
				Entry:                entry,
			}
		}

		log.Debug().
			Str("device", entry.Device).
			Msg("Snapshot fstab has /boot on different btrfs filesystem")
		return &BootMountInfo{
			HasSeparateBootMount: true,
			BootOnSameBtrfs:      false,
			Entry:                entry,
		}
	}

	log.Debug().Msg("Snapshot fstab has no /boot mount (part of root btrfs)")
	return &BootMountInfo{
		HasSeparateBootMount: false,
		BootOnSameBtrfs:      true,
	}
}
