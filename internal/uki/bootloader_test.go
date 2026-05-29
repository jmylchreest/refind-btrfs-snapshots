package uki

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/bootloader"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/config"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/runner"
	pkguki "github.com/jmylchreest/refind-btrfs-snapshots/pkg/uki"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Contract:
//   - Generate is a no-op when Cfg.UKI.WriteEntries is false.
//   - Emits one BinaryWrite per (ProcessedSnapshot × SourceUKI).
//   - Each BinaryWrite contains a UKI whose .cmdline references the
//     snapshot's subvol path and ID.
//   - Diffs are descriptor-only text records so the dry-run/confirm UX
//     shows what would change without dumping binary into the patch.
//   - On-disk files in OutputDir matching the prefix that aren't in the
//     expected set are emitted as orphan FileDiff removals (Modified="").
//
// Filtering of input.SourceUKIs (Layout=UKI, managed-prefix recursion
// guard) is the cmd's job — see cmd/uki-btrfs-snapshots filter tests.

func cfgWith(write bool, outputDir, prefix string) *config.Config {
	return &config.Config{
		UKI: config.UKIConfig{
			WriteEntries: config.Truthy(write),
			OutputDir:    outputDir,
			EntryPrefix:  prefix,
		},
	}
}

func newSnap(id uint64, path string) *btrfs.Snapshot {
	return &btrfs.Snapshot{
		Subvolume:    &btrfs.Subvolume{ID: id, Path: path},
		SnapshotTime: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

func srcUKI(t *testing.T, name string) *kernel.BootSet {
	t.Helper()
	abs, err := filepath.Abs("../../pkg/uki/testdata/uki-single-profile.efi")
	require.NoError(t, err)
	return &kernel.BootSet{
		KernelName: name,
		Layout:     kernel.LayoutUKI,
		UKI: &kernel.BootImage{
			Path:     "/EFI/Linux/" + name + ".efi",
			AbsPath:  abs,
			Filename: name + ".efi",
			Role:     kernel.RoleUKI,
		},
	}
}

func TestUKIGenerator_DisabledByDefault(t *testing.T) {
	espDir := t.TempDir()
	input := bootloader.Input{
		Cfg:                cfgWith(false, "/EFI/Linux", "uki-btrfs-snapshots-"),
		ESPPath:            espDir,
		ProcessedSnapshots: []*btrfs.Snapshot{newSnap(256, "@/.snapshots/1/snapshot")},
		SourceUKIs:         []*kernel.BootSet{srcUKI(t, "linux")},
	}
	out, err := NewGenerator().Generate(input)
	require.NoError(t, err)
	assert.Empty(t, out.Diffs)
	assert.Empty(t, out.BinaryWrites)
}

func TestUKIGenerator_EmitsBinaryWritePerPair(t *testing.T) {
	espDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(espDir, "EFI", "Linux"), 0o755))

	snaps := []*btrfs.Snapshot{
		newSnap(256, "@/.snapshots/1/snapshot"),
		newSnap(257, "@/.snapshots/2/snapshot"),
	}
	srcs := []*kernel.BootSet{srcUKI(t, "linux")}

	input := bootloader.Input{
		Cfg:                cfgWith(true, "/EFI/Linux", "uki-btrfs-snapshots-"),
		ESPPath:            espDir,
		ProcessedSnapshots: snaps,
		SourceUKIs:         srcs,
	}

	out, err := NewGenerator().Generate(input)
	require.NoError(t, err)
	require.Len(t, out.BinaryWrites, 2, "expected one clone per (snapshot × source)")
	require.Len(t, out.Diffs, 2, "one descriptor diff per binary write")

	for _, bw := range out.BinaryWrites {
		assert.Contains(t, bw.Path, filepath.Join(espDir, "EFI", "Linux", "uki-btrfs-snapshots-"))
		assert.True(t, len(bw.Content) > 0, "clone bytes must be non-empty")

		img, err := pkguki.Parse(bytes.NewReader(bw.Content))
		require.NoError(t, err, "binary content must parse as a UKI")
		assert.Contains(t, img.Cmdline(), "subvol=@/.snapshots/", "cloned cmdline must reference snapshot subvol")
	}

	for _, d := range out.Diffs {
		assert.True(t, d.IsNew)
		assert.Contains(t, d.Modified, "subvol=@/.snapshots/", "diff descriptor must mention rewritten cmdline")
	}
}

func TestUKIGenerator_Name(t *testing.T) {
	assert.Equal(t, "uki", NewGenerator().Name())
}

func TestUKIGenerator_OrphanCleanup(t *testing.T) {
	espDir := t.TempDir()
	outDir := filepath.Join(espDir, "EFI", "Linux")
	require.NoError(t, os.MkdirAll(outDir, 0o755))

	// pre-populate the output dir with: one matching expected (300, kept),
	// two orphans (50, 99), one unrelated (no prefix → untouched).
	pre := map[string]string{
		"uki-btrfs-snapshots-50-linux.efi": "old",
		"uki-btrfs-snapshots-99-linux.efi": "old",
		"linux.efi":                        "not managed",
	}
	for name, body := range pre {
		require.NoError(t, os.WriteFile(filepath.Join(outDir, name), []byte(body), 0o644))
	}

	input := bootloader.Input{
		Cfg:                cfgWith(true, "/EFI/Linux", "uki-btrfs-snapshots-"),
		ESPPath:            espDir,
		ProcessedSnapshots: []*btrfs.Snapshot{newSnap(300, "@/.snapshots/3/snapshot")},
		SourceUKIs:         []*kernel.BootSet{srcUKI(t, "linux")},
	}

	out, err := NewGenerator().Generate(input)
	require.NoError(t, err)

	var removals []string
	for _, d := range out.Diffs {
		if !d.IsNew && d.Modified == "" {
			removals = append(removals, filepath.Base(d.Path))
		}
	}
	assert.ElementsMatch(t, []string{
		"uki-btrfs-snapshots-50-linux.efi",
		"uki-btrfs-snapshots-99-linux.efi",
	}, removals)
}

func TestApply_WritesBinaryAndRemovesOrphans(t *testing.T) {
	espDir := t.TempDir()
	outDir := filepath.Join(espDir, "EFI", "Linux")
	require.NoError(t, os.MkdirAll(outDir, 0o755))

	orphanPath := filepath.Join(outDir, "uki-btrfs-snapshots-99-linux.efi")
	require.NoError(t, os.WriteFile(orphanPath, []byte("stale"), 0o644))

	input := bootloader.Input{
		Cfg:                cfgWith(true, "/EFI/Linux", "uki-btrfs-snapshots-"),
		ESPPath:            espDir,
		ProcessedSnapshots: []*btrfs.Snapshot{newSnap(300, "@/.snapshots/3/snapshot")},
		SourceUKIs:         []*kernel.BootSet{srcUKI(t, "linux")},
	}

	gen := NewGenerator()
	out, err := gen.Generate(input)
	require.NoError(t, err)

	require.NoError(t, Apply(out, runner.New(false), nil))

	// the new clone exists and parses as a UKI
	clones, err := filepath.Glob(filepath.Join(outDir, "uki-btrfs-snapshots-300-*.efi"))
	require.NoError(t, err)
	require.Len(t, clones, 1)
	body, err := os.ReadFile(clones[0])
	require.NoError(t, err)
	img, err := pkguki.Parse(bytes.NewReader(body))
	require.NoError(t, err)
	assert.Contains(t, img.Cmdline(), "subvolid=300")

	// the orphan got removed
	_, err = os.Stat(orphanPath)
	assert.True(t, os.IsNotExist(err), "orphan should be removed, got err=%v", err)
}

func TestApply_DryRunTouchesNothing(t *testing.T) {
	espDir := t.TempDir()
	outDir := filepath.Join(espDir, "EFI", "Linux")
	require.NoError(t, os.MkdirAll(outDir, 0o755))

	orphanPath := filepath.Join(outDir, "uki-btrfs-snapshots-99-linux.efi")
	require.NoError(t, os.WriteFile(orphanPath, []byte("stale"), 0o644))

	input := bootloader.Input{
		Cfg:                cfgWith(true, "/EFI/Linux", "uki-btrfs-snapshots-"),
		ESPPath:            espDir,
		ProcessedSnapshots: []*btrfs.Snapshot{newSnap(300, "@/.snapshots/3/snapshot")},
		SourceUKIs:         []*kernel.BootSet{srcUKI(t, "linux")},
	}
	out, err := NewGenerator().Generate(input)
	require.NoError(t, err)

	require.NoError(t, Apply(out, runner.New(true), nil))

	// orphan stays on disk
	_, err = os.Stat(orphanPath)
	assert.NoError(t, err, "dry-run must not remove orphans")

	// no new clones written
	clones, err := filepath.Glob(filepath.Join(outDir, "uki-btrfs-snapshots-300-*.efi"))
	require.NoError(t, err)
	assert.Empty(t, clones, "dry-run must not write clones")
}

func TestApply_NilOutput(t *testing.T) {
	assert.NoError(t, Apply(nil, runner.New(false), nil))
}

func TestSubstituteTemplate_ReplacesBraces(t *testing.T) {
	tests := []struct {
		tmpl []string
		path string
		want []string
	}{
		{[]string{"peseal", "sign", "{}"}, "/boot/x.efi", []string{"peseal", "sign", "/boot/x.efi"}},
		{[]string{"sbsign", "--output", "{}", "{}"}, "/a.efi", []string{"sbsign", "--output", "/a.efi", "/a.efi"}},
		{[]string{"echo", "no-placeholder"}, "/x", []string{"echo", "no-placeholder"}},
		{nil, "/x", nil},
	}
	for _, tt := range tests {
		got := substituteTemplate(tt.tmpl, tt.path)
		assert.Equal(t, tt.want, got)
	}
}

func writeFakeSigner(t *testing.T, dir string) string {
	t.Helper()
	script := filepath.Join(dir, "fake-signer")
	body := "#!/bin/sh\nprintf '%s\\n' \"$1\" >> " + filepath.Join(dir, "calls.log") + "\n"
	require.NoError(t, os.WriteFile(script, []byte(body), 0o755))
	return script
}

func TestApply_RunsSignCommandAfterEachClone(t *testing.T) {
	espDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(espDir, "EFI", "Linux"), 0o755))

	input := bootloader.Input{
		Cfg:     cfgWith(true, "/EFI/Linux", "uki-btrfs-snapshots-"),
		ESPPath: espDir,
		ProcessedSnapshots: []*btrfs.Snapshot{
			newSnap(256, "@/.snapshots/1/snapshot"),
			newSnap(257, "@/.snapshots/2/snapshot"),
		},
		SourceUKIs: []*kernel.BootSet{srcUKI(t, "linux")},
	}
	out, err := NewGenerator().Generate(input)
	require.NoError(t, err)

	signer := writeFakeSigner(t, espDir)
	signCmd := []string{signer, "{}"}

	require.NoError(t, Apply(out, runner.New(false), signCmd))

	log, err := os.ReadFile(filepath.Join(espDir, "calls.log"))
	require.NoError(t, err)
	calls := strings.Split(strings.TrimSpace(string(log)), "\n")
	assert.Len(t, calls, 2, "sign command must run exactly once per clone")
	for _, p := range calls {
		assert.True(t, strings.HasSuffix(p, ".efi"), "called paths must be the clone paths")
	}
}

func TestApply_NoSignCommandSkipsExec(t *testing.T) {
	espDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(espDir, "EFI", "Linux"), 0o755))
	input := bootloader.Input{
		Cfg:                cfgWith(true, "/EFI/Linux", "uki-btrfs-snapshots-"),
		ESPPath:            espDir,
		ProcessedSnapshots: []*btrfs.Snapshot{newSnap(256, "@/.snapshots/1/snapshot")},
		SourceUKIs:         []*kernel.BootSet{srcUKI(t, "linux")},
	}
	out, _ := NewGenerator().Generate(input)
	require.NoError(t, Apply(out, runner.New(false), nil), "nil sign command must be a no-op, not an error")
}

func TestApply_SignCommandFailureAggregatesIntoErrors(t *testing.T) {
	espDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(espDir, "EFI", "Linux"), 0o755))
	input := bootloader.Input{
		Cfg:                cfgWith(true, "/EFI/Linux", "uki-btrfs-snapshots-"),
		ESPPath:            espDir,
		ProcessedSnapshots: []*btrfs.Snapshot{newSnap(256, "@/.snapshots/1/snapshot")},
		SourceUKIs:         []*kernel.BootSet{srcUKI(t, "linux")},
	}
	out, _ := NewGenerator().Generate(input)

	// /bin/false exits 1 unconditionally — every clone's sign step fails.
	err := Apply(out, runner.New(false), []string{"/bin/false", "{}"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sign", "sign failures must be visible in the aggregated error")
}

func TestApply_SignSkippedInDryRun(t *testing.T) {
	espDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(espDir, "EFI", "Linux"), 0o755))
	input := bootloader.Input{
		Cfg:                cfgWith(true, "/EFI/Linux", "uki-btrfs-snapshots-"),
		ESPPath:            espDir,
		ProcessedSnapshots: []*btrfs.Snapshot{newSnap(256, "@/.snapshots/1/snapshot")},
		SourceUKIs:         []*kernel.BootSet{srcUKI(t, "linux")},
	}
	out, _ := NewGenerator().Generate(input)

	signer := writeFakeSigner(t, espDir)
	require.NoError(t, Apply(out, runner.New(true), []string{signer, "{}"}))

	_, err := os.Stat(filepath.Join(espDir, "calls.log"))
	assert.True(t, os.IsNotExist(err), "dry-run must not actually exec the sign command")
}
