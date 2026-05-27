package bls

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/bootloader"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/config"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Documented contract:
//   - Generate returns empty Output when Cfg.BLS.WriteEntries is false (no-op).
//   - Generate returns one new-file FileDiff per eligible BootPlan.
//   - Existing on-disk files matching the prefix that aren't in the expected
//     set are returned as removal FileDiffs (Modified="", Original=content).
//   - Files outside the prefix are untouched, even in the same directory.
//   - Output.UpdatedConfigs lists the entries dir so the run summary
//     surfaces the change.

func espCfg(write bool, dir, prefix string) *config.Config {
	return &config.Config{
		BLS: config.BLSConfig{
			WriteEntries: config.Truthy(write),
			EntriesDir:   dir,
			EntryPrefix:  prefix,
		},
	}
}

func makeBootPlan(snapPath string, id uint64, kernelName string) *kernel.BootPlan {
	return &kernel.BootPlan{
		Snapshot: &btrfs.Snapshot{
			Subvolume:    &btrfs.Subvolume{ID: id, Path: snapPath},
			SnapshotTime: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		Mode:   kernel.BootModeESP,
		Layout: kernel.LayoutSplit,
		BootSet: &kernel.BootSet{
			KernelName: kernelName,
			Layout:     kernel.LayoutSplit,
			Kernel: &kernel.BootImage{
				Path:     "/vmlinuz-" + kernelName,
				Filename: "vmlinuz-" + kernelName,
				Role:     kernel.RoleKernel,
			},
			Initramfs: &kernel.BootImage{
				Path:     "/initramfs-" + kernelName + ".img",
				Filename: "initramfs-" + kernelName + ".img",
				Role:     kernel.RoleInitramfs,
			},
		},
	}
}

func TestBLSGenerator_DisabledByDefault(t *testing.T) {
	espDir := t.TempDir()
	input := bootloader.Input{
		Cfg:       espCfg(false, "/loader/entries", "bls-btrfs-snapshots-"),
		ESPPath:   espDir,
		BootPlans: []*kernel.BootPlan{makeBootPlan("@/.snapshots/73/snapshot", 256, "linux")},
		SourceEntries: []bootloader.SourceEntry{
			{Title: "Arch Linux", Loader: "/vmlinuz-linux", Options: "root=UUID=x rw"},
		},
	}

	out, err := NewGenerator().Generate(input)
	require.NoError(t, err)
	assert.Empty(t, out.Diffs, "WriteEntries=false must produce no diffs")
}

func TestBLSGenerator_EmitsEntryPerEligiblePlan(t *testing.T) {
	espDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(espDir, "loader", "entries"), 0o755))

	plan := makeBootPlan("@/.snapshots/73/snapshot", 256, "linux")
	input := bootloader.Input{
		Cfg:       espCfg(true, "/loader/entries", "bls-btrfs-snapshots-"),
		ESPPath:   espDir,
		BootPlans: []*kernel.BootPlan{plan},
		SourceEntries: []bootloader.SourceEntry{
			{Title: "Arch Linux", Loader: "/vmlinuz-linux", Options: "root=UUID=x rw rootflags=subvol=@"},
		},
	}

	out, err := NewGenerator().Generate(input)
	require.NoError(t, err)
	require.Len(t, out.Diffs, 1, "expected one BLS entry diff")

	d := out.Diffs[0]
	assert.True(t, d.IsNew, "new entry should be marked IsNew")
	assert.Equal(t, "", d.Original)
	assert.Contains(t, d.Path, "bls-btrfs-snapshots-")
	assert.True(t, strings.HasSuffix(d.Path, ".conf"))
	assert.Contains(t, d.Modified, "linux /vmlinuz-linux")
	assert.Contains(t, d.Modified, "subvol=@/.snapshots/73/snapshot")
	assert.Contains(t, d.Modified, "subvolid=256")
}

func TestBLSGenerator_SkipsBtrfsModePlans(t *testing.T) {
	espDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(espDir, "loader", "entries"), 0o755))

	plan := makeBootPlan("@/.snapshots/42/snapshot", 101, "linux")
	plan.Mode = kernel.BootModeBtrfs

	input := bootloader.Input{
		Cfg:       espCfg(true, "/loader/entries", "bls-btrfs-snapshots-"),
		ESPPath:   espDir,
		BootPlans: []*kernel.BootPlan{plan},
		SourceEntries: []bootloader.SourceEntry{
			{Title: "Arch Linux", Loader: "/vmlinuz-linux", Options: "root=UUID=x rw"},
		},
	}

	out, err := NewGenerator().Generate(input)
	require.NoError(t, err)
	assert.Empty(t, out.Diffs, "btrfs-mode plans must not produce BLS entries")
}

func TestBLSGenerator_OrphanCleanup(t *testing.T) {
	espDir := t.TempDir()
	entriesDir := filepath.Join(espDir, "loader", "entries")
	require.NoError(t, os.MkdirAll(entriesDir, 0o755))

	// Plant three managed files: one we still want (256, the current snapshot
	// with the same source-title slug we'd produce), two orphans (50, 99).
	planted := map[string]string{
		"bls-btrfs-snapshots-256-arch-linux.conf": "title old\nlinux /old\n",
		"bls-btrfs-snapshots-50-arch-linux.conf":  "title orphan\nlinux /orphan\n",
		"bls-btrfs-snapshots-99-arch-linux.conf":  "title orphan2\nlinux /orphan2\n",
		// Unrelated file (not our prefix) — must be untouched.
		"arch.conf": "title Arch\nlinux /vmlinuz-linux\n",
	}
	for name, body := range planted {
		require.NoError(t, os.WriteFile(filepath.Join(entriesDir, name), []byte(body), 0o644))
	}

	plan := makeBootPlan("@/.snapshots/73/snapshot", 256, "linux")
	input := bootloader.Input{
		Cfg:       espCfg(true, "/loader/entries", "bls-btrfs-snapshots-"),
		ESPPath:   espDir,
		BootPlans: []*kernel.BootPlan{plan},
		SourceEntries: []bootloader.SourceEntry{
			{Title: "Arch Linux", Loader: "/vmlinuz-linux", Options: "root=UUID=x rw"},
		},
	}

	out, err := NewGenerator().Generate(input)
	require.NoError(t, err)

	// Categorise diffs by op.
	var updates, removals []*string
	for _, d := range out.Diffs {
		path := d.Path
		if d.Modified == "" && d.Original != "" {
			removals = append(removals, &path)
		} else {
			updates = append(updates, &path)
		}
	}

	assert.Len(t, removals, 2, "two orphan removals expected: %v", removals)
	assert.Len(t, updates, 1, "one update for the current snapshot")

	// arch.conf must NOT appear anywhere — it's outside our prefix.
	for _, d := range out.Diffs {
		assert.NotContains(t, d.Path, "arch.conf",
			"non-prefixed files must not be touched")
	}
}

func TestBLSGenerator_NoSourceEntryNoEmit(t *testing.T) {
	// If there's no source entry to derive the cmdline from, we have
	// nothing meaningful to write — skip rather than emit a broken entry.
	espDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(espDir, "loader", "entries"), 0o755))

	plan := makeBootPlan("@/.snapshots/73/snapshot", 256, "linux")
	input := bootloader.Input{
		Cfg:           espCfg(true, "/loader/entries", "bls-btrfs-snapshots-"),
		ESPPath:       espDir,
		BootPlans:     []*kernel.BootPlan{plan},
		SourceEntries: nil, // <-- no sources
	}

	out, err := NewGenerator().Generate(input)
	require.NoError(t, err)
	assert.Empty(t, out.Diffs, "no source entries → no BLS output")
}

func TestBLSGenerator_NameAndUpdatedConfigs(t *testing.T) {
	g := NewGenerator()
	assert.Equal(t, "bls", g.Name())

	espDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(espDir, "loader", "entries"), 0o755))
	out, err := g.Generate(bootloader.Input{
		Cfg:       espCfg(true, "/loader/entries", "bls-btrfs-snapshots-"),
		ESPPath:   espDir,
		BootPlans: []*kernel.BootPlan{makeBootPlan("@/.snapshots/73/snapshot", 256, "linux")},
		SourceEntries: []bootloader.SourceEntry{
			{Title: "Arch Linux", Loader: "/vmlinuz-linux", Options: "root=UUID=x rw"},
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, out.UpdatedConfigs, "UpdatedConfigs should list the entries dir")
	assert.Contains(t, out.UpdatedConfigs[0], "/loader/entries")
}

func TestSnapshotDisplayName(t *testing.T) {
	ts := time.Date(2026, 5, 27, 16, 0, 0, 0, time.UTC)

	tests := []struct {
		name         string
		path         string
		menuFormat   string
		useLocalTime bool
		want         string
	}{
		{
			name:       "default_menu_format_utc",
			path:       "/.snapshots/8064/snapshot",
			menuFormat: "2006-01-02T15:04:05Z",
			want:       "2026-05-27T16:00:00Z",
		},
		{
			name:       "custom_menu_format_friendly_placeholders",
			path:       "/.snapshots/8064/snapshot",
			menuFormat: "YYYY/MM/DD HH:mm",
			want:       "2026/05/27 16:00",
		},
		{
			name:       "rwsnap_prefix_extracts_embedded_timestamp",
			path:       "/.snapshots/rwsnap_2026-05-27T16-00-00_42",
			menuFormat: "2006-01-02T15:04:05Z",
			want:       "2026-05-27T16-00-00",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := &btrfs.Snapshot{
				Subvolume:    &btrfs.Subvolume{ID: 42, Path: tt.path},
				SnapshotTime: ts,
			}
			got := snapshotDisplayName(snap, tt.menuFormat, tt.useLocalTime)
			assert.Equal(t, tt.want, got)
		})
	}
}
