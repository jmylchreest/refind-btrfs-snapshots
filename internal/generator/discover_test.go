package generator

import (
	"testing"
	"time"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/stretchr/testify/assert"
)

func mkSnapshot(id uint64, path string) *btrfs.Snapshot {
	return &btrfs.Snapshot{
		Subvolume:    &btrfs.Subvolume{ID: id, Path: path},
		SnapshotTime: time.Now().Add(-time.Duration(id) * time.Hour),
	}
}

func TestSelectSnapshots(t *testing.T) {
	snaps := []*btrfs.Snapshot{
		mkSnapshot(1, "/.snapshots/1/snapshot"),
		mkSnapshot(2, "/.snapshots/2/snapshot"),
		mkSnapshot(3, "/.snapshots/3/snapshot"),
		mkSnapshot(4, "/.snapshots/4/snapshot"),
		mkSnapshot(5, "/.snapshots/5/snapshot"),
	}

	tests := []struct {
		name     string
		input    []*btrfs.Snapshot
		count    int
		wantLen  int
		wantFunc func(t *testing.T, got []*btrfs.Snapshot)
	}{
		{name: "zero_returns_all", input: snaps, count: 0, wantLen: 5},
		{name: "negative_returns_all", input: snaps, count: -1, wantLen: 5},
		{name: "exact_match", input: snaps, count: 5, wantLen: 5},
		{name: "smaller_than_total", input: snaps, count: 3, wantLen: 3,
			wantFunc: func(t *testing.T, got []*btrfs.Snapshot) {
				assert.Equal(t, uint64(1), got[0].ID, "selection takes from the start (assumed newest-first sorted)")
				assert.Equal(t, uint64(3), got[2].ID)
			},
		},
		{name: "larger_than_total_caps", input: snaps, count: 999, wantLen: 5},
		{name: "empty_input", input: nil, count: 3, wantLen: 0},
		{name: "empty_with_zero_count", input: nil, count: 0, wantLen: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectSnapshots(tt.input, tt.count)
			assert.Len(t, got, tt.wantLen)
			if tt.wantFunc != nil {
				tt.wantFunc(t, got)
			}
		})
	}
}

// makePlan builds a BootPlan whose ShouldSkip returns the requested value by
// constructing the underlying staleness state. ShouldSkip returns true iff
// the plan is ESP-mode + stale + action=delete.
func makePlan(snapshot *btrfs.Snapshot, skip bool) *kernel.BootPlan {
	p := &kernel.BootPlan{
		Snapshot: snapshot,
		Mode:     kernel.BootModeESP,
	}
	if skip {
		p.Staleness = &kernel.StalenessResult{
			IsStale: true,
			Action:  kernel.ActionDelete,
		}
	}
	return p
}

func TestFilterDeletedStale(t *testing.T) {
	a := mkSnapshot(1, "/snap/a")
	b := mkSnapshot(2, "/snap/b")
	c := mkSnapshot(3, "/snap/c")

	t.Run("none_stale_keeps_all", func(t *testing.T) {
		plans := []*kernel.BootPlan{
			makePlan(a, false),
			makePlan(b, false),
			makePlan(c, false),
		}
		kept, removed := filterDeletedStale([]*btrfs.Snapshot{a, b, c}, plans)
		assert.Len(t, kept, 3)
		assert.Empty(t, removed)
	})

	t.Run("all_stale_removes_all", func(t *testing.T) {
		plans := []*kernel.BootPlan{
			makePlan(a, true),
			makePlan(b, true),
			makePlan(c, true),
		}
		kept, removed := filterDeletedStale([]*btrfs.Snapshot{a, b, c}, plans)
		assert.Empty(t, kept)
		assert.Equal(t, []string{"/snap/a", "/snap/b", "/snap/c"}, removed)
	})

	t.Run("partial_removes_only_stale", func(t *testing.T) {
		plans := []*kernel.BootPlan{
			makePlan(a, true),
			makePlan(b, false),
			makePlan(c, true),
		}
		kept, removed := filterDeletedStale([]*btrfs.Snapshot{a, b, c}, plans)
		assert.Len(t, kept, 1)
		assert.Equal(t, "/snap/b", kept[0].Path)
		assert.ElementsMatch(t, []string{"/snap/a", "/snap/c"}, removed)
	})

	t.Run("multiple_plans_per_snapshot_kept_if_any_survives", func(t *testing.T) {
		// Snapshot a has two boot plans: one stale, one not. The non-stale plan
		// rescues the snapshot — it can still boot from the non-stale kernel.
		plans := []*kernel.BootPlan{
			makePlan(a, true),
			makePlan(a, false),
		}
		kept, removed := filterDeletedStale([]*btrfs.Snapshot{a}, plans)
		assert.Len(t, kept, 1, "snapshot has at least one viable plan → kept")
		assert.Empty(t, removed)
	})

	t.Run("snapshot_with_no_plans_kept", func(t *testing.T) {
		// allSkip starts as len(snapPlans) > 0; a snapshot with no plans means
		// allSkip starts false and the snapshot is kept. (Documents current behavior.)
		kept, removed := filterDeletedStale([]*btrfs.Snapshot{a}, nil)
		assert.Len(t, kept, 1)
		assert.Empty(t, removed)
	})
}
