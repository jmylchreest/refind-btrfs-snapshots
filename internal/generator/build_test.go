package generator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/config"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/fstab"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/refind"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBootableEntries_FiltersByDeviceAndSubvol(t *testing.T) {
	rootFS := &btrfs.Filesystem{
		UUID:      "main-uuid",
		Subvolume: &btrfs.Subvolume{ID: 256, Path: "@"},
	}

	entries := []*refind.MenuEntry{
		{Title: "matching", BootOptions: &refind.BootOptions{Root: "UUID=main-uuid", Subvol: "@"}},
		{Title: "different uuid", BootOptions: &refind.BootOptions{Root: "UUID=other-uuid", Subvol: "@"}},
		{Title: "different subvol", BootOptions: &refind.BootOptions{Root: "UUID=main-uuid", Subvol: "@home"}},
		{Title: "no boot options"},
		{Title: "no root", BootOptions: &refind.BootOptions{Subvol: "@"}},
		{Title: "second match", BootOptions: &refind.BootOptions{Root: "UUID=main-uuid", Subvol: "@"}},
	}

	got := bootableEntries(entries, rootFS)
	require.Len(t, got, 2)
	assert.Equal(t, "matching", got[0].Title)
	assert.Equal(t, "second match", got[1].Title)
}

func TestSplitSourcesByConfigType(t *testing.T) {
	entries := []*refind.MenuEntry{
		{Title: "a", SourceFile: "/boot/efi/EFI/refind/refind.conf"},
		{Title: "b", SourceFile: "/boot/loader/entries/refind_linux.conf"},
		{Title: "c", SourceFile: ""},
		{Title: "d", SourceFile: "/another/refind_linux.conf"},
	}

	refindLinux, other := splitSourcesByConfigType(entries)

	require.Len(t, refindLinux, 2)
	assert.Equal(t, "b", refindLinux[0].Title)
	assert.Equal(t, "d", refindLinux[1].Title)

	require.Len(t, other, 2)
	assert.Equal(t, "a", other[0].Title)
	assert.Equal(t, "c", other[1].Title)
}

func TestResolveRefindConfigPath(t *testing.T) {
	tmpESP := t.TempDir()

	t.Run("default_path_auto_detect_succeeds", func(t *testing.T) {
		// Stage a real refind.conf at the standard auto-detected location.
		refindDir := filepath.Join(tmpESP, "EFI", "refind")
		require.NoError(t, os.MkdirAll(refindDir, 0755))
		realPath := filepath.Join(refindDir, "refind.conf")
		require.NoError(t, os.WriteFile(realPath, []byte("# refind\n"), 0644))

		p := &Pipeline{
			Cfg:     &config.Config{Refind: config.RefindConfig{ConfigPath: "/EFI/refind/refind.conf"}},
			ESPPath: tmpESP,
		}
		got := p.resolveRefindConfigPath(refind.NewParser(tmpESP))
		assert.Equal(t, realPath, got)
	})

	t.Run("custom_relative_resolves_against_esp", func(t *testing.T) {
		p := &Pipeline{
			Cfg:     &config.Config{Refind: config.RefindConfig{ConfigPath: "EFI/BOOT/refind.conf"}},
			ESPPath: tmpESP,
		}
		got := p.resolveRefindConfigPath(refind.NewParser(tmpESP))
		assert.Equal(t, filepath.Join(tmpESP, "EFI/BOOT/refind.conf"), got)
	})

	t.Run("custom_absolute_passed_through", func(t *testing.T) {
		p := &Pipeline{
			Cfg:     &config.Config{Refind: config.RefindConfig{ConfigPath: "/custom/abs/refind.conf"}},
			ESPPath: tmpESP,
		}
		got := p.resolveRefindConfigPath(refind.NewParser(tmpESP))
		assert.Equal(t, "/custom/abs/refind.conf", got)
	})
}

// TestBuildPatch_EndToEnd exercises the full BuildPatch flow with a real
// temp-dir ESP layout, real fstab updates against a fake snapshot tree, and
// the real Parser/Generator — no btrfs operations required. Verifies the
// patch contains the expected files and the summary records them.
func TestBuildPatch_EndToEnd(t *testing.T) {
	tmpESP := t.TempDir()
	refindDir := filepath.Join(tmpESP, "EFI", "refind")
	require.NoError(t, os.MkdirAll(refindDir, 0755))

	// Stage a refind.conf with a matching menuentry. The non-matching one
	// (different UUID) is filtered out by bootableEntries.
	refindConf := filepath.Join(refindDir, "refind.conf")
	require.NoError(t, os.WriteFile(refindConf, []byte(`# rEFInd
menuentry "Arch Linux" {
    loader /vmlinuz-linux
    initrd /initramfs-linux.img
    options "root=UUID=test-uuid rootflags=subvol=@ rw quiet"
}
menuentry "Other Distro" {
    loader /vmlinuz-other
    options "root=UUID=other-uuid rootflags=subvol=@root rw"
}
`), 0644))

	// Fake snapshot tree with a writable /etc/fstab referencing the root subvol.
	snapshotRoot := t.TempDir()
	snapshotPath := filepath.Join(snapshotRoot, "snapshot-1")
	require.NoError(t, os.MkdirAll(filepath.Join(snapshotPath, "etc"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(snapshotPath, "etc/fstab"),
		[]byte("UUID=test-uuid / btrfs rw,subvol=@ 0 0\n"), 0644))

	snapshot := &btrfs.Snapshot{
		Subvolume:      &btrfs.Subvolume{ID: 257, Path: "/.snapshots/1/snapshot"},
		FilesystemPath: snapshotPath,
	}
	rootFS := &btrfs.Filesystem{
		UUID:      "test-uuid",
		Subvolume: &btrfs.Subvolume{ID: 256, Path: "@"},
	}

	cfg := &config.Config{
		Refind:   config.RefindConfig{ConfigPath: "/EFI/refind/refind.conf"},
		Snapshot: config.SnapshotConfig{WritableMethod: "toggle"},
		Advanced: config.AdvancedConfig{Naming: config.NamingConfig{MenuFormat: "2006-01-02T15:04:05Z"}},
		GenerateInclude: true, // force include-file generation so the test exercises that path
	}

	pipeline := &Pipeline{
		Cfg:     cfg,
		Fstab:   fstab.NewManager(),
		Runner:  runner.New(true), // dry-run
		ESPPath: tmpESP,
	}
	plan := &Plan{
		RootFS:             rootFS,
		ProcessedSnapshots: []*btrfs.Snapshot{snapshot},
	}

	patch, summary, err := pipeline.BuildPatch(plan)
	require.NoError(t, err)

	assert.GreaterOrEqual(t, len(patch.Files), 1, "patch should contain at least the fstab update")

	// Operation summary records the snapshot we processed.
	assert.Len(t, summary.IncludedSnapshots, 1)

	// Find the fstab update — its path is the snapshot's fstab path.
	var foundFstab, foundInclude bool
	for _, f := range patch.Files {
		switch {
		case f.Path == filepath.Join(snapshotPath, "etc/fstab"):
			foundFstab = true
			assert.Contains(t, f.Modified, ".snapshots/1/snapshot", "fstab should be rewritten to point at the snapshot subvol")
		case filepath.Base(f.Path) == "refind-btrfs-snapshots.conf":
			foundInclude = true
		}
	}
	assert.True(t, foundFstab, "expected fstab diff in patch")
	assert.True(t, foundInclude, "expected managed include diff in patch (because GenerateInclude=true)")
}

func TestBuildPatch_NoSourceEntriesIsAnError(t *testing.T) {
	tmpESP := t.TempDir()
	refindDir := filepath.Join(tmpESP, "EFI", "refind")
	require.NoError(t, os.MkdirAll(refindDir, 0755))
	// refind.conf with no entries matching the root filesystem.
	require.NoError(t, os.WriteFile(filepath.Join(refindDir, "refind.conf"), []byte(`# nothing matches
menuentry "Other" {
    loader /vmlinuz-other
    options "root=UUID=other-uuid rootflags=subvol=@ rw"
}
`), 0644))

	rootFS := &btrfs.Filesystem{
		UUID:      "no-match-uuid",
		Subvolume: &btrfs.Subvolume{Path: "@"},
	}

	pipeline := &Pipeline{
		Cfg:     &config.Config{Refind: config.RefindConfig{ConfigPath: "/EFI/refind/refind.conf"}},
		Fstab:   fstab.NewManager(),
		Runner:  runner.New(true),
		ESPPath: tmpESP,
	}
	_, _, err := pipeline.BuildPatch(&Plan{RootFS: rootFS})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no suitable boot entries")
}
