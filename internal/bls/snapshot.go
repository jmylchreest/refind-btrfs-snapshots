package bls

import (
	"fmt"
	"strings"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/params"
)

// rewriteCmdline substitutes the snapshot's subvol path and subvolid into
// baseCmdline. Preserves the user's @ vs /@ subvolume-format preference.
func rewriteCmdline(baseCmdline string, snap *btrfs.Snapshot) string {
	if baseCmdline == "" {
		return ""
	}
	p := params.NewBootOptionsParser()

	rootflags := p.ExtractRootFlags(baseCmdline)
	originalSubvol := p.ExtractSubvol(rootflags)

	pathPart := strings.TrimPrefix(snap.Path, "@")
	var snapshotSubvol string
	if originalSubvol != "" && strings.HasPrefix(originalSubvol, "/@") {
		snapshotSubvol = "/@" + pathPart
	} else {
		snapshotSubvol = "@" + pathPart
	}

	out := p.UpdateSubvol(baseCmdline, snapshotSubvol)
	out = p.UpdateSubvolID(out, fmt.Sprintf("%d", snap.ID))
	return out
}
