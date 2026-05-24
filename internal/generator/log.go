package generator

import (
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/fstab"
	"github.com/rs/zerolog/log"
)

func logRootFilesystem(rootFS *btrfs.Filesystem) {
	logEntry := log.Info().
		Str("device", rootFS.Device).
		Str("identifier", rootFS.GetBestIdentifier()).
		Str("id_type", rootFS.GetIdentifierType())

	if rootFS.Subvolume != nil {
		logEntry.Str("subvolume", rootFS.Subvolume.Path)
	} else {
		logEntry.Str("subvolume", "<unknown>")
	}
	logEntry.Msg("Found root btrfs filesystem")
}

// logLiveBootMode inspects the live system's /etc/fstab and reports whether
// the running system has /boot inside btrfs or on a separate partition —
// the mode a snapshot taken right now would inherit.
func logLiveBootMode(fstabMgr *fstab.Manager, rootFS *btrfs.Filesystem) {
	liveFstab, err := fstabMgr.ParseFstab("/etc/fstab")
	if err != nil {
		log.Debug().Err(err).Msg("Could not parse live /etc/fstab for boot mode detection")
		return
	}

	info := fstabMgr.AnalyzeBootMount(liveFstab, rootFS)
	logEntry := log.Info()

	if info.BootOnSameBtrfs {
		logEntry.Str("boot_mode", "btrfs").
			Msg("Live system has /boot inside btrfs (snapshots will contain their own kernels)")
	} else {
		logEntry.Str("boot_mode", "esp")
		if info.Entry != nil {
			logEntry.Str("boot_device", info.Entry.Device).
				Str("boot_fstype", info.Entry.FSType)
		}
		logEntry.Msg("Live system has /boot on separate partition (snapshots depend on ESP kernels)")
	}
}
