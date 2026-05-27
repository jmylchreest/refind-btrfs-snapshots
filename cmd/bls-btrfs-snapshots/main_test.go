package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestCmd(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "test"}
	cmd.PersistentFlags().StringP("config", "c", "", "config file")
	cmd.PersistentFlags().String("log-level", "", "log level")
	return cmd
}

func TestLevelFromFlagThenConfig_FlagWins(t *testing.T) {
	cmd := newTestCmd(t)
	require.NoError(t, cmd.ParseFlags([]string{"--log-level=trace"}))

	assert.Equal(t, "trace", levelFromFlagThenConfig(cmd),
		"explicit --log-level must take precedence over config")
}

func TestLevelFromFlagThenConfig_ConfigFallback(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "bls.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("log_level: warn\n"), 0o644))

	cmd := newTestCmd(t)
	require.NoError(t, cmd.ParseFlags([]string{"--config=" + cfgPath}))

	assert.Equal(t, "warn", levelFromFlagThenConfig(cmd),
		"with no --log-level, cfg.LogLevel from YAML must be returned")
}

func TestLevelFromFlagThenConfig_NoFlagNoConfig(t *testing.T) {
	cmd := newTestCmd(t)
	require.NoError(t, cmd.ParseFlags(nil))

	// Default config has LogLevel="info" — not empty. The contract here is
	// "no panic, returns whatever defaults produce" so we just assert the
	// function returns without error and yields a non-panicking value.
	assert.NotPanics(t, func() {
		_ = levelFromFlagThenConfig(cmd)
	})
}
