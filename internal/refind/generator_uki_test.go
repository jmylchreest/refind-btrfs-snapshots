package refind

import (
	"strings"
	"testing"
	"time"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/stretchr/testify/assert"
)

// Documented contract for UKI snapshot submenus (per docs/USAGE.md):
//
//   - Btrfs-mode UKI submenu: override `volume` and `loader` to point at the
//     snapshot's UKI. NO `initrd` lines (initramfs is embedded in the UKI).
//     NO `options` line (systemd-stub ignores load_options; the embedded
//     cmdline is what matters, and the UKI inside the snapshot already
//     points at that snapshot's subvolume).
//
//   - ESP-mode UKI submenu with Disabled=true: emit rEFInd's `disabled`
//     directive so the entry is visible but unbootable.
//
//   - ESP-mode UKI submenu with Disabled=false (warn strategy): emit a
//     normal submenu. The fact that it boots live root is logged at plan
//     time; the menu entry exists so users can see what's there.

func testBtrfsModeUKIPlan(snap *btrfs.Snapshot) *kernel.BootPlan {
	return &kernel.BootPlan{
		Snapshot:       snap,
		Mode:           kernel.BootModeBtrfs,
		Layout:         kernel.LayoutUKI,
		SnapshotKernel: "/@/.snapshots/73/snapshot/boot/EFI/Linux/linux.efi",
		BtrfsVolume:    "ARCH_ROOT",
	}
}

func testESPModeUKIPlan(snap *btrfs.Snapshot, disabled bool) *kernel.BootPlan {
	return &kernel.BootPlan{
		Snapshot: snap,
		Mode:     kernel.BootModeESP,
		Layout:   kernel.LayoutUKI,
		Disabled: disabled,
	}
}

func snapshotAt(path string, id uint64, when time.Time) *btrfs.Snapshot {
	return &btrfs.Snapshot{
		Subvolume:    &btrfs.Subvolume{ID: id, Path: path},
		SnapshotTime: when,
	}
}

func TestGenerateSingleMenuEntry_BtrfsModeUKI(t *testing.T) {
	snap := snapshotAt("@/.snapshots/73/snapshot", 256, time.Date(2025, 2, 14, 10, 0, 0, 0, time.UTC))

	plan := testBtrfsModeUKIPlan(snap)
	gen := NewGeneratorWithBootPlans("/boot/efi", "2006-01-02T15:04:05Z", false, nil, nil, []*kernel.BootPlan{plan})

	template := &MenuEntry{
		Loader:  "/EFI/Linux/linux.efi",
		Options: "quiet rw root=UUID=test-uuid",
	}
	rootFS := &btrfs.Filesystem{UUID: "test-uuid"}
	content := gen.generateSingleMenuEntry("Linux UKI", template, []*btrfs.Snapshot{snap}, rootFS)

	// Submenu must be present.
	assert.Contains(t, content, "submenuentry ")
	// Volume + loader overrides for btrfs-mode UKI.
	assert.Contains(t, content, "volume  ARCH_ROOT")
	assert.Contains(t, content, "loader  /@/.snapshots/73/snapshot/boot/EFI/Linux/linux.efi")
	// MUST NOT contain initrd line — UKI embeds initramfs.
	assert.NotRegexp(t, `(?m)^\s+initrd\s+`, strings.Split(content, "submenuentry")[1],
		"UKI btrfs submenu must not emit initrd lines")
	// MUST NOT contain options line — systemd-stub ignores load_options on standard UKIs.
	submenuBlock := strings.Split(content, "submenuentry")[1]
	assert.NotRegexp(t, `(?m)^\s+options\s+`, submenuBlock,
		"UKI submenu must not emit options line")
}

func TestGenerateSingleMenuEntry_ESPModeUKIDisabled(t *testing.T) {
	snap := snapshotAt("@/.snapshots/42/snapshot", 101, time.Date(2025, 6, 12, 7, 0, 18, 0, time.UTC))

	plan := testESPModeUKIPlan(snap, true)
	gen := NewGeneratorWithBootPlans("/boot/efi", "2006-01-02T15:04:05Z", false, nil, nil, []*kernel.BootPlan{plan})

	template := &MenuEntry{
		Loader:  "/EFI/Linux/linux.efi",
		Options: "quiet rw root=UUID=test-uuid",
	}
	rootFS := &btrfs.Filesystem{UUID: "test-uuid"}
	content := gen.generateSingleMenuEntry("Linux UKI", template, []*btrfs.Snapshot{snap}, rootFS)

	submenuBlock := strings.Split(content, "submenuentry")[1]
	assert.Contains(t, submenuBlock, "disabled",
		"ESP-mode UKI with Disabled=true must emit rEFInd's `disabled` directive")
	// No initrd or options for any UKI submenu, regardless of disabled state.
	assert.NotRegexp(t, `(?m)^\s+initrd\s+`, submenuBlock,
		"UKI submenu must not emit initrd lines")
	assert.NotRegexp(t, `(?m)^\s+options\s+`, submenuBlock,
		"UKI submenu must not emit options line")
}

func TestGenerateSingleMenuEntry_ESPModeUKIWarn(t *testing.T) {
	snap := snapshotAt("@/.snapshots/42/snapshot", 101, time.Date(2025, 6, 12, 7, 0, 18, 0, time.UTC))

	plan := testESPModeUKIPlan(snap, false)
	gen := NewGeneratorWithBootPlans("/boot/efi", "2006-01-02T15:04:05Z", false, nil, nil, []*kernel.BootPlan{plan})

	template := &MenuEntry{
		Loader:  "/EFI/Linux/linux.efi",
		Options: "quiet rw root=UUID=test-uuid",
	}
	rootFS := &btrfs.Filesystem{UUID: "test-uuid"}
	content := gen.generateSingleMenuEntry("Linux UKI", template, []*btrfs.Snapshot{snap}, rootFS)

	submenuBlock := strings.Split(content, "submenuentry")[1]
	assert.NotContains(t, submenuBlock, "disabled",
		"warn-strategy UKI plan must not emit disabled directive")
	assert.NotRegexp(t, `(?m)^\s+initrd\s+`, submenuBlock)
	assert.NotRegexp(t, `(?m)^\s+options\s+`, submenuBlock)
}

func TestGenerateSingleMenuEntry_SplitLayoutUnchanged(t *testing.T) {
	// Sanity: existing Split-layout behaviour must be unchanged when Layout
	// is left as the zero value (LayoutSplit) or explicitly set.
	snap := snapshotAt("@/.snapshots/42/snapshot", 101, time.Date(2025, 6, 12, 7, 0, 18, 0, time.UTC))

	plan := &kernel.BootPlan{
		Snapshot: snap,
		Mode:     kernel.BootModeESP,
		Layout:   kernel.LayoutSplit,
	}
	gen := NewGeneratorWithBootPlans("/boot/efi", "2006-01-02T15:04:05Z", false, nil, nil, []*kernel.BootPlan{plan})

	template := &MenuEntry{
		Loader:  "/boot/vmlinuz-linux",
		Initrd:  []string{"/boot/initramfs-linux.img"},
		Options: "quiet rw rootflags=subvol=@ root=UUID=test-uuid",
	}
	rootFS := &btrfs.Filesystem{UUID: "test-uuid"}
	content := gen.generateSingleMenuEntry("Arch Linux", template, []*btrfs.Snapshot{snap}, rootFS)

	submenuBlock := strings.Split(content, "submenuentry")[1]
	// Split-layout ESP-mode submenu still emits the options override.
	assert.Contains(t, submenuBlock, "options",
		"Split-layout submenu must still emit options override")
	assert.Contains(t, submenuBlock, "subvol=@/.snapshots/42/snapshot")
}
