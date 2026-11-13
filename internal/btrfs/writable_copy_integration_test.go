//go:build btrfs_integration

package btrfs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These opt-in regression tests exercise real Btrfs snapshot operations.
// They create tiny real Btrfs subvolumes in the checkout. Only `subvolume show`
// is shimmed: this environment denies its B-tree search ioctl. IDs come from
// real `inspect-internal rootid`; reported top-level paths are synthetic.
// Run only on Btrfs: go test -tags btrfs_integration ./internal/btrfs -run TestWritableCopyIntegration -v
func writableCopyIntegrationFixture(t *testing.T) (*Manager, *Snapshot, string) {
	t.Helper()
	dir, err := os.MkdirTemp(".", ".btrfs-copy-test-")
	require.NoError(t, err)
	dir, err = filepath.Abs(dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(dir)) })
	// findmnt provides an independent oracle for the mounted subvolume prefix.
	root, err := exec.Command("findmnt", "-n", "-o", "FSROOT", "-T", dir).Output()
	require.NoError(t, err)
	mount, err := exec.Command("findmnt", "-n", "-o", "TARGET", "-T", dir).Output()
	require.NoError(t, err)
	t.Setenv("BTRFS_COPY_TEST_ROOT", strings.TrimSpace(string(root)))
	t.Setenv("BTRFS_COPY_TEST_MOUNT", strings.TrimSpace(string(mount)))
	bin := t.TempDir()
	shim := `#!/bin/sh
if [ "$1" = subvolume ] && [ "$2" = show ]; then
 id=$(/usr/bin/btrfs inspect-internal rootid "$3") || exit 1
 path="$BTRFS_COPY_TEST_ROOT${3#"$BTRFS_COPY_TEST_MOUNT"}"
 printf '%s\nSubvolume ID: %s\nUUID: test-%s\nParent UUID: %s\n' "${path#/}" "$id" "$id" "$(cat "$3/.copy-test-parent" 2>/dev/null || printf '-')"
 /usr/bin/btrfs property get "$3" ro | sed 's/ro=true/Flags: readonly/; s/ro=false/Flags: -/'
elif [ "$1" = subvolume ] && [ "$2" = snapshot ]; then
 id=$(/usr/bin/btrfs inspect-internal rootid "$3") || exit 1
 /usr/bin/btrfs "$@" || exit 1
 printf 'test-%s' "$id" > "$4/.copy-test-parent"
else
 exec /usr/bin/btrfs "$@"
fi
`
	require.NoError(t, os.WriteFile(filepath.Join(bin, "btrfs"), []byte(shim), 0755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	src := filepath.Join(dir, "snapshot")
	out, err := exec.Command("btrfs", "subvolume", "create", src).CombinedOutput()
	require.NoError(t, err, "%s", out)
	require.NoError(t, os.MkdirAll(filepath.Join(src, "etc"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "etc", "fstab"), []byte("UUID=test / btrfs subvol=@ 0 0\n"), 0644))
	out, err = exec.Command("btrfs", "property", "set", src, "ro", "true").CombinedOutput()
	require.NoError(t, err, "%s", out)
	t.Cleanup(func() {
		out, err := exec.Command("btrfs", "property", "set", src, "ro", "false").CombinedOutput()
		require.NoError(t, err, "%s", out)
	})
	m := NewManager(nil, 0, "", false)
	sv, err := m.getSubvolumeInfo(src)
	require.NoError(t, err)
	return m, &Snapshot{Subvolume: sv, FilesystemPath: src, SnapshotTime: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}, filepath.Join(dir, "copies")
}

func TestWritableCopyIntegrationMountedSource(t *testing.T) {
	m, src, dest := writableCopyIntegrationFixture(t)
	_, err := m.CreateWritableSnapshot(src, dest, &runner.RealRunner{})
	require.NoError(t, err, "creation must use FilesystemPath rather than the top-level-relative Path")
}

func TestWritableCopyIntegrationCopyFilesystemPath(t *testing.T) {
	m, src, dest := writableCopyIntegrationFixture(t)
	// Isolate subsequent bugs by letting the old implementation find its source.
	src.Path = src.FilesystemPath
	copy, err := m.CreateWritableSnapshot(src, dest, &runner.RealRunner{})
	require.NoError(t, err)
	assert.NotEmpty(t, copy.FilesystemPath)
	_, err = os.Stat(GetSnapshotFstabPath(copy))
	assert.NoError(t, err, "fstab must be readable from the copy")
}

func TestWritableCopyIntegrationRerun(t *testing.T) {
	m, src, dest := writableCopyIntegrationFixture(t)
	src.Path = src.FilesystemPath
	first, err := m.CreateWritableSnapshot(src, dest, &runner.RealRunner{})
	require.NoError(t, err)
	second, err := m.CreateWritableSnapshot(src, dest, &runner.RealRunner{})
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)
	entries, err := os.ReadDir(dest)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	_, err = os.Stat(filepath.Join(dest, entries[0].Name(), "snapshot"))
	assert.True(t, os.IsNotExist(err), "rerun must not create a nested snapshot")
}

func TestWritableCopyIntegrationDryRunDoesNotMutateSource(t *testing.T) {
	m, src, dest := writableCopyIntegrationFixture(t)
	original := *src.Subvolume
	copy, err := m.CreateWritableSnapshot(src, dest, &runner.DryRunner{})
	require.NoError(t, err)
	assert.Equal(t, original, *src.Subvolume, "dry run must preserve source metadata")
	assert.Equal(t, src.FilesystemPath, copy.FilesystemPath)
	assert.NotSame(t, src.Subvolume, copy.Subvolume)
	assert.Zero(t, copy.ID)
	assert.False(t, copy.IsReadOnly)
	assert.Equal(t, dest+"/rwsnap_2026-01-02_03-04-05_ID"+strconv.FormatUint(src.ID, 10), copy.PlannedFilesystemPath)
	assert.Equal(t, strings.TrimPrefix(filepath.Join(os.Getenv("BTRFS_COPY_TEST_ROOT"), strings.TrimPrefix(copy.PlannedFilesystemPath, os.Getenv("BTRFS_COPY_TEST_MOUNT"))), "/"), copy.Path, "planned boot path must retain the top-level subvolume prefix")
	_, err = os.Stat(dest)
	assert.True(t, os.IsNotExist(err))
}

type writableCopyIntegrationRecorder struct {
	runner.DryRunner
	commands [][]string
}

func (r *writableCopyIntegrationRecorder) Command(_ string, args []string, _ string) error {
	r.commands = append(r.commands, append([]string(nil), args...))
	return nil
}

func TestWritableCopyIntegrationCleanupZeroKeepsAll(t *testing.T) {
	m, src, dest := writableCopyIntegrationFixture(t)
	src.Path = src.FilesystemPath
	_, err := m.CreateWritableSnapshot(src, dest, &runner.RealRunner{})
	require.NoError(t, err)
	r := &writableCopyIntegrationRecorder{}
	require.NoError(t, m.CleanupOldSnapshots(dest, 0, r))
	assert.Empty(t, r.commands, "zero means include all; no deletion should be scheduled")
}

func TestWritableCopyIntegrationRejectUnrelatedExistingSubvolume(t *testing.T) {
	m, src, dest := writableCopyIntegrationFixture(t)
	src.Path = src.FilesystemPath
	require.NoError(t, os.MkdirAll(dest, 0755))
	target := filepath.Join(dest, "rwsnap_2026-01-02_03-04-05_ID"+strconv.FormatUint(src.ID, 10))
	out, err := exec.Command("btrfs", "subvolume", "create", target).CombinedOutput()
	require.NoError(t, err, "%s", out)
	_, err = m.CreateWritableSnapshot(src, dest, &runner.RealRunner{})
	assert.Error(t, err, "an unrelated subvolume with the expected name must not be accepted as a copy")
}

func TestWritableCopyIntegrationRejectReadOnlyExistingCopy(t *testing.T) {
	m, src, dest := writableCopyIntegrationFixture(t)
	src.Path = src.FilesystemPath
	_, err := m.CreateWritableSnapshot(src, dest, &runner.RealRunner{})
	require.NoError(t, err)
	entries, err := os.ReadDir(dest)
	require.NoError(t, err)
	target := filepath.Join(dest, entries[0].Name())
	out, err := exec.Command("btrfs", "property", "set", target, "ro", "true").CombinedOutput()
	require.NoError(t, err, "%s", out)
	t.Cleanup(func() {
		out, err := exec.Command("btrfs", "property", "set", target, "ro", "false").CombinedOutput()
		require.NoError(t, err, "%s", out)
	})
	copy, err := m.CreateWritableSnapshot(src, dest, &runner.RealRunner{})
	if err == nil {
		assert.False(t, copy.IsReadOnly, "successful copy must be writable")
	}
}
