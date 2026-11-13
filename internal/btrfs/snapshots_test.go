package btrfs

import (
	"encoding/json"
	"fmt"
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
func copyFixture(t *testing.T) (*Manager, *Snapshot, string, string, *copyTestRunner) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	dest := filepath.Join(dir, "copies")
	require.NoError(t, os.MkdirAll(filepath.Join(source, "etc"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(source, "etc", "fstab"), []byte("fstab"), 0644))
	src := &Snapshot{
		Subvolume:      &Subvolume{ID: 100, UUID: "source-uuid", Path: "@/.snapshots/1/snapshot", IsReadOnly: true},
		FilesystemPath: source, SnapshotTime: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Description: "before update", SnapperNum: 1, SnapperType: "pre",
	}
	m := NewManager(nil, 0, "", false)
	m.readSubvolumeInfo = func(path string) (*Subvolume, error) {
		data, err := os.ReadFile(filepath.Join(path, ".subvolume"))
		if err != nil {
			return nil, err
		}
		var subvol Subvolume
		if err := json.Unmarshal(data, &subvol); err != nil {
			return nil, err
		}
		return &subvol, nil
	}
	return m, src, dest, filepath.Join(dest, "rwsnap_2026-01-02_03-04-05_ID100"), &copyTestRunner{source: source}
}

func TestWritableCopyCreationAndReuse(t *testing.T) {
	m, src, dest, target, r := copyFixture(t)
	before := *src.Subvolume
	for i := 0; i < 2; i++ {
		got, err := m.CreateWritableSnapshot(src, dest, r)
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
			m, src, dest, target, r := copyFixture(t)
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
					require.NoError(t, writeCopyTestMetadata(target, &Subvolume{ParentUUID: "other"}))
				}
				if kind == "readonly" {
					require.NoError(t, writeCopyTestMetadata(target, &Subvolume{ParentUUID: "source-uuid", IsReadOnly: true}))
				}
			}
			_, err := m.CreateWritableSnapshot(src, dest, r)
			require.Error(t, err)
			_, err = os.Stat(filepath.Join(target, "source"))
			assert.True(t, os.IsNotExist(err) || kind == "file")
		})
	}
}

func TestWritableCopyRequiresMountedPathAndUUID(t *testing.T) {
	m, src, dest, _, _ := copyFixture(t)
	src.FilesystemPath = ""
	_, err := m.CreateWritableSnapshot(src, dest, &runner.DryRunner{})
	require.Error(t, err)
	src.FilesystemPath = "/source"
	src.UUID = ""
	_, err = m.CreateWritableSnapshot(src, dest, &runner.DryRunner{})
	require.Error(t, err)
}

func TestCleanupEmptySelectionDoesNotInspectDestination(t *testing.T) {
	m := NewManager(nil, 0, "", false)
	// A file would fail ReadDir; keeping all should return before touching it.
	path := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(path, nil, 0644))
	require.NoError(t, m.CleanupUnselectedSnapshots(path, nil, &runner.RealRunner{}))
}

// copyTestRunner implements only the two mutations exercised by these tests.
// Files and rename publication remain real; no subprocess or PATH override is used.
type copyTestRunner struct {
	runner.RealRunner
	source         string
	beforeSnapshot func() error
	snapshotCalls  int
}

func writeCopyTestMetadata(path string, subvol *Subvolume) error {
	data, err := json.Marshal(subvol)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(path, ".subvolume"), data, 0644)
}

func (r *copyTestRunner) Command(name string, args []string, _ string) error {
	if name != "btrfs" || len(args) < 3 || args[0] != "subvolume" {
		return fmt.Errorf("unexpected command: %s %v", name, args)
	}
	switch args[1] {
	case "snapshot":
		if len(args) != 4 || args[2] != r.source {
			return fmt.Errorf("unexpected snapshot arguments: %v", args)
		}
		if r.beforeSnapshot != nil {
			if err := r.beforeSnapshot(); err != nil {
				return err
			}
		}
		// Reproduce Btrfs's existing-directory behavior so a nesting regression
		// is detected by the assertions, rather than rejected by the fake itself.
		target := args[3]
		if info, err := os.Stat(target); err == nil && info.IsDir() {
			target = filepath.Join(target, filepath.Base(r.source))
		}
		if err := os.Mkdir(target, 0755); err != nil {
			return err
		}
		if err := os.CopyFS(target, os.DirFS(r.source)); err != nil {
			return err
		}
		r.snapshotCalls++
		return writeCopyTestMetadata(target, &Subvolume{ID: 200, UUID: "copy-uuid", ParentUUID: "source-uuid", Path: "@/.copies/test"})
	case "delete":
		if len(args) != 3 {
			return fmt.Errorf("unexpected delete arguments: %v", args)
		}
		return os.RemoveAll(args[2])
	default:
		return fmt.Errorf("unexpected subvolume operation: %v", args)
	}
}

func TestWritableCopyDoesNotOverwriteConcurrentDestination(t *testing.T) {
	m, src, dest, target, r := copyFixture(t)
	// Another process creates the final destination after the initial check.
	r.beforeSnapshot = func() error { return os.Mkdir(target, 0755) }
	_, err := m.CreateWritableSnapshot(src, dest, r)
	require.Error(t, err)
	entries, err := os.ReadDir(target)
	require.NoError(t, err)
	assert.Empty(t, entries, "competing destination must be untouched")
	entries, err = os.ReadDir(dest)
	require.NoError(t, err)
	require.Len(t, entries, 1, "staging snapshot and directory must be cleaned up")
	assert.Equal(t, filepath.Base(target), entries[0].Name())
}

func TestCleanupRemovesUnselectedCopy(t *testing.T) {
	m, src, dest, older, r := copyFixture(t)
	_, err := m.CreateWritableSnapshot(src, dest, r)
	require.NoError(t, err)
	src.SnapshotTime = src.SnapshotTime.Add(time.Hour)
	newer, err := m.CreateWritableSnapshot(src, dest, r)
	require.NoError(t, err)
	require.NoError(t, m.CleanupUnselectedSnapshots(dest, []*Snapshot{newer}, r))
	_, err = os.Stat(older)
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(GetSnapshotFstabPath(newer))
	require.NoError(t, err)
}
