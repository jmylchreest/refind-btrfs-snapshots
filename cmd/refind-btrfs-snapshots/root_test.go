package main

import (
	"os"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitLogging(t *testing.T) {
	tests := []struct {
		name     string
		logLevel string
		expected zerolog.Level
	}{
		{"trace_level", "trace", zerolog.TraceLevel},
		{"debug_level", "debug", zerolog.DebugLevel},
		{"info_level", "info", zerolog.InfoLevel},
		{"warn_level", "warn", zerolog.WarnLevel},
		{"error_level", "error", zerolog.ErrorLevel},
		{"fatal_level", "fatal", zerolog.FatalLevel},
		{"panic_level", "panic", zerolog.PanicLevel},
		{"invalid_level", "invalid", zerolog.InfoLevel},
		{"empty_level", "", zerolog.InfoLevel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initLogging(tt.logLevel)
			assert.Equal(t, tt.expected, zerolog.GlobalLevel())
		})
	}
}

func TestExecute(t *testing.T) {
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()
	os.Args = []string{"test", "--help"}

	assert.NotPanics(t, func() {
		_ = Execute()
	})
}

func TestRootCmdConfiguration(t *testing.T) {
	require.NotNil(t, rootCmd)

	assert.Equal(t, "refind-btrfs-snapshots", rootCmd.Use)
	assert.Equal(t, "Generate rEFInd boot entries for btrfs snapshots", rootCmd.Short)
	assert.Contains(t, rootCmd.Long, "Generate rEFInd boot menu entries for btrfs snapshots")

	configFlag := rootCmd.PersistentFlags().Lookup("config")
	require.NotNil(t, configFlag)
	assert.Equal(t, "", configFlag.DefValue)

	logLevelFlag := rootCmd.PersistentFlags().Lookup("log-level")
	require.NotNil(t, logLevelFlag)
	assert.Equal(t, "info", logLevelFlag.DefValue)

	localTimeFlag := rootCmd.PersistentFlags().Lookup("local-time")
	require.NotNil(t, localTimeFlag)
	assert.Equal(t, "false", localTimeFlag.DefValue)
}
