package generator

import (
	"errors"
	"testing"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/config"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Embed the real manager to keep the fake limited to the copy-mode boundary.
type copyCleanupManager struct {
	*btrfs.Manager
	copies       map[uint64]*btrfs.Snapshot
	failID       uint64
	cleanupCalls int
	kept         []*btrfs.Snapshot
}

func (m *copyCleanupManager) CreateWritableSnapshot(src *btrfs.Snapshot, _ string, _ runner.Runner) (*btrfs.Snapshot, error) {
	if src.ID == m.failID {
		return nil, errors.New("copy failed")
	}
	return m.copies[src.ID], nil
}
func (m *copyCleanupManager) CleanupUnselectedSnapshots(_ string, selected []*btrfs.Snapshot, _ runner.Runner) error {
	m.cleanupCalls++
	m.kept = selected
	return nil
}

func TestCopyCleanupUsesProcessedSelection(t *testing.T) {
	for _, count := range []int{0, 2} {
		t.Run(map[int]string{0: "all snapshots", 2: "limited selection"}[count], func(t *testing.T) {
			first, second := mkSnapshot(1, "@/first"), mkSnapshot(2, "@/second")
			first.IsReadOnly, second.IsReadOnly = true, true
			firstCopy, secondCopy := mkSnapshot(101, "@/copy-first"), mkSnapshot(102, "@/copy-second")
			m := &copyCleanupManager{copies: map[uint64]*btrfs.Snapshot{1: firstCopy, 2: secondCopy}}
			cfg := &config.Config{}
			cfg.Snapshot.WritableMethod = "copy"
			cfg.Snapshot.SelectionCount = count
			cfg.Behavior.CleanupOldSnapshots = true
			p := &Pipeline{Cfg: cfg, Btrfs: m, Runner: &runner.DryRunner{}}
			selected := selectSnapshots([]*btrfs.Snapshot{first, second}, count)
			processed, err := p.processWritability(selected, selected)
			require.NoError(t, err)
			require.Equal(t, 1, m.cleanupCalls)
			require.Equal(t, []*btrfs.Snapshot{firstCopy, secondCopy}, processed)
			assert.Equal(t, processed, m.kept, "cleanup must protect the actual returned copies, even when count is zero")
		})
	}
}

func TestCopyCleanupSkippedAfterPartialFailure(t *testing.T) {
	first, second := mkSnapshot(1, "@/first"), mkSnapshot(2, "@/second")
	first.IsReadOnly, second.IsReadOnly = true, true
	copy := mkSnapshot(101, "@/copy-first")
	m := &copyCleanupManager{copies: map[uint64]*btrfs.Snapshot{1: copy}, failID: 2}
	cfg := &config.Config{}
	cfg.Snapshot.WritableMethod = "copy"
	cfg.Behavior.CleanupOldSnapshots = true
	p := &Pipeline{Cfg: cfg, Btrfs: m, Runner: &runner.DryRunner{}}
	selected := []*btrfs.Snapshot{first, second}
	processed, err := p.processWritability(selected, selected)
	require.NoError(t, err)
	assert.Equal(t, []*btrfs.Snapshot{copy}, processed)
	assert.Zero(t, m.cleanupCalls, "a failed copy must not cause deletion of existing copies")
}

func TestCopyCleanupCanBeDisabled(t *testing.T) {
	src := mkSnapshot(1, "@/source")
	src.IsReadOnly = true
	m := &copyCleanupManager{copies: map[uint64]*btrfs.Snapshot{1: mkSnapshot(101, "@/copy")}}
	cfg := &config.Config{}
	cfg.Snapshot.WritableMethod = "copy"
	p := &Pipeline{Cfg: cfg, Btrfs: m, Runner: &runner.DryRunner{}}
	_, err := p.processWritability([]*btrfs.Snapshot{src}, []*btrfs.Snapshot{src})
	require.NoError(t, err)
	assert.Zero(t, m.cleanupCalls)
}
