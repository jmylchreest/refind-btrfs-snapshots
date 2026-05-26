package refind

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/params"
)

// updateOptionsForSnapshot updates boot options to point to the snapshot
func (g *Generator) updateOptionsForSnapshot(originalOptions string, snapshot *btrfs.Snapshot) string {
	if originalOptions == "" {
		return ""
	}

	parser := params.NewBootOptionsParser()
	options := originalOptions

	// Preserve the user's @ vs /@ subvolume format from the original config.
	rootflags := parser.ExtractRootFlags(originalOptions)
	originalSubvol := parser.ExtractSubvol(rootflags)

	var snapshotSubvol string

	var snapshotPathPart string
	if strings.HasPrefix(snapshot.Path, "@") {
		snapshotPathPart = strings.TrimPrefix(snapshot.Path, "@")
	} else {
		snapshotPathPart = snapshot.Path
	}

	if originalSubvol != "" && strings.HasPrefix(originalSubvol, "/@") {
		snapshotSubvol = "/@" + snapshotPathPart
	} else {
		snapshotSubvol = "@" + snapshotPathPart
	}

	options = parser.UpdateSubvol(options, snapshotSubvol)
	options = parser.UpdateSubvolID(options, fmt.Sprintf("%d", snapshot.ID))

	initrds := parser.SpaceParser.ExtractMultiple(options, "initrd")
	if len(initrds) > 0 {
		options = parser.SpaceParser.RemoveAll(options, "initrd")
		for _, initrd := range initrds {
			options = options + fmt.Sprintf(" initrd=%s", initrd)
		}
	}

	return options
}

// getSnapshotDisplayName generates a display name for a snapshot
func (g *Generator) getSnapshotDisplayName(snapshot *btrfs.Snapshot) string {
	if strings.HasPrefix(filepath.Base(snapshot.Path), "rwsnap_") {
		name := filepath.Base(snapshot.Path)
		parts := strings.Split(name, "_")
		if len(parts) >= 3 {
			timestamp := strings.Join(parts[1:len(parts)-1], "_")
			return timestamp
		}
	}

	return btrfs.FormatSnapshotTimeForMenu(snapshot.SnapshotTime, g.menuFormat, g.useLocalTime)
}
