package uki

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/bootloader"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/diff"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/runner"
	"github.com/rs/zerolog/log"
)

// NewGenerator returns the UKI cloner as a bootloader.Generator. Its
// Generate is a no-op when cfg.UKI.WriteEntries is false. Apply is a
// package-level helper rather than a method so the cmd can reuse the
// same orphan-removal path even after the generator instance is gone.
func NewGenerator() bootloader.Generator {
	return &generator{}
}

type generator struct{}

func (g *generator) Name() string { return "uki" }

func (g *generator) Generate(input bootloader.Input) (*bootloader.Output, error) {
	out := &bootloader.Output{}
	if input.Cfg == nil || !input.Cfg.UKI.WriteEntries.IsTrue() {
		return out, nil
	}

	cfg := input.Cfg.UKI
	outputDir := filepath.Join(input.ESPPath, strings.TrimPrefix(cfg.OutputDir, "/"))

	sources := filterSources(input.SourceUKIs, cfg.EntryPrefix)
	expected, err := buildClones(sources, input.ProcessedSnapshots, outputDir, cfg.EntryPrefix)
	if err != nil {
		return nil, err
	}

	for _, e := range expected {
		out.BinaryWrites = append(out.BinaryWrites, &bootloader.BinaryWrite{
			Path:        e.Path,
			Content:     e.Content,
			Description: fmt.Sprintf("Clone UKI for snapshot %d", e.SnapshotID),
		})
		out.Diffs = append(out.Diffs, &diff.FileDiff{
			Path:     e.Path,
			IsNew:    true,
			Original: "",
			Modified: e.Descriptor,
		})
	}

	expectedPaths := make(map[string]bool, len(expected))
	for _, e := range expected {
		expectedPaths[e.Path] = true
	}
	orphans, err := findOrphans(outputDir, cfg.EntryPrefix, expectedPaths)
	if err != nil {
		return nil, err
	}
	out.Diffs = append(out.Diffs, orphans...)

	if len(out.Diffs) > 0 || len(out.BinaryWrites) > 0 {
		out.UpdatedConfigs = append(out.UpdatedConfigs, cfg.OutputDir)
	}

	log.Info().
		Int("clones", len(out.BinaryWrites)).
		Int("orphans", len(orphans)).
		Str("dir", outputDir).
		Msg("UKI clone planning complete")
	return out, nil
}

// clonePlan is the internal record returned by buildClones: an in-memory
// (path, bytes, descriptor) triple per (snapshot × source) pair. Descriptor
// is a short text record used for the dry-run/confirm display so the user
// sees what would change without dumping binary into the patch.
type clonePlan struct {
	Path       string
	Content    []byte
	Descriptor string
	SnapshotID uint64
}

// buildClones reads each source UKI from disk, rewrites its .cmdline for
// each snapshot, and returns the planned writes. The source bytes are read
// once and re-cloned per snapshot to avoid keeping multiple full UKIs
// resident; this is acceptable because the parse cost is small relative to
// the write cost.
func buildClones(sources []*kernel.BootSet, snaps []*btrfs.Snapshot, outputDir, prefix string) ([]*clonePlan, error) {
	var plans []*clonePlan
	for _, src := range sources {
		if src == nil || src.UKI == nil {
			continue
		}
		srcPath := sourceAbsPath(src)
		srcBytes, err := os.ReadFile(srcPath)
		if err != nil {
			return nil, fmt.Errorf("read source UKI %s: %w", srcPath, err)
		}
		baseCmdline, err := cmdlineFromBytes(srcBytes)
		if err != nil {
			return nil, fmt.Errorf("read cmdline from %s: %w", srcPath, err)
		}
		baseName := strings.TrimSuffix(filepath.Base(src.UKI.Path), ".efi")
		for _, snap := range snaps {
			if snap == nil || snap.Subvolume == nil || snap.Path == "" {
				continue
			}
			newCmdline := rewriteCmdline(baseCmdline, snap)
			clone, err := CloneWithCmdline(srcBytes, newCmdline)
			if err != nil {
				return nil, fmt.Errorf("clone %s for snapshot %d: %w", srcPath, snap.ID, err)
			}
			dst := filepath.Join(outputDir, fmt.Sprintf("%s%d-%s.efi", prefix, snap.ID, baseName))
			plans = append(plans, &clonePlan{
				Path:       dst,
				Content:    clone,
				Descriptor: descriptorFor(srcPath, dst, newCmdline),
				SnapshotID: snap.ID,
			})
		}
	}
	return plans, nil
}

// sourceAbsPath returns the absolute filesystem path of a source UKI,
// preferring AbsPath but falling back to Path so test fixtures that
// only populate one field still work.
func sourceAbsPath(src *kernel.BootSet) string {
	if src.UKI.AbsPath != "" {
		return src.UKI.AbsPath
	}
	return src.UKI.Path
}

// filterSources drops sources whose UKI filename already matches the
// managed prefix — those are our own previous clones and must never be
// used as sources, or each run would double the clone count.
func filterSources(sources []*kernel.BootSet, prefix string) []*kernel.BootSet {
	var out []*kernel.BootSet
	for _, s := range sources {
		if s == nil || s.UKI == nil {
			continue
		}
		if prefix != "" && strings.HasPrefix(filepath.Base(s.UKI.Path), prefix) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func findOrphans(outputDir, prefix string, expected map[string]bool) ([]*diff.FileDiff, error) {
	if prefix == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read UKI output dir: %w", err)
	}
	var orphans []*diff.FileDiff
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".efi") {
			continue
		}
		abspath := filepath.Join(outputDir, name)
		if expected[abspath] {
			continue
		}
		orphans = append(orphans, &diff.FileDiff{
			Path:     abspath,
			IsNew:    false,
			Original: fmt.Sprintf("[managed UKI clone, %d bytes]\n", fileSize(abspath)),
			Modified: "",
		})
	}
	return orphans, nil
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func descriptorFor(srcPath, dstPath, newCmdline string) string {
	return fmt.Sprintf("# UKI clone\nsource: %s\ndest:   %s\ncmdline: %s\n", srcPath, dstPath, newCmdline)
}

// Apply writes every BinaryWrite via the runner and removes orphan
// FileDiffs (those with IsNew=false && Modified=""). The text-diff
// pipeline (diff.Apply) handles the descriptor FileDiffs as a no-op
// write — Apply here is the binary-aware counterpart.
func Apply(out *bootloader.Output, r runner.Runner) error {
	if out == nil {
		return nil
	}
	var errs []error

	for _, bw := range out.BinaryWrites {
		if err := r.MkdirAll(filepath.Dir(bw.Path), 0o755, fmt.Sprintf("Create directory for %s", bw.Path)); err != nil {
			errs = append(errs, fmt.Errorf("mkdir %s: %w", filepath.Dir(bw.Path), err))
			continue
		}
		if err := r.WriteFile(bw.Path, bw.Content, 0o644, bw.Description); err != nil {
			errs = append(errs, fmt.Errorf("write %s: %w", bw.Path, err))
			continue
		}
	}

	for _, d := range out.Diffs {
		if d.IsNew || d.Modified != "" {
			continue
		}
		if r.IsDryRun() {
			log.Info().Str("path", d.Path).Msg("[DRY RUN] Would remove orphan UKI clone")
			continue
		}
		if err := os.Remove(d.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove orphan %s: %w", d.Path, err))
			continue
		}
		log.Info().Str("path", d.Path).Msg("Removed orphan UKI clone")
	}

	if len(errs) > 0 {
		return fmt.Errorf("UKI apply: %d failures: %w", len(errs), errors.Join(errs...))
	}
	return nil
}
