package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaults(t *testing.T) {
	d := Defaults()

	assert.Equal(t, []string{"/.snapshots"}, d.Snapshot.SearchDirectories)
	assert.Equal(t, 3, d.Snapshot.MaxDepth)
	assert.Equal(t, "toggle", d.Snapshot.WritableMethod)
	assert.Equal(t, "/EFI/refind/refind.conf", d.Refind.ConfigPath)
	assert.True(t, d.ESP.AutoDetect.IsTrue())
	assert.True(t, d.Behavior.ExitOnSnapshotBoot.IsTrue())
	assert.True(t, d.Behavior.CleanupOldSnapshots.IsTrue())
	assert.Equal(t, "delete", d.Kernel.StaleSnapshotAction)
	assert.Equal(t, "info", d.LogLevel)
	// uki.snapshot_strategy controls behaviour for ESP-mode UKI snapshot
	// entries (boot loader cmdline cannot override embedded cmdline).
	// Default is "skip" — see docs/USAGE.md#uki-snapshots-esp-mode-caveat.
	assert.Equal(t, "skip", d.UKI.SnapshotStrategy)
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{name: "defaults_pass", mutate: func(c *Config) {}},
		{
			name:    "invalid_writable_method",
			mutate:  func(c *Config) { c.Snapshot.WritableMethod = "bogus" },
			wantErr: `invalid snapshot.writable_method: "bogus"`,
		},
		{
			name:    "invalid_stale_action",
			mutate:  func(c *Config) { c.Kernel.StaleSnapshotAction = "bogus" },
			wantErr: `invalid kernel.stale_snapshot_action: "bogus"`,
		},
		{
			name:    "negative_max_depth",
			mutate:  func(c *Config) { c.Snapshot.MaxDepth = -1 },
			wantErr: "invalid snapshot.max_depth: -1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Defaults()
			tt.mutate(&c)
			err := c.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

func TestLoad_DefaultsOnly(t *testing.T) {
	cfg, err := Load("", nil)
	require.NoError(t, err)

	assertEqualConfig(t, Defaults(), *cfg)
}

func TestLoad_MissingFileIsNotAnError(t *testing.T) {
	cfg, err := Load("/nonexistent/path/does-not-exist.yaml", nil)
	require.NoError(t, err)
	assert.Equal(t, "info", cfg.LogLevel)
}

func TestLoad_MalformedFileFallsBackToDefaults(t *testing.T) {
	tmp := t.TempDir()
	bad := filepath.Join(tmp, "bad.yaml")
	require.NoError(t, os.WriteFile(bad, []byte("snapshot:\n  search_directories: [\n  unclosed\n"), 0644))

	cfg, err := Load(bad, nil)
	require.NoError(t, err)
	assertEqualConfig(t, Defaults(), *cfg)
}

// assertEqualConfig compares two Configs, treating nil and empty slices as
// equivalent (koanf's Unmarshal materializes unset slice fields as empty
// rather than nil, but both iterate zero times so behavior is identical).
func assertEqualConfig(t *testing.T, want, got Config) {
	t.Helper()
	if len(want.Kernel.BootImagePatterns) == 0 && len(got.Kernel.BootImagePatterns) == 0 {
		want.Kernel.BootImagePatterns = nil
		got.Kernel.BootImagePatterns = nil
	}
	assert.Equal(t, want, got)
}

func TestLoad_FileOverridesDefaults(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("log_level: debug\nsnapshot:\n  max_depth: 7\n"), 0644))

	cfg, err := Load(cfgPath, nil)
	require.NoError(t, err)
	assert.Equal(t, "debug", cfg.LogLevel)
	assert.Equal(t, 7, cfg.Snapshot.MaxDepth)
}

func TestLoad_UKISnapshotStrategy(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("uki:\n  snapshot_strategy: warn\n"), 0644))

	cfg, err := Load(cfgPath, nil)
	require.NoError(t, err)
	assert.Equal(t, "warn", cfg.UKI.SnapshotStrategy)
}

func TestLoad_EnvOverridesFile_TopLevelOnly(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("log_level: info\n"), 0644))

	t.Setenv("REFIND_BTRFS_SNAPSHOTS_LOG_LEVEL", "warn")
	t.Setenv("REFIND_BTRFS_SNAPSHOTS_SNAPSHOT_MAX_DEPTH", "9")

	cfg, err := Load(cfgPath, nil)
	require.NoError(t, err)

	assert.Equal(t, "warn", cfg.LogLevel, "top-level env var should propagate")
	assert.Equal(t, 3, cfg.Snapshot.MaxDepth,
		"nested env var (with underscore in key segment) should NOT propagate — matches legacy viper behavior")
}

func TestLoad_BootImagePatterns(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yaml")
	yaml := `
kernel:
  boot_image_patterns:
    - glob: "vmlinuz-*"
      role: "kernel"
      strip_prefix: "vmlinuz-"
    - glob: "initramfs-*.img"
      role: "initramfs"
      strip_prefix: "initramfs-"
      strip_suffix: ".img"
`
	require.NoError(t, os.WriteFile(cfgPath, []byte(yaml), 0644))

	cfg, err := Load(cfgPath, nil)
	require.NoError(t, err)
	require.Len(t, cfg.Kernel.BootImagePatterns, 2)
	assert.Equal(t, "vmlinuz-*", cfg.Kernel.BootImagePatterns[0].Glob)
	assert.Equal(t, "kernel", cfg.Kernel.BootImagePatterns[0].Role)
	assert.Equal(t, "initramfs-*.img", cfg.Kernel.BootImagePatterns[1].Glob)
	assert.Equal(t, ".img", cfg.Kernel.BootImagePatterns[1].StripSuffix)
}
