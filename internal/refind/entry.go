package refind

import (
	"strconv"
	"strings"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/rs/zerolog/log"
)

// IsBootable reports whether a menu entry is a viable target for snapshot
// generation: it must have boot options with a root parameter, a subvol or
// subvolid in rootflags, and both must match the root filesystem.
func IsBootable(entry *MenuEntry, rootFS *btrfs.Filesystem) bool {
	if entry.BootOptions == nil {
		log.Trace().Str("title", entry.Title).Msg("Entry rejected: no boot options")
		return false
	}
	if entry.BootOptions.Root == "" {
		log.Trace().Str("title", entry.Title).Msg("Entry rejected: no root parameter")
		return false
	}
	if entry.BootOptions.Subvol == "" && entry.BootOptions.SubvolID == "" {
		log.Trace().
			Str("title", entry.Title).
			Str("subvol", entry.BootOptions.Subvol).
			Str("subvolid", entry.BootOptions.SubvolID).
			Msg("Entry rejected: no subvol or subvolid")
		return false
	}
	if !rootFS.MatchesDevice(entry.BootOptions.Root) {
		log.Trace().
			Str("title", entry.Title).
			Str("entry_root", entry.BootOptions.Root).
			Str("rootfs_device", rootFS.Device).
			Str("rootfs_uuid", rootFS.UUID).
			Msg("Entry rejected: device mismatch")
		return false
	}

	if rootFS.Subvolume != nil {
		if entry.BootOptions.Subvol != "" {
			entrySubvol := strings.TrimPrefix(entry.BootOptions.Subvol, "/")
			rootFSSubvol := strings.TrimPrefix(rootFS.Subvolume.Path, "/")
			if entrySubvol != rootFSSubvol {
				log.Trace().
					Str("title", entry.Title).
					Str("entry_subvol", entry.BootOptions.Subvol).
					Str("entry_subvol_normalized", entrySubvol).
					Str("rootfs_subvol", rootFS.Subvolume.Path).
					Str("rootfs_subvol_normalized", rootFSSubvol).
					Msg("Entry rejected: subvol mismatch")
				return false
			}
		}
		if entry.BootOptions.SubvolID != "" {
			if subvolID, err := strconv.ParseUint(entry.BootOptions.SubvolID, 10, 64); err == nil {
				if subvolID != rootFS.Subvolume.ID {
					log.Trace().
						Str("title", entry.Title).
						Uint64("entry_subvolid", subvolID).
						Uint64("rootfs_subvolid", rootFS.Subvolume.ID).
						Msg("Entry rejected: subvolid mismatch")
					return false
				}
			}
		}
	}

	log.Debug().
		Str("title", entry.Title).
		Str("root", entry.BootOptions.Root).
		Str("subvol", entry.BootOptions.Subvol).
		Str("subvolid", entry.BootOptions.SubvolID).
		Msg("Entry accepted as bootable")
	return true
}
