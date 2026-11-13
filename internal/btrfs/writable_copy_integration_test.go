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
	"golang.org/x/sys/unix"
)

// These opt-in tests use unmodified btrfs-progs and real subvolume metadata.
// Run on Btrfs with permission to inspect/create subvolumes:
// go test -tags btrfs_integration ./internal/btrfs -run TestWritableCopyIntegration -v
func writableCopyIntegrationFixture(t *testing.T) (*Manager, *Snapshot, string) {
	t.Helper()
	if _, err := exec.LookPath("btrfs"); err != nil {
		t.Skip("requires btrfs-progs")
	}
	var stat unix.Statfs_t
	require.NoError(t, unix.Statfs(".", &stat))
	if stat.Type != unix.BTRFS_SUPER_MAGIC {
		t.Skip("requires a checkout on Btrfs")
	}
	dir, err := os.MkdirTemp(".", ".btrfs-copy-test-")
	if os.IsPermission(err) {
		t.Skipf("requires a writable checkout: %v", err)
	}
	require.NoError(t, err)
	dir, err = filepath.Abs(dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(dir)) })
	src := filepath.Join(dir, "snapshot")
	requireBtrfsIntegrationPrerequisite(t, "subvolume", "create", src)
	// Check actual kernel permissions before exercising production code. Only
	// environment permission errors skip; unexpected command failures fail.
	requireBtrfsIntegrationPrerequisite(t, "subvolume", "show", src)
	require.NoError(t, os.MkdirAll(filepath.Join(src, "etc"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "etc", "fstab"), []byte("UUID=test / btrfs subvol=@ 0 0\n"), 0644))
	requireBtrfsIntegrationPrerequisite(t, "property", "set", src, "ro", "true")
	t.Cleanup(func() {
		out, err := exec.Command("btrfs", "property", "set", src, "ro", "false").CombinedOutput()
		require.NoError(t, err, "%s", out)
	})
	m := NewManager(nil, 0, "", false)
	sv, err := m.getSubvolumeInfo(src)
	require.NoError(t, err)
	return m, &Snapshot{Subvolume: sv, FilesystemPath: src, SnapshotTime: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}, filepath.Join(dir, "copies")
}

func requireBtrfsIntegrationPrerequisite(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("btrfs", args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	output, err := cmd.CombinedOutput()
	if err != nil && (strings.Contains(string(output), "Operation not permitted") || strings.Contains(string(output), "Permission denied")) {
		t.Skipf("requires Btrfs permissions for %v: %s", args, output)
	}
	require.NoError(t, err, "%s", output)
}

func TestWritableCopyIntegrationMountedSource(t *testing.T) {
	m, src, dest := writableCopyIntegrationFixture(t)
	_, err := m.CreateWritableSnapshot(src, dest, &runner.RealRunner{})
	require.NoError(t, err, "creation must use FilesystemPath rather than the top-level-relative Path")
}

func TestWritableCopyIntegrationCopyFilesystemPath(t *testing.T) {
	m, src, dest := writableCopyIntegrationFixture(t)
	copy, err := m.CreateWritableSnapshot(src, dest, &runner.RealRunner{})
	require.NoError(t, err)
	assert.NotEmpty(t, copy.FilesystemPath)
	_, err = os.Stat(GetSnapshotFstabPath(copy))
	assert.NoError(t, err, "fstab must be readable from the copy")
}

func TestWritableCopyIntegrationRerun(t *testing.T) {
	m, src, dest := writableCopyIntegrationFixture(t)
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
	assert.Equal(t, filepath.Join(filepath.Dir(original.Path), "copies", filepath.Base(copy.PlannedFilesystemPath)), copy.Path, "planned boot path must retain the top-level subvolume prefix")
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
	_, err := m.CreateWritableSnapshot(src, dest, &runner.RealRunner{})
	require.NoError(t, err)
	r := &writableCopyIntegrationRecorder{}
	require.NoError(t, m.CleanupOldSnapshots(dest, 0, r))
	assert.Empty(t, r.commands, "zero means include all; no deletion should be scheduled")
}

func TestWritableCopyIntegrationRejectUnrelatedExistingSubvolume(t *testing.T) {
	m, src, dest := writableCopyIntegrationFixture(t)
	require.NoError(t, os.MkdirAll(dest, 0755))
	target := filepath.Join(dest, "rwsnap_2026-01-02_03-04-05_ID"+strconv.FormatUint(src.ID, 10))
	out, err := exec.Command("btrfs", "subvolume", "create", target).CombinedOutput()
	require.NoError(t, err, "%s", out)
	_, err = m.CreateWritableSnapshot(src, dest, &runner.RealRunner{})
	assert.Error(t, err, "an unrelated subvolume with the expected name must not be accepted as a copy")
}

func TestWritableCopyIntegrationRejectReadOnlyExistingCopy(t *testing.T) {
	m, src, dest := writableCopyIntegrationFixture(t)
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
	_, err = m.CreateWritableSnapshot(src, dest, &runner.RealRunner{})
	require.ErrorContains(t, err, "not a writable snapshot")
}
