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
	assert.Equal(t, "trace", levelFromFlagThenConfig(cmd))
}

func TestLevelFromFlagThenConfig_ConfigFallback(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uki.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("log_level: warn\n"), 0o644))

	cmd := newTestCmd(t)
	require.NoError(t, cmd.ParseFlags([]string{"--config=" + cfgPath}))

	assert.Equal(t, "warn", levelFromFlagThenConfig(cmd))
}
