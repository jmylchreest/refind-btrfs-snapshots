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

// NewGenerator returns the UKI cloner as a bootloader.Generator. Apply
// is a package-level helper rather than a method because the binary
// writes and orphan removals are stateless operations on the Output.
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

	expected, err := buildClones(input.SourceUKIs, input.ProcessedSnapshots, outputDir, cfg.EntryPrefix)
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

type clonePlan struct {
	Path       string
	Content    []byte
	Descriptor string
	SnapshotID uint64
}

// buildClones reads each source UKI once and re-emits it per snapshot
// with the .cmdline rewritten. Reading once keeps memory bounded to a
// single source UKI at a time even when fanning out across many snapshots.
func buildClones(sources []*kernel.BootSet, snaps []*btrfs.Snapshot, outputDir, prefix string) ([]*clonePlan, error) {
	var plans []*clonePlan
	for _, src := range sources {
		if src == nil || src.UKI == nil {
			continue
		}
		srcPath := src.UKI.AbsPath
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
				Descriptor: fmt.Sprintf("# UKI clone\nsource: %s\ndest:   %s\ncmdline: %s\n", srcPath, dst, newCmdline),
				SnapshotID: snap.ID,
			})
		}
	}
	return plans, nil
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

// Apply writes every BinaryWrite via the runner and removes orphan
// FileDiffs (IsNew=false, Modified=""). It exists because the byte
// payloads can't go through diff.Apply — the text-diff pipeline would
// mangle the binary on display and is only safe for descriptor diffs.
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
