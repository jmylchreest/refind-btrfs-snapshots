package kernel

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/fstab"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testSnapshot creates a minimal snapshot for testing.
func testSnapshot(path, fsPath string) *btrfs.Snapshot {
	return &btrfs.Snapshot{
		Subvolume: &btrfs.Subvolume{
			ID:   256,
			Path: path,
		},
		FilesystemPath: fsPath,
		SnapshotTime:   time.Now(),
	}
}

// testRootFS creates a minimal root filesystem for testing.
func testRootFS() *btrfs.Filesystem {
	return &btrfs.Filesystem{
		UUID:       "12345678-1234-1234-1234-123456789abc",
		Label:      "ARCH_ROOT",
		PartLabel:  "arch",
		Device:     "/dev/sda2",
		MountPoint: "/",
	}
}

// testBootSet creates a minimal boot set for testing.
func testBootSet(kernelName, version string) *BootSet {
	bs := &BootSet{
		KernelName: kernelName,
		Kernel: &BootImage{
			Path:       "/boot/" + "vmlinuz-" + kernelName,
			AbsPath:    "/boot/efi/boot/vmlinuz-" + kernelName,
			Filename:   "vmlinuz-" + kernelName,
			Role:       RoleKernel,
			KernelName: kernelName,
		},
		Initramfs: &BootImage{
			Path:       "/boot/" + "initramfs-" + kernelName + ".img",
			AbsPath:    "/boot/efi/boot/initramfs-" + kernelName + ".img",
			Filename:   "initramfs-" + kernelName + ".img",
			Role:       RoleInitramfs,
			KernelName: kernelName,
		},
	}
	if version != "" {
		bs.Kernel.Inspected = &InspectedMetadata{Version: version}
	}
	return bs
}

// setupSnapshotFstab creates a temporary fstab file inside a fake snapshot.
func setupSnapshotFstab(t *testing.T, fsPath string, content string) {
	t.Helper()
	fstabDir := filepath.Join(fsPath, "etc")
	require.NoError(t, os.MkdirAll(fstabDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fstabDir, "fstab"), []byte(content), 0o644))
}

// setupSnapshotBoot creates kernel files inside a fake snapshot's /boot.
func setupSnapshotBoot(t *testing.T, fsPath string, files []string) {
	t.Helper()
	bootDir := filepath.Join(fsPath, "boot")
	require.NoError(t, os.MkdirAll(bootDir, 0o755))
	for _, f := range files {
		require.NoError(t, os.WriteFile(filepath.Join(bootDir, f), []byte("fake"), 0o644))
	}
}

// setupSnapshotModules creates module directories inside a fake snapshot.
func setupSnapshotModules(t *testing.T, fsPath string, versions []string) {
	t.Helper()
	for _, v := range versions {
		modDir := filepath.Join(fsPath, "lib", "modules", v)
		require.NoError(t, os.MkdirAll(modDir, 0o755))
	}
}

// --- BootMountInfo tests (fstab analysis) ---

func TestAnalyzeBootMount_NoBootEntry(t *testing.T) {
	m := fstab.NewManager()
	f := &fstab.Fstab{
		Entries: []*fstab.Entry{
			{Device: "UUID=abc", Mountpoint: "/", FSType: "btrfs", Options: "subvol=@"},
			{Device: "UUID=def", Mountpoint: "/home", FSType: "btrfs", Options: "subvol=@home"},
		},
	}
	info := m.AnalyzeBootMount(f, testRootFS())
	assert.False(t, info.HasSeparateBootMount)
	assert.True(t, info.BootOnSameBtrfs)
	assert.Nil(t, info.Entry)
}

func TestAnalyzeBootMount_VfatBoot(t *testing.T) {
	m := fstab.NewManager()
	f := &fstab.Fstab{
		Entries: []*fstab.Entry{
			{Device: "UUID=abc", Mountpoint: "/", FSType: "btrfs", Options: "subvol=@"},
			{Device: "UUID=AAAA-BBBB", Mountpoint: "/boot", FSType: "vfat", Options: "defaults"},
		},
	}
	info := m.AnalyzeBootMount(f, testRootFS())
	assert.True(t, info.HasSeparateBootMount)
	assert.False(t, info.BootOnSameBtrfs)
	assert.NotNil(t, info.Entry)
	assert.Equal(t, "vfat", info.Entry.FSType)
}

func TestAnalyzeBootMount_Ext4Boot(t *testing.T) {
	m := fstab.NewManager()
	f := &fstab.Fstab{
		Entries: []*fstab.Entry{
			{Device: "UUID=abc", Mountpoint: "/", FSType: "btrfs", Options: "subvol=@"},
			{Device: "/dev/sda1", Mountpoint: "/boot", FSType: "ext4", Options: "defaults"},
		},
	}
	info := m.AnalyzeBootMount(f, testRootFS())
	assert.True(t, info.HasSeparateBootMount)
	assert.False(t, info.BootOnSameBtrfs)
}

func TestAnalyzeBootMount_BtrfsSameDevice(t *testing.T) {
	m := fstab.NewManager()
	rootFS := testRootFS()
	f := &fstab.Fstab{
		Entries: []*fstab.Entry{
			{Device: "UUID=12345678-1234-1234-1234-123456789abc", Mountpoint: "/", FSType: "btrfs", Options: "subvol=@"},
			{Device: "UUID=12345678-1234-1234-1234-123456789abc", Mountpoint: "/boot", FSType: "btrfs", Options: "subvol=@boot"},
		},
	}
	info := m.AnalyzeBootMount(f, rootFS)
	assert.True(t, info.HasSeparateBootMount)
	assert.True(t, info.BootOnSameBtrfs) // same btrfs device
}

func TestAnalyzeBootMount_BtrfsDifferentDevice(t *testing.T) {
	m := fstab.NewManager()
	rootFS := testRootFS()
	f := &fstab.Fstab{
		Entries: []*fstab.Entry{
			{Device: "UUID=12345678-1234-1234-1234-123456789abc", Mountpoint: "/", FSType: "btrfs", Options: "subvol=@"},
			{Device: "UUID=different-uuid-here", Mountpoint: "/boot", FSType: "btrfs", Options: "subvol=@boot"},
		},
	}
	info := m.AnalyzeBootMount(f, rootFS)
	assert.True(t, info.HasSeparateBootMount)
	assert.False(t, info.BootOnSameBtrfs) // different btrfs device
}

func TestAnalyzeBootMount_NilFstab(t *testing.T) {
	m := fstab.NewManager()
	info := m.AnalyzeBootMount(nil, testRootFS())
	assert.True(t, info.BootOnSameBtrfs) // conservative: assume btrfs mode
}

// --- Planner tests ---

func TestPlanner_BtrfsMode(t *testing.T) {
	tmpDir := t.TempDir()
	snapshot := testSnapshot("@/.snapshots/73/snapshot", tmpDir)

	// Fstab with no /boot entry (btrfs mode)
	setupSnapshotFstab(t, tmpDir, `UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=@/.snapshots/73/snapshot 0 1
`)

	// Kernel files inside snapshot
	setupSnapshotBoot(t, tmpDir, []string{
		"vmlinuz-linux",
		"initramfs-linux.img",
	})

	rootFS := testRootFS()
	planner := NewPlanner(fstab.NewManager(), nil, nil, rootFS)
	plans := planner.Plan([]*btrfs.Snapshot{snapshot})

	require.Len(t, plans, 1)
	assert.Equal(t, BootModeBtrfs, plans[0].Mode)
	assert.Contains(t, plans[0].SnapshotKernel, "vmlinuz-linux")
	assert.Contains(t, plans[0].SnapshotKernel, "@/.snapshots/73/snapshot")
	assert.Len(t, plans[0].SnapshotInitrds, 1)
	assert.Contains(t, plans[0].SnapshotInitrds[0], "initramfs-linux.img")
	assert.Equal(t, "ARCH_ROOT", plans[0].BtrfsVolume)
	assert.False(t, plans[0].IsStale())
	assert.False(t, plans[0].ShouldSkip())
}

func TestPlanner_ESPMode(t *testing.T) {
	tmpDir := t.TempDir()
	snapshot := testSnapshot("@/.snapshots/42/snapshot", tmpDir)

	// Fstab with /boot on vfat (ESP mode)
	setupSnapshotFstab(t, tmpDir, `UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=@/.snapshots/42/snapshot 0 1
UUID=AAAA-BBBB /boot vfat defaults 0 2
`)

	// Modules inside snapshot
	setupSnapshotModules(t, tmpDir, []string{"6.19.0-2-cachyos"})

	rootFS := testRootFS()
	bs := testBootSet("linux-cachyos", "6.19.0-2-cachyos")
	checker := NewChecker(ActionWarn)
	planner := NewPlanner(fstab.NewManager(), checker, []*BootSet{bs}, rootFS)
	plans := planner.Plan([]*btrfs.Snapshot{snapshot})

	require.Len(t, plans, 1)
	assert.Equal(t, BootModeESP, plans[0].Mode)
	assert.Equal(t, bs, plans[0].BootSet)
	assert.NotNil(t, plans[0].Staleness)
	assert.False(t, plans[0].Staleness.IsStale) // modules match
}

func TestPlanner_ESPMode_Stale(t *testing.T) {
	tmpDir := t.TempDir()
	snapshot := testSnapshot("@/.snapshots/42/snapshot", tmpDir)

	// Fstab with /boot on vfat (ESP mode)
	setupSnapshotFstab(t, tmpDir, `UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=@/.snapshots/42/snapshot 0 1
UUID=AAAA-BBBB /boot vfat defaults 0 2
`)

	// Modules inside snapshot DON'T match the boot kernel
	setupSnapshotModules(t, tmpDir, []string{"6.18.0-1-cachyos"})

	rootFS := testRootFS()
	bs := testBootSet("linux-cachyos", "6.19.0-2-cachyos")
	checker := NewChecker(ActionDelete)
	planner := NewPlanner(fstab.NewManager(), checker, []*BootSet{bs}, rootFS)
	plans := planner.Plan([]*btrfs.Snapshot{snapshot})

	require.Len(t, plans, 1)
	assert.Equal(t, BootModeESP, plans[0].Mode)
	assert.True(t, plans[0].IsStale())
	assert.True(t, plans[0].ShouldSkip())
}

func TestPlanner_MixedMode(t *testing.T) {
	tmpDir1 := t.TempDir()
	tmpDir2 := t.TempDir()

	// Snapshot 1: btrfs mode (no /boot entry, kernels in snapshot)
	snapshot1 := testSnapshot("@/.snapshots/73/snapshot", tmpDir1)
	setupSnapshotFstab(t, tmpDir1, `UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=@/.snapshots/73/snapshot 0 1
`)
	setupSnapshotBoot(t, tmpDir1, []string{
		"vmlinuz-linux",
		"initramfs-linux.img",
	})

	// Snapshot 2: ESP mode (/boot on vfat)
	snapshot2 := testSnapshot("@/.snapshots/42/snapshot", tmpDir2)
	setupSnapshotFstab(t, tmpDir2, `UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=@/.snapshots/42/snapshot 0 1
UUID=AAAA-BBBB /boot vfat defaults 0 2
`)
	setupSnapshotModules(t, tmpDir2, []string{"6.19.0-2-cachyos"})

	rootFS := testRootFS()
	bs := testBootSet("linux-cachyos", "6.19.0-2-cachyos")
	checker := NewChecker(ActionWarn)
	planner := NewPlanner(fstab.NewManager(), checker, []*BootSet{bs}, rootFS)
	plans := planner.Plan([]*btrfs.Snapshot{snapshot1, snapshot2})

	// Should have plans for both snapshots: 1 btrfs-mode, 1 ESP-mode
	require.Len(t, plans, 2)
	assert.Equal(t, BootModeBtrfs, plans[0].Mode)
	assert.Equal(t, BootModeESP, plans[1].Mode)
}

func TestPlanner_BtrfsMode_FallbackToESP(t *testing.T) {
	tmpDir := t.TempDir()
	snapshot := testSnapshot("@/.snapshots/73/snapshot", tmpDir)

	// Fstab says /boot is part of btrfs, but no kernel files exist
	setupSnapshotFstab(t, tmpDir, `UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=@/.snapshots/73/snapshot 0 1
`)
	// NOT creating any boot files — empty /boot

	rootFS := testRootFS()
	bs := testBootSet("linux-cachyos", "6.19.0-2-cachyos")
	checker := NewChecker(ActionWarn)
	planner := NewPlanner(fstab.NewManager(), checker, []*BootSet{bs}, rootFS)
	plans := planner.Plan([]*btrfs.Snapshot{snapshot})

	// Should fall back to ESP mode since no kernels in snapshot
	require.Len(t, plans, 1)
	assert.Equal(t, BootModeESP, plans[0].Mode)
}

func TestPlanner_NoFstab(t *testing.T) {
	tmpDir := t.TempDir()
	snapshot := testSnapshot("@/.snapshots/99/snapshot", tmpDir)
	// NOT creating fstab — should default to ESP mode

	rootFS := testRootFS()
	planner := NewPlanner(fstab.NewManager(), nil, nil, rootFS)
	plans := planner.Plan([]*btrfs.Snapshot{snapshot})

	require.Len(t, plans, 1)
	assert.Equal(t, BootModeESP, plans[0].Mode)
}

func TestPlanner_BtrfsMode_VolumeIdentifier(t *testing.T) {
	tests := []struct {
		name     string
		rootFS   *btrfs.Filesystem
		expected string
	}{
		{
			name:     "prefers label",
			rootFS:   &btrfs.Filesystem{Label: "ARCH_ROOT", PartLabel: "arch", UUID: "abc-def"},
			expected: "ARCH_ROOT",
		},
		{
			name:     "falls back to part label",
			rootFS:   &btrfs.Filesystem{PartLabel: "arch", UUID: "abc-def"},
			expected: "arch",
		},
		{
			name:     "falls back to UUID",
			rootFS:   &btrfs.Filesystem{UUID: "abc-def"},
			expected: "abc-def",
		},
		{
			name:     "falls back to part UUID",
			rootFS:   &btrfs.Filesystem{PartUUID: "xyz-123"},
			expected: "xyz-123",
		},
		{
			name:     "empty if no identifiers",
			rootFS:   &btrfs.Filesystem{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			planner := NewPlanner(fstab.NewManager(), nil, nil, tt.rootFS)
			assert.Equal(t, tt.expected, planner.buildBtrfsVolume())
		})
	}
}

func TestPlanner_BtrfsMode_MultipleKernels(t *testing.T) {
	tmpDir := t.TempDir()
	snapshot := testSnapshot("@/.snapshots/73/snapshot", tmpDir)

	setupSnapshotFstab(t, tmpDir, `UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=@/.snapshots/73/snapshot 0 1
`)

	// Two kernels in snapshot
	setupSnapshotBoot(t, tmpDir, []string{
		"vmlinuz-linux",
		"vmlinuz-linux-lts",
		"initramfs-linux.img",
		"initramfs-linux-lts.img",
	})

	rootFS := testRootFS()
	planner := NewPlanner(fstab.NewManager(), nil, nil, rootFS)
	plans := planner.Plan([]*btrfs.Snapshot{snapshot})

	// Should have one plan per kernel found
	assert.GreaterOrEqual(t, len(plans), 2)
	for _, p := range plans {
		assert.Equal(t, BootModeBtrfs, p.Mode)
		assert.NotEmpty(t, p.SnapshotKernel)
	}
}

func TestPlanner_ESPMode_MultipleBootSets(t *testing.T) {
	tmpDir := t.TempDir()
	snapshot := testSnapshot("@/.snapshots/42/snapshot", tmpDir)

	setupSnapshotFstab(t, tmpDir, `UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=@/.snapshots/42/snapshot 0 1
UUID=AAAA-BBBB /boot vfat defaults 0 2
`)
	setupSnapshotModules(t, tmpDir, []string{"6.19.0-2-cachyos", "6.6.78-1-lts"})

	rootFS := testRootFS()
	bs1 := testBootSet("linux-cachyos", "6.19.0-2-cachyos")
	bs2 := testBootSet("linux-lts", "6.6.78-1-lts")
	checker := NewChecker(ActionWarn)
	planner := NewPlanner(fstab.NewManager(), checker, []*BootSet{bs1, bs2}, rootFS)
	plans := planner.Plan([]*btrfs.Snapshot{snapshot})

	// One plan per boot set
	require.Len(t, plans, 2)
	assert.Equal(t, bs1, plans[0].BootSet)
	assert.Equal(t, bs2, plans[1].BootSet)
}

// --- Helper function tests ---

func TestBootPlan_FormatStaleSummary(t *testing.T) {
	btrfsPlan := &BootPlan{
		Snapshot: testSnapshot("@/.snapshots/73/snapshot", "/tmp"),
		Mode:     BootModeBtrfs,
	}
	assert.Contains(t, btrfsPlan.FormatStaleSummary(), "btrfs-mode")

	espPlan := &BootPlan{
		Snapshot:  testSnapshot("@/.snapshots/42/snapshot", "/tmp"),
		Mode:      BootModeESP,
		BootSet:   testBootSet("linux-cachyos", ""),
		Staleness: &StalenessResult{IsStale: true, Action: ActionWarn},
	}
	summary := espPlan.FormatStaleSummary()
	assert.Contains(t, summary, "linux-cachyos")
	assert.Contains(t, summary, "warn")

	freshEspPlan := &BootPlan{
		Snapshot:  testSnapshot("@/.snapshots/42/snapshot", "/tmp"),
		Mode:      BootModeESP,
		BootSet:   testBootSet("linux", ""),
		Staleness: &StalenessResult{IsStale: false},
	}
	assert.Empty(t, freshEspPlan.FormatStaleSummary())
}

func TestFindKernelImages(t *testing.T) {
	tmpDir := t.TempDir()

	// Create kernel files
	files := []string{
		"vmlinuz-linux",
		"initramfs-linux.img",
		"initramfs-linux-fallback.img",
		"intel-ucode.img",
	}
	for _, f := range files {
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, f), []byte("fake"), 0o644))
	}

	results := findKernelImages(tmpDir)
	require.Len(t, results, 1)
	assert.Equal(t, "vmlinuz-linux", results[0].kernelFilename)
	// Should have microcode + primary initramfs
	assert.GreaterOrEqual(t, len(results[0].initrdFilenames), 1)
}

func TestFindKernelImages_NonexistentDir(t *testing.T) {
	results := findKernelImages("/nonexistent/path")
	assert.Nil(t, results)
}

func TestFindKernelImages_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	results := findKernelImages(tmpDir)
	assert.Nil(t, results)
}

// --- Backward compatibility and transition scenario tests ---

// TestPlanner_ESPOnly_BackwardCompat verifies that a pure ESP setup (the common
// case for existing users) produces the same staleness results through the
// planner as the old inline code path did.
func TestPlanner_ESPOnly_BackwardCompat(t *testing.T) {
	// 5 snapshots, all with /boot on vfat — the typical existing user setup
	rootFS := testRootFS()
	bs := testBootSet("linux-cachyos", "6.19.0-2-cachyos")
	checker := NewChecker(ActionWarn)

	var snapshots []*btrfs.Snapshot
	for i := 0; i < 5; i++ {
		tmpDir := t.TempDir()
		path := fmt.Sprintf("@/.snapshots/%d/snapshot", 100+i)
		snap := testSnapshot(path, tmpDir)

		setupSnapshotFstab(t, tmpDir, `UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=`+path+` 0 1
UUID=AAAA-BBBB /boot vfat defaults 0 2
`)
		// First 3 have matching modules, last 2 have old modules
		if i < 3 {
			setupSnapshotModules(t, tmpDir, []string{"6.19.0-2-cachyos"})
		} else {
			setupSnapshotModules(t, tmpDir, []string{"6.18.0-1-cachyos"})
		}

		snapshots = append(snapshots, snap)
	}

	planner := NewPlanner(fstab.NewManager(), checker, []*BootSet{bs}, rootFS)
	plans := planner.Plan(snapshots)

	// All should be ESP mode
	require.Len(t, plans, 5)
	for _, p := range plans {
		assert.Equal(t, BootModeESP, p.Mode, "snapshot %s", p.Snapshot.Path)
		assert.NotNil(t, p.BootSet)
		assert.NotNil(t, p.Staleness)
	}

	// First 3 fresh, last 2 stale
	for i := 0; i < 3; i++ {
		assert.False(t, plans[i].IsStale(), "snapshot %d should be fresh", i)
	}
	for i := 3; i < 5; i++ {
		assert.True(t, plans[i].IsStale(), "snapshot %d should be stale", i)
		assert.Equal(t, ActionWarn, plans[i].Staleness.Action)
	}
}

// TestPlanner_ESPOnly_AllStaleActions tests each staleness action through the planner.
func TestPlanner_ESPOnly_AllStaleActions(t *testing.T) {
	tests := []struct {
		name       string
		action     StaleAction
		wantSkip   bool
		wantAction StaleAction
	}{
		{"warn", ActionWarn, false, ActionWarn},
		{"disable", ActionDisable, false, ActionDisable},
		{"delete", ActionDelete, true, ActionDelete},
		{"fallback_no_fallback", ActionFallback, false, ActionDisable}, // downgrades to disable
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			snap := testSnapshot("@/.snapshots/42/snapshot", tmpDir)

			setupSnapshotFstab(t, tmpDir, `UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=@/.snapshots/42/snapshot 0 1
UUID=AAAA-BBBB /boot vfat defaults 0 2
`)
			// Mismatched modules to trigger staleness
			setupSnapshotModules(t, tmpDir, []string{"6.18.0-1-cachyos"})

			rootFS := testRootFS()
			bs := testBootSet("linux-cachyos", "6.19.0-2-cachyos")
			checker := NewChecker(tt.action)
			planner := NewPlanner(fstab.NewManager(), checker, []*BootSet{bs}, rootFS)
			plans := planner.Plan([]*btrfs.Snapshot{snap})

			require.Len(t, plans, 1)
			assert.True(t, plans[0].IsStale())
			assert.Equal(t, tt.wantSkip, plans[0].ShouldSkip())
			assert.Equal(t, tt.wantAction, plans[0].Staleness.Action)
		})
	}
}

// TestPlanner_ESPOnly_FallbackWithImage tests that when stale_snapshot_action=fallback
// and a fallback initramfs exists on the ESP, the planner uses ActionFallback and
// marks FallbackUsed=true. The entry is stale but NOT skipped.
func TestPlanner_ESPOnly_FallbackWithImage(t *testing.T) {
	tmpDir := t.TempDir()
	snap := testSnapshot("@/.snapshots/42/snapshot", tmpDir)

	setupSnapshotFstab(t, tmpDir, `UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=@/.snapshots/42/snapshot 0 1
UUID=AAAA-BBBB /boot vfat defaults 0 2
`)
	setupSnapshotModules(t, tmpDir, []string{"6.18.0-1-cachyos"})

	rootFS := testRootFS()
	bs := testBootSet("linux-cachyos", "6.19.0-2-cachyos")
	bs.Fallback = &BootImage{
		Path:       "/boot/initramfs-linux-cachyos-fallback.img",
		Filename:   "initramfs-linux-cachyos-fallback.img",
		Role:       RoleFallbackInitramfs,
		KernelName: "linux-cachyos",
	}

	checker := NewChecker(ActionFallback)
	planner := NewPlanner(fstab.NewManager(), checker, []*BootSet{bs}, rootFS)
	plans := planner.Plan([]*btrfs.Snapshot{snap})

	require.Len(t, plans, 1)
	assert.True(t, plans[0].IsStale())
	assert.Equal(t, ActionFallback, plans[0].Staleness.Action)
	assert.True(t, plans[0].Staleness.FallbackUsed)
	assert.False(t, plans[0].ShouldSkip(), "fallback action must not skip the entry")
}

// TestPlanner_ESPOnly_FallbackWithoutImage tests that when stale_snapshot_action=fallback
// but NO fallback initramfs exists on the ESP, the action automatically downgrades
// to ActionDisable. This protects the user from generating an entry that references
// a nonexistent fallback initramfs.
func TestPlanner_ESPOnly_FallbackWithoutImage(t *testing.T) {
	tmpDir := t.TempDir()
	snap := testSnapshot("@/.snapshots/42/snapshot", tmpDir)

	setupSnapshotFstab(t, tmpDir, `UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=@/.snapshots/42/snapshot 0 1
UUID=AAAA-BBBB /boot vfat defaults 0 2
`)
	setupSnapshotModules(t, tmpDir, []string{"6.18.0-1-cachyos"})

	rootFS := testRootFS()
	bs := testBootSet("linux-cachyos", "6.19.0-2-cachyos")
	// Explicitly: no bs.Fallback set (nil)

	checker := NewChecker(ActionFallback) // configured for fallback, but no image exists
	planner := NewPlanner(fstab.NewManager(), checker, []*BootSet{bs}, rootFS)
	plans := planner.Plan([]*btrfs.Snapshot{snap})

	require.Len(t, plans, 1)
	assert.True(t, plans[0].IsStale())
	// Key assertion: downgrades from fallback to disable
	assert.Equal(t, ActionDisable, plans[0].Staleness.Action,
		"must downgrade to 'disable' when fallback image is missing")
	assert.False(t, plans[0].Staleness.FallbackUsed,
		"FallbackUsed must be false when no fallback image exists")
	assert.False(t, plans[0].ShouldSkip(), "disable action must not skip the entry")
}

// TestPlanner_ESPToBtrfsTransition simulates a user who moved /boot from a
// separate ESP partition into the btrfs filesystem. Older snapshots have
// /boot on vfat, newer ones don't.
func TestPlanner_ESPToBtrfsTransition(t *testing.T) {
	rootFS := testRootFS()
	bs := testBootSet("linux-cachyos", "6.19.0-2-cachyos")
	checker := NewChecker(ActionWarn)

	// Old snapshot: /boot on vfat (ESP mode)
	tmpOld := t.TempDir()
	snapOld := testSnapshot("@/.snapshots/50/snapshot", tmpOld)
	setupSnapshotFstab(t, tmpOld, `UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=@/.snapshots/50/snapshot 0 1
UUID=AAAA-BBBB /boot vfat defaults 0 2
`)
	setupSnapshotModules(t, tmpOld, []string{"6.18.0-1-cachyos"}) // old kernel

	// New snapshot: /boot inside btrfs (no /boot entry)
	tmpNew := t.TempDir()
	snapNew := testSnapshot("@/.snapshots/100/snapshot", tmpNew)
	setupSnapshotFstab(t, tmpNew, `UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=@/.snapshots/100/snapshot 0 1
`)
	setupSnapshotBoot(t, tmpNew, []string{
		"vmlinuz-linux-cachyos",
		"initramfs-linux-cachyos.img",
	})

	planner := NewPlanner(fstab.NewManager(), checker, []*BootSet{bs}, rootFS)
	plans := planner.Plan([]*btrfs.Snapshot{snapOld, snapNew})

	require.Len(t, plans, 2)

	// Old snapshot: ESP mode, stale (old modules)
	assert.Equal(t, BootModeESP, plans[0].Mode)
	assert.True(t, plans[0].IsStale())
	assert.Equal(t, ActionWarn, plans[0].Staleness.Action)

	// New snapshot: btrfs mode, never stale
	assert.Equal(t, BootModeBtrfs, plans[1].Mode)
	assert.False(t, plans[1].IsStale())
	assert.Empty(t, plans[1].SnapshotKernel == "", "should have kernel path")
	assert.NotEmpty(t, plans[1].SnapshotKernel)
	assert.Contains(t, plans[1].SnapshotKernel, "vmlinuz-linux-cachyos")
}

// TestPlanner_BtrfsToESPTransition simulates a user who added a separate
// /boot partition. Older snapshots have /boot in btrfs, newer ones have
// /boot on vfat.
func TestPlanner_BtrfsToESPTransition(t *testing.T) {
	rootFS := testRootFS()
	bs := testBootSet("linux", "6.19.0-2-cachyos")
	checker := NewChecker(ActionWarn)

	// Old snapshot: /boot inside btrfs (no /boot fstab entry)
	tmpOld := t.TempDir()
	snapOld := testSnapshot("@/.snapshots/30/snapshot", tmpOld)
	setupSnapshotFstab(t, tmpOld, `UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=@/.snapshots/30/snapshot 0 1
`)
	setupSnapshotBoot(t, tmpOld, []string{
		"vmlinuz-linux",
		"initramfs-linux.img",
	})

	// New snapshot: /boot on vfat (ESP mode)
	tmpNew := t.TempDir()
	snapNew := testSnapshot("@/.snapshots/80/snapshot", tmpNew)
	setupSnapshotFstab(t, tmpNew, `UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=@/.snapshots/80/snapshot 0 1
UUID=AAAA-BBBB /boot vfat defaults 0 2
`)
	setupSnapshotModules(t, tmpNew, []string{"6.19.0-2-cachyos"})

	planner := NewPlanner(fstab.NewManager(), checker, []*BootSet{bs}, rootFS)
	plans := planner.Plan([]*btrfs.Snapshot{snapOld, snapNew})

	require.Len(t, plans, 2)

	// Old snapshot: btrfs mode, self-contained, never stale
	assert.Equal(t, BootModeBtrfs, plans[0].Mode)
	assert.False(t, plans[0].IsStale())
	assert.Contains(t, plans[0].SnapshotKernel, "vmlinuz-linux")
	assert.Equal(t, "ARCH_ROOT", plans[0].BtrfsVolume)

	// New snapshot: ESP mode, fresh (modules match)
	assert.Equal(t, BootModeESP, plans[1].Mode)
	assert.False(t, plans[1].IsStale())
	assert.NotNil(t, plans[1].BootSet)
}

// TestPlanner_BtrfsMode_NeverStaleRegardlessOfModules verifies that btrfs-mode
// snapshots are never marked stale, even if their /lib/modules doesn't match
// any ESP boot set. This is the key behavioral guarantee.
func TestPlanner_BtrfsMode_NeverStaleRegardlessOfModules(t *testing.T) {
	tmpDir := t.TempDir()
	snap := testSnapshot("@/.snapshots/73/snapshot", tmpDir)

	setupSnapshotFstab(t, tmpDir, `UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=@/.snapshots/73/snapshot 0 1
`)
	setupSnapshotBoot(t, tmpDir, []string{
		"vmlinuz-linux",
		"initramfs-linux.img",
	})
	// Modules don't match the ESP boot set — but shouldn't matter!
	setupSnapshotModules(t, tmpDir, []string{"5.15.0-old-kernel"})

	rootFS := testRootFS()
	bs := testBootSet("linux-cachyos", "6.19.0-2-cachyos")
	checker := NewChecker(ActionDelete) // harshest action
	planner := NewPlanner(fstab.NewManager(), checker, []*BootSet{bs}, rootFS)
	plans := planner.Plan([]*btrfs.Snapshot{snap})

	require.Len(t, plans, 1)
	assert.Equal(t, BootModeBtrfs, plans[0].Mode)
	assert.False(t, plans[0].IsStale(), "btrfs-mode must never be stale")
	assert.False(t, plans[0].ShouldSkip(), "btrfs-mode must never be skipped")
}

// TestPlanner_ESPOnly_NoBootSets verifies behavior when no boot sets are
// detected on the ESP (e.g., first run, empty ESP). Planner should still
// produce plans without crashing.
func TestPlanner_ESPOnly_NoBootSets(t *testing.T) {
	tmpDir := t.TempDir()
	snap := testSnapshot("@/.snapshots/42/snapshot", tmpDir)

	setupSnapshotFstab(t, tmpDir, `UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=@/.snapshots/42/snapshot 0 1
UUID=AAAA-BBBB /boot vfat defaults 0 2
`)

	rootFS := testRootFS()
	planner := NewPlanner(fstab.NewManager(), nil, nil, rootFS) // no boot sets
	plans := planner.Plan([]*btrfs.Snapshot{snap})

	require.Len(t, plans, 1)
	assert.Equal(t, BootModeESP, plans[0].Mode)
	assert.Nil(t, plans[0].BootSet)
	assert.Nil(t, plans[0].Staleness)
	assert.False(t, plans[0].IsStale())
	assert.False(t, plans[0].ShouldSkip())
}

// TestPlanner_MixedMode_LargeScale simulates a realistic scenario: 80 snapshots
// where the user switched from ESP to btrfs at snapshot 60.
func TestPlanner_MixedMode_LargeScale(t *testing.T) {
	rootFS := testRootFS()
	bs := testBootSet("linux-cachyos", "6.19.0-2-cachyos")
	checker := NewChecker(ActionWarn)

	var snapshots []*btrfs.Snapshot
	for i := 1; i <= 80; i++ {
		tmpDir := t.TempDir()
		path := fmt.Sprintf("@/.snapshots/%d/snapshot", i)
		snap := testSnapshot(path, tmpDir)

		if i <= 60 {
			// Old snapshots: ESP mode
			setupSnapshotFstab(t, tmpDir, fmt.Sprintf(`UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=%s 0 1
UUID=AAAA-BBBB /boot vfat defaults 0 2
`, path))
			if i > 50 {
				// Recent ESP snapshots have matching modules
				setupSnapshotModules(t, tmpDir, []string{"6.19.0-2-cachyos"})
			} else {
				// Older ESP snapshots have old modules
				setupSnapshotModules(t, tmpDir, []string{"6.17.0-1-cachyos"})
			}
		} else {
			// New snapshots: btrfs mode (after user moved /boot into btrfs)
			setupSnapshotFstab(t, tmpDir, fmt.Sprintf(`UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=%s 0 1
`, path))
			setupSnapshotBoot(t, tmpDir, []string{
				"vmlinuz-linux-cachyos",
				"initramfs-linux-cachyos.img",
			})
		}

		snapshots = append(snapshots, snap)
	}

	planner := NewPlanner(fstab.NewManager(), checker, []*BootSet{bs}, rootFS)
	plans := planner.Plan(snapshots)

	require.Len(t, plans, 80)

	// Count modes
	espCount := 0
	btrfsCount := 0
	staleCount := 0
	for _, p := range plans {
		switch p.Mode {
		case BootModeESP:
			espCount++
		case BootModeBtrfs:
			btrfsCount++
		}
		if p.IsStale() {
			staleCount++
		}
	}

	assert.Equal(t, 60, espCount)
	assert.Equal(t, 20, btrfsCount)
	assert.Equal(t, 50, staleCount) // snapshots 1-50 are stale (old modules)

	// Verify no btrfs-mode snapshot is stale
	for _, p := range plans {
		if p.Mode == BootModeBtrfs {
			assert.False(t, p.IsStale(), "btrfs-mode snapshot %s must not be stale", p.Snapshot.Path)
		}
	}
}
