// Package generator owns the snapshot generation pipeline: discovery,
// patch construction, and the operation summary. cmd/generate composes
// these phases; tests construct Pipeline values directly with typed Config
// and exercise each phase in isolation.
package generator

import (
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/config"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/fstab"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/runner"
)

// Pipeline bundles the dependencies needed for snapshot generation so each
// phase doesn't need to re-receive them. Construct once at the cmd boundary
// (with already-resolved managers, ESP path, and kernel scanner) then call
// Discover and BuildPatch in sequence.
type Pipeline struct {
	Cfg           *config.Config
	Btrfs         *btrfs.Manager
	Fstab         *fstab.Manager
	Runner        runner.Runner
	ESPPath       string
	KernelScanner *kernel.Scanner
	BootSets      []*kernel.BootSet
}

// Plan is the typed result of Pipeline.Discover: the snapshots that will
// actually be processed (post writability + stale-delete filtering) plus
// the bootability plans for each, and the list of snapshot paths the stale
// filter removed (for the operation summary).
type Plan struct {
	RootFS             *btrfs.Filesystem
	ProcessedSnapshots []*btrfs.Snapshot
	BootPlans          []*kernel.BootPlan
	Removed            []string
}
