package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeSnapshot(path string) *btrfs.Snapshot {
	return &btrfs.Snapshot{
		Subvolume:    &btrfs.Subvolume{Path: path},
		SnapshotTime: time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC),
	}
}

func TestOutputStatusTable_LayoutInColumnHeader(t *testing.T) {
	sets := []*kernel.BootSet{
		{KernelName: "linux", Layout: kernel.LayoutSplit},
		{KernelName: "linux-zen", Layout: kernel.LayoutUKI},
	}
	matrix := []snapshotCompatibility{
		{
			Snapshot: makeSnapshot("@/.snapshots/1/snapshot"),
			BootMode: kernel.BootModeESP,
			Results: []*kernel.StalenessResult{
				{IsStale: false},
				{IsStale: false},
			},
		},
	}

	out := captureStdout(t, func() {
		require.NoError(t, outputStatusTable(sets, matrix, false))
	})

	assert.Contains(t, out, "LINUX (SPLIT)", "column header must include (LAYOUT) for split sets")
	assert.Contains(t, out, "LINUX-ZEN (UKI)", "column header must include (LAYOUT) for UKI sets")
}

func TestOutputStatusJSON_LayoutPopulated(t *testing.T) {
	sets := []*kernel.BootSet{
		{KernelName: "linux", Layout: kernel.LayoutSplit},
		{KernelName: "linux-zen", Layout: kernel.LayoutUKI},
	}
	matrix := []snapshotCompatibility{
		{
			Snapshot: makeSnapshot("@/.snapshots/1/snapshot"),
			BootMode: kernel.BootModeESP,
			Results: []*kernel.StalenessResult{
				{IsStale: false, Method: kernel.MatchBinaryHeader},
				{IsStale: true, Method: kernel.MatchBinaryHeader, Action: kernel.ActionDelete},
			},
		},
		{
			Snapshot: makeSnapshot("@/.snapshots/btrfs/snapshot"),
			BootMode: kernel.BootModeBtrfs,
			Results:  []*kernel.StalenessResult{nil, nil},
		},
	}

	out := captureStdout(t, func() {
		require.NoError(t, outputStatusJSON(sets, matrix, false))
	})

	var parsed struct {
		Compatibility []struct {
			BootSets []compatEntryJSON `json:"boot_sets"`
		} `json:"compatibility"`
	}
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(out)), &parsed))
	require.Len(t, parsed.Compatibility, 2)
	require.Len(t, parsed.Compatibility[0].BootSets, 2)
	require.Len(t, parsed.Compatibility[1].BootSets, 2)

	assert.Equal(t, "split", parsed.Compatibility[0].BootSets[0].Layout, "ESP rows carry layout")
	assert.Equal(t, "uki", parsed.Compatibility[0].BootSets[1].Layout)
	assert.Equal(t, "split", parsed.Compatibility[1].BootSets[0].Layout, "btrfs-mode rows also carry layout")
	assert.Equal(t, "uki", parsed.Compatibility[1].BootSets[1].Layout)
}
