package kernel

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/fstab"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Documented contract (per docs/USAGE.md "UKI Snapshots: ESP-mode Caveat"):
//   - ESP-mode UKI sets honour the configured snapshot strategy:
//       skip    → no plan emitted (snapshot is omitted for this set)
//       warn    → plan emitted, warning logged
//       disable → plan emitted with Disabled=true, generator emits 'disabled'
//   - Btrfs-mode UKI sets are unaffected by strategy and always emit a plan
//     whose SnapshotKernel points at the UKI inside the snapshot.
//   - BootPlan.Layout reflects the boot set's layout.

func testUKIBootSet(kernelName, version string) *BootSet {
	bs := &BootSet{
		KernelName: kernelName,
		Layout:     LayoutUKI,
		UKI: &BootImage{
			Path:       "/EFI/Linux/" + kernelName + ".efi",
			AbsPath:    "/boot/EFI/Linux/" + kernelName + ".efi",
			Filename:   kernelName + ".efi",
			Role:       RoleUKI,
			KernelName: kernelName,
		},
	}
	if version != "" {
		bs.UKI.Inspected = &InspectedMetadata{Format: "uki", Version: version}
	}
	return bs
}

func TestPlanner_ESPMode_UKI_Skip(t *testing.T) {
	tmpDir := t.TempDir()
	snapshot := testSnapshot("@/.snapshots/42/snapshot", tmpDir)
	setupSnapshotFstab(t, tmpDir, "UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=@/.snapshots/42/snapshot 0 1\nUUID=AAAA-BBBB /boot vfat defaults 0 2\n")
	setupSnapshotModules(t, tmpDir, []string{"6.19.0-uki"})

	rootFS := testRootFS()
	bs := testUKIBootSet("linux", "6.19.0-uki")
	planner := NewPlanner(fstab.NewManager(), NewChecker(ActionWarn), []*BootSet{bs}, rootFS, UKIStrategySkip)
	plans := planner.Plan([]*btrfs.Snapshot{snapshot})

	assert.Empty(t, plans, "strategy=skip must omit ESP-mode UKI plans entirely")
}

func TestPlanner_ESPMode_UKI_Disable(t *testing.T) {
	tmpDir := t.TempDir()
	snapshot := testSnapshot("@/.snapshots/42/snapshot", tmpDir)
	setupSnapshotFstab(t, tmpDir, "UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=@/.snapshots/42/snapshot 0 1\nUUID=AAAA-BBBB /boot vfat defaults 0 2\n")
	setupSnapshotModules(t, tmpDir, []string{"6.19.0-uki"})

	rootFS := testRootFS()
	bs := testUKIBootSet("linux", "6.19.0-uki")
	planner := NewPlanner(fstab.NewManager(), NewChecker(ActionWarn), []*BootSet{bs}, rootFS, UKIStrategyDisable)
	plans := planner.Plan([]*btrfs.Snapshot{snapshot})

	require.Len(t, plans, 1)
	assert.Equal(t, BootModeESP, plans[0].Mode)
	assert.Equal(t, LayoutUKI, plans[0].Layout)
	assert.True(t, plans[0].Disabled, "strategy=disable must mark plan disabled")
}

func TestPlanner_ESPMode_UKI_Warn(t *testing.T) {
	tmpDir := t.TempDir()
	snapshot := testSnapshot("@/.snapshots/42/snapshot", tmpDir)
	setupSnapshotFstab(t, tmpDir, "UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=@/.snapshots/42/snapshot 0 1\nUUID=AAAA-BBBB /boot vfat defaults 0 2\n")
	setupSnapshotModules(t, tmpDir, []string{"6.19.0-uki"})

	rootFS := testRootFS()
	bs := testUKIBootSet("linux", "6.19.0-uki")
	planner := NewPlanner(fstab.NewManager(), NewChecker(ActionWarn), []*BootSet{bs}, rootFS, UKIStrategyWarn)
	plans := planner.Plan([]*btrfs.Snapshot{snapshot})

	require.Len(t, plans, 1)
	assert.Equal(t, LayoutUKI, plans[0].Layout)
	assert.False(t, plans[0].Disabled, "strategy=warn must not disable the plan")
}

func TestPlanner_ESPMode_SplitLayoutUnaffectedByUKIStrategy(t *testing.T) {
	// Sanity: changing UKIStrategy must not affect Split-layout boot sets.
	tmpDir := t.TempDir()
	snapshot := testSnapshot("@/.snapshots/42/snapshot", tmpDir)
	setupSnapshotFstab(t, tmpDir, "UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=@/.snapshots/42/snapshot 0 1\nUUID=AAAA-BBBB /boot vfat defaults 0 2\n")
	setupSnapshotModules(t, tmpDir, []string{"6.19.0-2-cachyos"})

	rootFS := testRootFS()
	bs := testBootSet("linux-cachyos", "6.19.0-2-cachyos")
	bs.Layout = LayoutSplit

	planner := NewPlanner(fstab.NewManager(), NewChecker(ActionWarn), []*BootSet{bs}, rootFS, UKIStrategySkip)
	plans := planner.Plan([]*btrfs.Snapshot{snapshot})

	require.Len(t, plans, 1, "split-layout sets must produce a plan regardless of UKI strategy")
	assert.Equal(t, LayoutSplit, plans[0].Layout)
	assert.False(t, plans[0].Disabled)
}

func TestPlanner_BtrfsMode_UKIInsideSnapshot(t *testing.T) {
	tmpDir := t.TempDir()
	snapshot := testSnapshot("@/.snapshots/73/snapshot", tmpDir)
	setupSnapshotFstab(t, tmpDir, "UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=@/.snapshots/73/snapshot 0 1\n")

	// UKI inside snapshot's /boot/EFI/Linux/
	ukiDir := filepath.Join(tmpDir, "boot", "EFI", "Linux")
	require.NoError(t, os.MkdirAll(ukiDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ukiDir, "linux.efi"), []byte("fake-uki"), 0o644))

	rootFS := testRootFS()
	planner := NewPlanner(fstab.NewManager(), nil, nil, rootFS, UKIStrategySkip)
	plans := planner.Plan([]*btrfs.Snapshot{snapshot})

	require.Len(t, plans, 1)
	assert.Equal(t, BootModeBtrfs, plans[0].Mode)
	assert.Equal(t, LayoutUKI, plans[0].Layout)
	assert.Contains(t, plans[0].SnapshotKernel, "linux.efi")
	assert.Contains(t, plans[0].SnapshotKernel, "@/.snapshots/73/snapshot")
	assert.Empty(t, plans[0].SnapshotInitrds, "UKI is self-contained — no initrd paths")
	assert.False(t, plans[0].Disabled, "btrfs-mode UKI plans are never disabled")
}

func TestPlanner_BtrfsMode_UKIAndSplitMixed(t *testing.T) {
	// Same snapshot has both a UKI under /boot/EFI/Linux/ AND a loose vmlinuz under /boot.
	// Each must produce its own plan with the right layout.
	tmpDir := t.TempDir()
	snapshot := testSnapshot("@/.snapshots/99/snapshot", tmpDir)
	setupSnapshotFstab(t, tmpDir, "UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=@/.snapshots/99/snapshot 0 1\n")
	setupSnapshotBoot(t, tmpDir, []string{"vmlinuz-linux", "initramfs-linux.img"})
	ukiDir := filepath.Join(tmpDir, "boot", "EFI", "Linux")
	require.NoError(t, os.MkdirAll(ukiDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ukiDir, "linux.efi"), []byte("fake-uki"), 0o644))

	rootFS := testRootFS()
	planner := NewPlanner(fstab.NewManager(), nil, nil, rootFS, UKIStrategySkip)
	plans := planner.Plan([]*btrfs.Snapshot{snapshot})

	require.Len(t, plans, 2)
	layouts := map[BootLayout]bool{}
	for _, p := range plans {
		layouts[p.Layout] = true
	}
	assert.True(t, layouts[LayoutSplit], "expected a Split plan from vmlinuz-linux")
	assert.True(t, layouts[LayoutUKI], "expected a UKI plan from linux.efi")
}

func TestParseUKIStrategy(t *testing.T) {
	cases := []struct {
		in   string
		want UKIStrategy
	}{
		{"skip", UKIStrategySkip},
		{"warn", UKIStrategyWarn},
		{"disable", UKIStrategyDisable},
		{"", UKIStrategySkip},        // default
		{"nonsense", UKIStrategySkip}, // unknown falls back to safest default
	}
	for _, c := range cases {
		got := ParseUKIStrategy(c.in)
		assert.Equal(t, c.want, got, "input=%q", c.in)
	}
}
