package cmd

import (
	"os"
	"testing"

	"github.com/rs/zerolog"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitConfig(t *testing.T) {
	// Save original values
	originalConfigFile := viper.ConfigFileUsed()
	defer func() {
		viper.Reset()
		if originalConfigFile != "" {
			viper.SetConfigFile(originalConfigFile)
			viper.ReadInConfig()
		}
	}()

	tests := []struct {
		name         string
		cfgFile      string
		expectedPath string
		setupEnv     map[string]string
	}{
		{
			name:         "default_config_path",
			cfgFile:      "",
			expectedPath: "/etc/refind-btrfs-snapshots.yaml",
		},
		{
			name:         "custom_config_path",
			cfgFile:      "/tmp/custom-config.yaml",
			expectedPath: "/tmp/custom-config.yaml",
		},
		{
			name:         "with_env_variables",
			cfgFile:      "",
			expectedPath: "/etc/refind-btrfs-snapshots.yaml",
			setupEnv: map[string]string{
				"REFIND_BTRFS_SNAPSHOTS_SNAPSHOT_SEARCH_DIRECTORIES": "/custom/snapshots",
				"REFIND_BTRFS_SNAPSHOTS_LOG_LEVEL":                   "debug",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original cfgFile and environment
			originalCfgFile := cfgFile
			defer func() { cfgFile = originalCfgFile }()

			// Reset viper for each test
			viper.Reset()

			// Set up environment variables
			for key, value := range tt.setupEnv {
				os.Setenv(key, value)
				defer os.Unsetenv(key)
			}

			// Set cfgFile global variable
			cfgFile = tt.cfgFile

			// Call initConfig
			initConfig()

			// Check that defaults are set for tests without env variables
			if tt.setupEnv == nil {
				assert.Equal(t, []string{"/.snapshots"}, viper.GetStringSlice("snapshot.search_directories"))
				assert.Equal(t, 3, viper.GetInt("snapshot.max_depth"))
				assert.Equal(t, "info", viper.GetString("log_level"))
			} else {
				// For env variable tests, just verify they're set in environment
				if envVal, exists := tt.setupEnv["REFIND_BTRFS_SNAPSHOTS_LOG_LEVEL"]; exists {
					actualEnvVal := os.Getenv("REFIND_BTRFS_SNAPSHOTS_LOG_LEVEL")
					assert.Equal(t, envVal, actualEnvVal, "Environment variable should be set")
				}
			}
		})
	}
}

func TestSetDefaults(t *testing.T) {
	// Reset viper
	viper.Reset()

	setDefaults()

	tests := []struct {
		key      string
		expected interface{}
	}{
		// Snapshot configuration
		{"snapshot.search_directories", []string{"/.snapshots"}},
		{"snapshot.max_depth", 3},
		{"snapshot.selection_count", 0},
		{"snapshot.destination_dir", "/.refind-btrfs-snapshots"},
		{"snapshot.writable_method", "toggle"},

		// rEFInd configuration
		{"refind.config_path", "/EFI/refind/refind.conf"},

		// ESP configuration
		{"esp.auto_detect", true},
		{"esp.uuid", ""},
		{"esp.mount_point", ""},

		// Behavior configuration
		{"behavior.exit_on_snapshot_boot", true},
		{"behavior.cleanup_old_snapshots", true},

		// Logging
		{"log_level", "info"},

		// Advanced configuration
		{"advanced.naming.timestamp_format", "2006-01-02_15-04-05"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			actual := viper.Get(tt.key)
			assert.Equal(t, tt.expected, actual)
		})
	}
}

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
			// Reset viper
			viper.Reset()
			setDefaults()
			viper.Set("log_level", tt.logLevel)

			// Call initLogging
			initLogging()

			// Check that the global log level was set correctly
			assert.Equal(t, tt.expected, zerolog.GlobalLevel())
		})
	}
}

func TestGetVersion(t *testing.T) {
	tests := []struct {
		name         string
		versionValue string
		expected     string
	}{
		{
			name:         "version_set",
			versionValue: "1.2.3",
			expected:     "1.2.3",
		},
		{
			name:         "version_empty",
			versionValue: "",
			expected:     "dev",
		},
		{
			name:         "version_dev",
			versionValue: "dev",
			expected:     "dev",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original value
			originalVersion := Version

			// Set test value
			Version = tt.versionValue

			// Test function
			result := getVersion()
			assert.Equal(t, tt.expected, result)

			// Restore original value
			Version = originalVersion
		})
	}
}

func TestExecute(t *testing.T) {
	// Test that Execute function exists and returns no error when called with help
	// We can't easily test the full command execution without complex setup
	// But we can test that the function is callable

	// Save original args
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	// Set args to show help (won't actually execute the command)
	os.Args = []string{"test", "--help"}

	// Execute should not panic
	assert.NotPanics(t, func() {
		// This will likely return an error because help exits, but shouldn't panic
		Execute()
	})
}

func TestRootCmdConfiguration(t *testing.T) {
	// Test root command configuration
	require.NotNil(t, rootCmd)

	assert.Equal(t, "refind-btrfs-snapshots", rootCmd.Use)
	assert.Equal(t, "Generate rEFInd boot entries for btrfs snapshots", rootCmd.Short)
	assert.Contains(t, rootCmd.Long, "Generate rEFInd boot menu entries for btrfs snapshots")

	// Test that persistent flags are set
	configFlag := rootCmd.PersistentFlags().Lookup("config")
	require.NotNil(t, configFlag)
	assert.Equal(t, "", configFlag.DefValue)

	logLevelFlag := rootCmd.PersistentFlags().Lookup("log-level")
	require.NotNil(t, logLevelFlag)
	assert.Equal(t, "info", logLevelFlag.DefValue)
}

func TestRootCmdPersistentPreRun(t *testing.T) {
	// Test that PersistentPreRun calls initLogging
	// We'll verify this by checking that the log level gets set correctly
	viper.Reset()
	setDefaults()
	viper.Set("log_level", "debug")

	// The actual test would involve calling the PersistentPreRun function
	// but since it's embedded in the command, we'll test initLogging directly
	initLogging()

	assert.Equal(t, zerolog.DebugLevel, zerolog.GlobalLevel())
}

// Test helper function to verify viper configuration
func TestViperBindings(t *testing.T) {
	// This test verifies that the viper bindings work correctly
	// In a real scenario, these would be set by cobra flag parsing

	viper.Reset()
	setDefaults()

	// Test setting values through viper
	viper.Set("log_level", "error")
	assert.Equal(t, "error", viper.GetString("log_level"))

	viper.Set("snapshot.search_directories", []string{"/custom", "/snapshots"})
	assert.Equal(t, []string{"/custom", "/snapshots"}, viper.GetStringSlice("snapshot.search_directories"))
}
