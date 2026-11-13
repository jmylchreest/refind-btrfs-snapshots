package btrfs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Stub only the external command boundary; test the production path, metadata,
// validation, and reuse logic on ordinary temporary directories in normal CI.
func copyFixture(t *testing.T) (*Manager, *Snapshot, string, string) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	dest := filepath.Join(dir, "copies")
	require.NoError(t, os.MkdirAll(filepath.Join(source, "etc"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(source, "etc", "fstab"), []byte("fstab"), 0644))
	bin := t.TempDir()
	script := `#!/bin/sh
set -eu
case "$1 $2" in
 'subvolume snapshot')
  [ "$3" = "$COPY_SOURCE" ] || exit 1
  [ ! -e "$4" ] || exit 1
  cp -R "$3" "$4"
  printf '%s\n' 'Flags: -' 'Parent UUID: source-uuid' > "$4/.subvolume"
  ;;
 'subvolume delete')
  rm -r "$3"
  ;;
 'subvolume show')
  [ -f "$3/.subvolume" ] || exit 1
  printf '%s\n' '@/.copies/test' 'Subvolume ID: 200' 'UUID: copy-uuid'
  cat "$3/.subvolume"
  ;;
 *) exit 1;;
esac
`
	require.NoError(t, os.WriteFile(filepath.Join(bin, "btrfs"), []byte(script), 0755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("COPY_SOURCE", source)
	src := &Snapshot{
		Subvolume:      &Subvolume{ID: 100, UUID: "source-uuid", Path: "@/.snapshots/1/snapshot", IsReadOnly: true},
		FilesystemPath: source, SnapshotTime: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Description: "before update", SnapperNum: 1, SnapperType: "pre",
	}
	return NewManager(nil, 0, "", false), src, dest, filepath.Join(dest, "rwsnap_2026-01-02_03-04-05_ID100")
}

func TestWritableCopyCreationAndReuse(t *testing.T) {
	m, src, dest, target := copyFixture(t)
	before := *src.Subvolume
	for i := 0; i < 2; i++ {
		got, err := m.CreateWritableSnapshot(src, dest, &runner.RealRunner{})
		require.NoError(t, err)
		assert.Equal(t, target, got.FilesystemPath)
		assert.Equal(t, src.Path, got.OriginalPath)
		assert.Equal(t, uint64(200), got.ID)
		assert.False(t, got.IsReadOnly)
		assert.Equal(t, src.Description, got.Description)
		assert.Equal(t, src.SnapperNum, got.SnapperNum)
		assert.Equal(t, src.SnapperType, got.SnapperType)
		assert.Equal(t, before, *src.Subvolume)
		content, err := os.ReadFile(GetSnapshotFstabPath(got))
		require.NoError(t, err)
		if i == 0 {
			assert.Equal(t, "fstab", string(content))
			require.NoError(t, os.WriteFile(GetSnapshotFstabPath(got), []byte("changed after boot"), 0644))
		} else {
			assert.Equal(t, "changed after boot", string(content))
		}
	}
}

func TestWritableCopyRejectsInvalidDestination(t *testing.T) {
	for _, kind := range []string{"directory", "file", "symlink", "dangling symlink", "unrelated", "readonly"} {
		t.Run(kind, func(t *testing.T) {
			m, src, dest, target := copyFixture(t)
			require.NoError(t, os.MkdirAll(dest, 0755))
			switch kind {
			case "file":
				require.NoError(t, os.WriteFile(target, nil, 0644))
			case "symlink":
				require.NoError(t, os.Symlink(src.FilesystemPath, target))
			case "dangling symlink":
				require.NoError(t, os.Symlink(filepath.Join(dest, "missing"), target))
			default:
				require.NoError(t, os.Mkdir(target, 0755))
				if kind == "unrelated" {
					require.NoError(t, os.WriteFile(filepath.Join(target, ".subvolume"), []byte("Parent UUID: other\nFlags: -\n"), 0644))
				}
				if kind == "readonly" {
					require.NoError(t, os.WriteFile(filepath.Join(target, ".subvolume"), []byte("Parent UUID: source-uuid\nFlags: readonly\n"), 0644))
				}
			}
			_, err := m.CreateWritableSnapshot(src, dest, &runner.RealRunner{})
			require.Error(t, err)
			_, err = os.Stat(filepath.Join(target, "source"))
			assert.True(t, os.IsNotExist(err) || kind == "file")
		})
	}
}

func TestWritableCopyRequiresMountedPathAndUUID(t *testing.T) {
	m, src, dest, _ := copyFixture(t)
	src.FilesystemPath = ""
	_, err := m.CreateWritableSnapshot(src, dest, &runner.DryRunner{})
	require.Error(t, err)
	src.FilesystemPath = "/source"
	src.UUID = ""
	_, err = m.CreateWritableSnapshot(src, dest, &runner.DryRunner{})
	require.Error(t, err)
}

func TestCleanupZeroDoesNotInspectDestination(t *testing.T) {
	m := NewManager(nil, 0, "", false)
	// A file would fail ReadDir; keeping all should return before touching it.
	path := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(path, nil, 0644))
	require.NoError(t, m.CleanupOldSnapshots(path, 0, &runner.RealRunner{}))
	require.Error(t, m.CleanupOldSnapshots(path, -1, &runner.RealRunner{}))
}

// Simulate another process creating the final destination after the initial
// existence check but before the snapshot command completes.
type racingCopyRunner struct {
	runner.RealRunner
	target string
}

func (r *racingCopyRunner) Command(name string, args []string, desc string) error {
	if len(args) > 1 && args[1] == "snapshot" {
		if err := os.Mkdir(r.target, 0755); err != nil {
			return err
		}
	}
	return r.RealRunner.Command(name, args, desc)
}
func TestWritableCopyDoesNotOverwriteConcurrentDestination(t *testing.T) {
	m, src, dest, target := copyFixture(t)
	_, err := m.CreateWritableSnapshot(src, dest, &racingCopyRunner{target: target})
	require.Error(t, err)
	entries, err := os.ReadDir(target)
	require.NoError(t, err)
	assert.Empty(t, entries, "competing destination must be untouched")
	entries, err = os.ReadDir(dest)
	require.NoError(t, err)
	require.Len(t, entries, 1, "staging snapshot and directory must be cleaned up")
	assert.Equal(t, filepath.Base(target), entries[0].Name())
}

func TestCleanupPositiveCountStillRemovesOlderCopies(t *testing.T) {
	m, src, dest, older := copyFixture(t)
	_, err := m.CreateWritableSnapshot(src, dest, &runner.RealRunner{})
	require.NoError(t, err)
	src.SnapshotTime = src.SnapshotTime.Add(time.Hour)
	newer, err := m.CreateWritableSnapshot(src, dest, &runner.RealRunner{})
	require.NoError(t, err)
	require.NoError(t, m.CleanupOldSnapshots(dest, 1, &runner.RealRunner{}))
	_, err = os.Stat(older)
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(GetSnapshotFstabPath(newer))
	require.NoError(t, err)
}
