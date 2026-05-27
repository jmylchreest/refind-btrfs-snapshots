package snapshotfs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/fstab"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeSnapshotWithFstab creates a snapshot rooted at dir/<id> with the
// provided fstab content under etc/fstab, mirroring the on-disk layout
// btrfs.GetSnapshotFstabPath expects.
func makeSnapshotWithFstab(t *testing.T, parent string, id uint64, subvolPath, fstabContent string) *btrfs.Snapshot {
	t.Helper()
	snapDir := filepath.Join(parent, "snap-"+subvolPath)
	require.NoError(t, os.MkdirAll(filepath.Join(snapDir, "etc"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "etc", "fstab"), []byte(fstabContent), 0o644))
	return &btrfs.Snapshot{
		Subvolume:      &btrfs.Subvolume{ID: id, Path: subvolPath},
		SnapshotTime:   time.Date(2026, 5, 27, 16, 0, 0, 0, time.UTC),
		FilesystemPath: snapDir,
	}
}

func TestUpdateFstabs_ProducesDiffForUnalignedRoot(t *testing.T) {
	dir := t.TempDir()
	// The snapshot's fstab still references the live @ subvol; the helper
	// should rewrite it to point at the snapshot's own subvolume.
	snap := makeSnapshotWithFstab(t, dir, 256, "@/.snapshots/1/snapshot",
		"UUID=test-uuid / btrfs subvol=@,subvolid=5 0 0\n")

	rootFS := &btrfs.Filesystem{
		Device:    "/dev/test",
		UUID:      "test-uuid",
		Subvolume: &btrfs.Subvolume{ID: 5, Path: "@"},
	}

	updates := UpdateFstabs([]*btrfs.Snapshot{snap}, rootFS, fstab.NewManager())
	require.Len(t, updates, 1)
	assert.Same(t, snap, updates[0].Snapshot)
	require.NotNil(t, updates[0].Diff)
	assert.Contains(t, updates[0].Diff.Modified, "subvol=/@/.snapshots/1/snapshot")
	assert.Contains(t, updates[0].Diff.Modified, "subvolid=256")
}

func TestUpdateFstabs_NoDiffWhenAlreadyAligned(t *testing.T) {
	dir := t.TempDir()
	// fstab already points at the snapshot's own subvol — helper must produce
	// nothing. This is the idempotency case: a second run after a successful
	// apply must not re-emit changes.
	snap := makeSnapshotWithFstab(t, dir, 256, "@/.snapshots/1/snapshot",
		"UUID=test-uuid / btrfs subvol=/@/.snapshots/1/snapshot,subvolid=256 0 0\n")

	rootFS := &btrfs.Filesystem{
		Device:    "/dev/test",
		UUID:      "test-uuid",
		Subvolume: &btrfs.Subvolume{ID: 5, Path: "@"},
	}

	updates := UpdateFstabs([]*btrfs.Snapshot{snap}, rootFS, fstab.NewManager())
	assert.Empty(t, updates, "aligned fstab must produce no update")
}

func TestUpdateFstabs_Idempotent(t *testing.T) {
	dir := t.TempDir()
	snap := makeSnapshotWithFstab(t, dir, 256, "@/.snapshots/1/snapshot",
		"UUID=test-uuid / btrfs subvol=@,subvolid=5 0 0\n")

	rootFS := &btrfs.Filesystem{
		Device:    "/dev/test",
		UUID:      "test-uuid",
		Subvolume: &btrfs.Subvolume{ID: 5, Path: "@"},
	}

	mgr := fstab.NewManager()
	first := UpdateFstabs([]*btrfs.Snapshot{snap}, rootFS, mgr)
	require.Len(t, first, 1)

	// Apply the change to disk and re-run — the second call must produce
	// nothing because the file is now aligned.
	require.NoError(t, os.WriteFile(filepath.Join(snap.FilesystemPath, "etc", "fstab"),
		[]byte(first[0].Diff.Modified), 0o644))

	second := UpdateFstabs([]*btrfs.Snapshot{snap}, rootFS, mgr)
	assert.Empty(t, second, "second invocation after apply must be a no-op")
}

func TestUpdateFstabs_SkipsSnapshotsWithoutFstab(t *testing.T) {
	dir := t.TempDir()
	snapDir := filepath.Join(dir, "snap-noetc")
	require.NoError(t, os.MkdirAll(snapDir, 0o755))
	snap := &btrfs.Snapshot{
		Subvolume:      &btrfs.Subvolume{ID: 256, Path: "@/.snapshots/1/snapshot"},
		SnapshotTime:   time.Date(2026, 5, 27, 16, 0, 0, 0, time.UTC),
		FilesystemPath: snapDir,
	}
	rootFS := &btrfs.Filesystem{UUID: "test-uuid", Subvolume: &btrfs.Subvolume{ID: 5, Path: "@"}}

	updates := UpdateFstabs([]*btrfs.Snapshot{snap}, rootFS, fstab.NewManager())
	assert.Empty(t, updates, "missing fstab must produce no update and no error")
}

func TestUpdateFstabs_OneBadSnapshotDoesNotBlockOthers(t *testing.T) {
	dir := t.TempDir()
	good := makeSnapshotWithFstab(t, dir, 256, "@/.snapshots/1/snapshot",
		"UUID=test-uuid / btrfs subvol=@,subvolid=5 0 0\n")
	// "Bad" snapshot has no FilesystemPath set — the underlying call will
	// log a warn and continue.
	bad := &btrfs.Snapshot{Subvolume: &btrfs.Subvolume{ID: 999, Path: "@/.snapshots/bad"}}

	rootFS := &btrfs.Filesystem{UUID: "test-uuid", Subvolume: &btrfs.Subvolume{ID: 5, Path: "@"}}
	updates := UpdateFstabs([]*btrfs.Snapshot{bad, good}, rootFS, fstab.NewManager())
	require.Len(t, updates, 1, "good snapshot must still produce its update")
	assert.Same(t, good, updates[0].Snapshot)
}
