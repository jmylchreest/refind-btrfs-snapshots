// Package bootloader is the interface boundary between the snapshot
// discovery pipeline and per-bootloader emitters. Implementations consume
// a primitive-typed Input and return file diffs; they must not touch disk
// themselves — the pipeline applies the patch via the runner so dry-run
// and confirmation flows stay uniform across bootloaders.
package bootloader

import (
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/config"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/diff"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
)

type Generator interface {
	Name() string
	Generate(input Input) (*Output, error)
}

// Input carries every field any current or anticipated generator might
// need. Each implementation reads only what's relevant. SourceEntries
// originates from whatever native boot config the system already has
// (rEFInd menuentries today) and supplies the canonical cmdline/loader
// paths used to derive snapshot variants.
type Input struct {
	Cfg                *config.Config
	ESPPath            string
	RootFS             *btrfs.Filesystem
	ProcessedSnapshots []*btrfs.Snapshot
	BootPlans          []*kernel.BootPlan
	SourceEntries      []SourceEntry
}

type SourceEntry struct {
	Title   string
	Loader  string
	Initrd  []string
	Options string
}

type Output struct {
	Diffs          []*diff.FileDiff
	UpdatedConfigs []string
}
