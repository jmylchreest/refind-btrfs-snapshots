package btrfs

import (
	"fmt"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/runner"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Reproduces the reporter's two-run failure: the orphan sorts between the two
// selected copies, so count-based cleanup repeatedly deletes the older selection.
func TestCopyRetentionDoesNotDisplaceSelectedCopyWithOrphan(t *testing.T) {
	m, source, dest, _, r := copyFixture(t)
	latest, oldest := *source, *source
	latestSubvol, oldestSubvol := *source.Subvolume, *source.Subvolume
	latest.Subvolume, oldest.Subvolume = &latestSubvol, &oldestSubvol
	latest.ID = 1555
	latest.SnapshotTime = time.Date(2026, 11, 13, 16, 0, 4, 0, time.UTC)
	oldest.ID = 1516
	oldest.SnapshotTime = time.Date(2026, 11, 10, 10, 26, 32, 0, time.UTC)
	orphan := filepath.Join(dest, "rwsnap_2026-11-10_14-00-01_ID1524")
	require.NoError(t, os.MkdirAll(orphan, 0755))
	require.NoError(t, writeCopyTestMetadata(orphan, &Subvolume{
		ID: 1525, UUID: "orphan-copy", ParentUUID: "deleted-source", Path: "@/.copies/orphan",
	}))
	for run := 1; run <= 2; run++ {
		var processed []*Snapshot
		for _, selected := range []*Snapshot{&latest, &oldest} {
			copy, err := m.CreateWritableSnapshot(selected, dest, r)
			require.NoError(t, err)
			processed = append(processed, copy)
		}
		require.NoError(t, m.CleanupUnselectedSnapshots(dest, processed, r))
		for _, copy := range processed {
			_, err := os.Stat(GetSnapshotFstabPath(copy))
			assert.NoError(t, err, "run %d: selected copy must survive cleanup: %s", run, copy.FilesystemPath)
		}
		_, err := os.Stat(orphan)
		assert.True(t, os.IsNotExist(err), "run %d: orphan must not displace selected copies", run)
		assert.Equal(t, 2, r.snapshotCalls, "run %d: selected copies should be reused after their initial creation", run)
	}
}

func TestCopyRetentionProtectsUnmanagedEntries(t *testing.T) {
	m, src, dest, _, r := copyFixture(t)
	copy, err := m.CreateWritableSnapshot(src, dest, r)
	require.NoError(t, err)
	for _, name := range []string{"other-snapshot", "rwsnap_plain-subvolume", "rwsnap_directory"} {
		path := filepath.Join(dest, name)
		require.NoError(t, os.Mkdir(path, 0755))
		if name == "other-snapshot" {
			require.NoError(t, writeCopyTestMetadata(path, &Subvolume{ParentUUID: "source-uuid"}))
		}
		if name == "rwsnap_plain-subvolume" {
			require.NoError(t, writeCopyTestMetadata(path, &Subvolume{ParentUUID: "-"}))
		}
	}
	link := filepath.Join(dest, "rwsnap_symlink")
	require.NoError(t, os.Symlink(copy.FilesystemPath, link))
	require.NoError(t, m.CleanupUnselectedSnapshots(dest, []*Snapshot{copy}, r))
	for _, name := range []string{"other-snapshot", "rwsnap_plain-subvolume", "rwsnap_directory", "rwsnap_symlink"} {
		_, err := os.Lstat(filepath.Join(dest, name))
		require.NoError(t, err, "%s must not be deleted", name)
	}
	// Invalid selection data must fail before deleting anything.
	require.Error(t, m.CleanupUnselectedSnapshots(dest, []*Snapshot{{}}, r))
	_, err = os.Stat(copy.FilesystemPath)
	require.NoError(t, err)
}

type retentionRecordingRunner struct {
	runner.DryRunner
	deleted []string
}

func (r *retentionRecordingRunner) Command(name string, args []string, _ string) error {
	if name != "btrfs" || len(args) != 3 || args[0] != "subvolume" || args[1] != "delete" {
		return fmt.Errorf("unexpected cleanup command: %s %v", name, args)
	}
	r.deleted = append(r.deleted, args[2])
	return nil
}

func TestCopyRetentionDryRunProtectsPlannedDestination(t *testing.T) {
	m, src, dest, _, r := copyFixture(t)
	copy, err := m.CreateWritableSnapshot(src, dest, r)
	require.NoError(t, err)
	planned := *src
	planned.PlannedFilesystemPath = copy.FilesystemPath
	orphan := filepath.Join(dest, "rwsnap_orphan")
	require.NoError(t, os.Mkdir(orphan, 0755))
	require.NoError(t, writeCopyTestMetadata(orphan, &Subvolume{ParentUUID: "deleted-source"}))
	recorder := &retentionRecordingRunner{}
	// Mix an absolute planned path with a relative destination directory.
	t.Chdir(filepath.Dir(dest))
	require.NoError(t, m.CleanupUnselectedSnapshots("copies", []*Snapshot{&planned}, recorder))
	assert.Equal(t, []string{orphan}, recorder.deleted)
	_, err = os.Stat(orphan)
	require.NoError(t, err, "dry run must not remove the orphan")
	_, err = os.Stat(copy.FilesystemPath)
	require.NoError(t, err)
}
