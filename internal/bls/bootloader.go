package bls

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/bootloader"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/diff"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/rs/zerolog/log"
)

// NewGenerator returns the BLS Type #1 writer as a bootloader.Generator.
// Its Generate is a no-op when cfg.BLS.WriteEntries is false.
func NewGenerator() bootloader.Generator {
	return &generator{}
}

type generator struct{}

func (g *generator) Name() string { return "bls" }

func (g *generator) Generate(input bootloader.Input) (*bootloader.Output, error) {
	out := &bootloader.Output{}
	if input.Cfg == nil || !input.Cfg.BLS.WriteEntries.IsTrue() {
		return out, nil
	}

	cfg := input.Cfg.BLS
	entriesDir := filepath.Join(input.ESPPath, cfg.EntriesDir)

	expected, err := g.buildExpected(input, entriesDir, cfg.EntryPrefix)
	if err != nil {
		return nil, err
	}

	orphans, err := g.findOrphans(entriesDir, cfg.EntryPrefix, expected)
	if err != nil {
		return nil, err
	}

	for _, d := range expected {
		out.Diffs = append(out.Diffs, d)
	}
	out.Diffs = append(out.Diffs, orphans...)

	if len(out.Diffs) > 0 {
		out.UpdatedConfigs = append(out.UpdatedConfigs, cfg.EntriesDir)
	}

	log.Info().
		Int("entries", len(expected)).
		Int("orphans", len(orphans)).
		Str("dir", entriesDir).
		Msg("BLS entry generation complete")
	return out, nil
}

// buildExpected emits one BLS entry per (source entry × eligible snapshot)
// pair, following the same model the rEFInd generator uses: source entries
// supply the loader/initrd/cmdline verbatim, snapshots are reachable when
// at least one of their BootPlans is ESP-mode and not stale-delete.
//
// Returned map is keyed by absolute path for easy orphan diffing.
func (g *generator) buildExpected(input bootloader.Input, entriesDir, prefix string) (map[string]*diff.FileDiff, error) {
	expected := make(map[string]*diff.FileDiff)
	if len(input.SourceEntries) == 0 {
		return expected, nil
	}

	eligibleSnaps := eligibleSnapshots(input.BootPlans)
	if len(eligibleSnaps) == 0 {
		return expected, nil
	}

	for _, src := range input.SourceEntries {
		if src.Loader == "" {
			continue
		}
		titleSlug := slugify(src.Title)
		if titleSlug == "" {
			titleSlug = "entry"
		}
		for _, snap := range eligibleSnaps {
			if snap == nil || snap.Subvolume == nil {
				continue
			}

			entry := newEntryFromSource(snap, src, snapshotDisplayName(snap, input.Cfg.Advanced.Naming.MenuFormat, input.Cfg.Display.LocalTime.IsTrue()))
			if entry == nil {
				continue
			}

			id := fmt.Sprintf("%d-%s", snap.Subvolume.ID, titleSlug)
			filename := EntryFilename(prefix, id)
			abspath := filepath.Join(entriesDir, filename)

			var body bytes.Buffer
			if err := WriteEntry(&body, entry); err != nil {
				return nil, fmt.Errorf("write BLS entry for %s: %w", id, err)
			}

			original := ""
			isNew := true
			if existing, err := os.ReadFile(abspath); err == nil {
				original = string(existing)
				isNew = false
			}

			expected[abspath] = &diff.FileDiff{
				Path:     abspath,
				Original: original,
				Modified: body.String(),
				IsNew:    isNew,
			}
		}
	}
	return expected, nil
}

// eligibleSnapshots returns deduplicated *btrfs.Snapshot pointers for
// snapshots with at least one ESP-mode, non-UKI plan that isn't
// stale-delete. Btrfs-mode plans are excluded because systemd-boot can't
// traverse btrfs subvolumes to reach kernels inside the snapshot.
func eligibleSnapshots(plans []*kernel.BootPlan) []*btrfs.Snapshot {
	seen := make(map[string]bool, len(plans))
	out := make([]*btrfs.Snapshot, 0, len(plans))
	for _, p := range plans {
		if p == nil || p.Snapshot == nil || p.Mode != kernel.BootModeESP {
			continue
		}
		if p.Layout == kernel.LayoutUKI {
			continue
		}
		if p.ShouldSkip() {
			continue
		}
		if seen[p.Snapshot.Path] {
			continue
		}
		seen[p.Snapshot.Path] = true
		out = append(out, p.Snapshot)
	}
	return out
}

// newEntryFromSource builds a BLS Entry from a source entry's loader/initrd
// plus the snapshot-targeted cmdline.
func newEntryFromSource(snap *btrfs.Snapshot, src bootloader.SourceEntry, displayName string) *Entry {
	if snap == nil || snap.Subvolume == nil || src.Loader == "" {
		return nil
	}
	opts := rewriteCmdline(src.Options, snap)
	e := &Entry{
		Title:  fmt.Sprintf("%s (%s)", src.Title, displayName),
		Sort:   fmt.Sprintf("bls-btrfs-snapshots-%d", snap.Subvolume.ID),
		Linux:  src.Loader,
		Initrd: append([]string(nil), src.Initrd...),
	}
	if opts != "" {
		e.Options = []string{opts}
	}
	return e
}

// findOrphans returns FileDiff removals for any .conf in entriesDir that
// matches our prefix but isn't in the expected set.
func (g *generator) findOrphans(entriesDir, prefix string, expected map[string]*diff.FileDiff) ([]*diff.FileDiff, error) {
	if prefix == "" {
		// No prefix means we have no way to recognise our managed files;
		// refusing to scan is safer than risking unrelated deletions.
		return nil, nil
	}
	entries, err := os.ReadDir(entriesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read BLS entries dir: %w", err)
	}

	var orphans []*diff.FileDiff
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".conf") {
			continue
		}
		abspath := filepath.Join(entriesDir, name)
		if _, kept := expected[abspath]; kept {
			continue
		}
		body, err := os.ReadFile(abspath)
		if err != nil {
			log.Warn().Err(err).Str("path", abspath).Msg("Could not read orphan BLS entry for diff")
			continue
		}
		orphans = append(orphans, &diff.FileDiff{
			Path:     abspath,
			Original: string(body),
			Modified: "",
		})
	}
	return orphans, nil
}

// snapshotDisplayName mirrors the rEFInd generator's getSnapshotDisplayName
// so labels read the same across both binaries: rwsnap_<ts>_<id> directory
// names yield the embedded timestamp; everything else formats SnapshotTime
// through the user's menu_format + local_time settings.
func snapshotDisplayName(snap *btrfs.Snapshot, menuFormat string, useLocalTime bool) string {
	if snap == nil {
		return ""
	}
	if base := filepath.Base(snap.Path); strings.HasPrefix(base, "rwsnap_") {
		parts := strings.Split(base, "_")
		if len(parts) >= 3 {
			return strings.Join(parts[1:len(parts)-1], "_")
		}
	}
	return btrfs.FormatSnapshotTimeForMenu(snap.SnapshotTime, menuFormat, useLocalTime)
}

// slugify produces a filesystem-friendly identifier from a free-form title:
// lowercase, alnum + dashes only, runs of separators collapsed.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	out := make([]rune, 0, len(s))
	prevDash := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			out = append(out, r)
			prevDash = false
		default:
			if !prevDash && len(out) > 0 {
				out = append(out, '-')
				prevDash = true
			}
		}
	}
	for len(out) > 0 && out[len(out)-1] == '-' {
		out = out[:len(out)-1]
	}
	return string(out)
}
