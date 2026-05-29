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
// paths used to derive snapshot variants. SourceUKIs supplies the UKI
// generator with the live-system UKIs to clone (one clone per snapshot).
type Input struct {
	Cfg                *config.Config
	ESPPath            string
	RootFS             *btrfs.Filesystem
	ProcessedSnapshots []*btrfs.Snapshot
	BootPlans          []*kernel.BootPlan
	SourceEntries      []SourceEntry
	SourceUKIs         []*kernel.BootSet
}

type SourceEntry struct {
	Title   string
	Loader  string
	Initrd  []string
	Options string
}

// BinaryWrite is a planned out-of-band binary file write. The UKI generator
// uses it for cloned UKIs because their bytes are unsuitable for the
// text-diff FileDiff pipeline. Generators that emit only text (rEFInd, BLS)
// leave this slice nil.
type BinaryWrite struct {
	Path        string
	Content     []byte
	Description string
}

type Output struct {
	Diffs          []*diff.FileDiff
	UpdatedConfigs []string
	BinaryWrites   []*BinaryWrite
}
