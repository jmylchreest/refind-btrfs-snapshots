package cliconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.PersistentFlags().StringP("config", "c", "", "config file")
	cmd.PersistentFlags().String("log-level", "info", "log level")
	cmd.PersistentFlags().Bool("dry-run", false, "dry run")
	cmd.PersistentFlags().Int("count", 0, "count")
	return cmd
}

func TestLoad_DefaultPathUsedWhenNoFlag(t *testing.T) {
	cmd := newCmd()
	require.NoError(t, cmd.ParseFlags(nil))

	cfg, err := Load(cmd, "/nonexistent/path.yaml", nil)
	require.NoError(t, err)
	assert.Equal(t, "info", cfg.LogLevel)
}

func TestLoad_ConfigFlagOverridesDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.yaml")
	require.NoError(t, os.WriteFile(path, []byte("log_level: debug\n"), 0o644))

	cmd := newCmd()
	require.NoError(t, cmd.ParseFlags([]string{"--config=" + path}))

	cfg, err := Load(cmd, "/should/not/be/used.yaml", nil)
	require.NoError(t, err)
	assert.Equal(t, "debug", cfg.LogLevel)
}

func TestLoad_FlagOverrideAppliedOnlyWhenSet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	require.NoError(t, os.WriteFile(path, []byte("log_level: warn\n"), 0o644))

	flagToKey := map[string]string{"log-level": "log_level"}

	cmd := newCmd()
	require.NoError(t, cmd.ParseFlags([]string{"--config=" + path}))
	cfg, err := Load(cmd, "", flagToKey)
	require.NoError(t, err)
	assert.Equal(t, "warn", cfg.LogLevel, "unset flag must not override file value")

	cmd = newCmd()
	require.NoError(t, cmd.ParseFlags([]string{"--config=" + path, "--log-level=error"}))
	cfg, err = Load(cmd, "", flagToKey)
	require.NoError(t, err)
	assert.Equal(t, "error", cfg.LogLevel, "explicitly-set flag must override file value")
}

func TestLoad_FlagNotInMapIgnored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	require.NoError(t, os.WriteFile(path, []byte("log_level: warn\n"), 0o644))

	cmd := newCmd()
	require.NoError(t, cmd.ParseFlags([]string{"--config=" + path, "--log-level=error"}))

	cfg, err := Load(cmd, "", map[string]string{})
	require.NoError(t, err)
	assert.Equal(t, "warn", cfg.LogLevel, "flag absent from map must not override")
}

func TestFlagValueAs_TypeCoercion(t *testing.T) {
	cmd := newCmd()
	require.NoError(t, cmd.ParseFlags([]string{"--dry-run", "--count=42", "--log-level=debug"}))

	assert.Equal(t, true, flagValueAs(cmd.Flag("dry-run")))
	assert.Equal(t, 42, flagValueAs(cmd.Flag("count")))
	assert.Equal(t, "debug", flagValueAs(cmd.Flag("log-level")))
}

func TestFlagOverrides_ReturnsNilWhenEmpty(t *testing.T) {
	cmd := newCmd()
	require.NoError(t, cmd.ParseFlags(nil))

	got := flagOverrides(cmd.Flags(), map[string]string{"dry-run": "dry_run"})
	assert.Nil(t, got, "no flags set → nil so config.Load skips the override layer")
}
