// Copyright (c) 2024 John Mylchreest <jmylchreest@gmail.com>
//
// This file is part of refind-btrfs-snapshots.
//
// refind-btrfs-snapshots is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// refind-btrfs-snapshots is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with refind-btrfs-snapshots. If not, see <https://www.gnu.org/licenses/>.

package cmd

import (
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile   string
	logLevel  string
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "refind-btrfs-snapshots",
	Short: "Generate rEFInd boot entries for btrfs snapshots",
	Long: `Generate rEFInd boot menu entries for btrfs snapshots with automatic
ESP detection, snapshot discovery, and configuration management.`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		initLogging()
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Set up console logging immediately to ensure all output is formatted nicely
	log.Logger = log.Output(zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: "15:04:05",
		NoColor:    false,
	})

	cobra.OnInitialize(initConfig)

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is /etc/refind-btrfs-snapshots.yaml)")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level (trace, debug, info, warn, error, fatal, panic)")

	// Bind flags to viper
	viper.BindPFlag("log_level", rootCmd.PersistentFlags().Lookup("log-level"))
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Use a fixed default config file path
		viper.SetConfigFile("/etc/refind-btrfs-snapshots.yaml")
	}

	// Read in environment variables that match
	viper.SetEnvPrefix("REFIND_BTRFS_SNAPSHOTS")
	viper.AutomaticEnv()

	// Set default values
	setDefaults()

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		log.Debug().Str("config_file", viper.ConfigFileUsed()).Msg("Using config file")
	} else {
		// Check if viper found a config file or not
		if viper.ConfigFileUsed() == "" {
			log.Debug().Msg("No config file found, using defaults")
		} else {
			log.Debug().Err(err).Str("config_file", viper.ConfigFileUsed()).Msg("Config file found but failed to parse, using defaults")
		}
	}
}

func setDefaults() {
	// Snapshot configuration
	viper.SetDefault("snapshot.search_directories", []string{"/.snapshots"})
	viper.SetDefault("snapshot.max_depth", 3)
	viper.SetDefault("snapshot.selection_count", 0)
	viper.SetDefault("snapshot.destination_dir", "/.refind-btrfs-snapshots")
	viper.SetDefault("snapshot.writable_method", "toggle")

	// rEFInd configuration
	viper.SetDefault("refind.config_path", "/EFI/refind/refind.conf")

	// ESP configuration
	viper.SetDefault("esp.auto_detect", true)
	viper.SetDefault("esp.uuid", "")
	viper.SetDefault("esp.mount_point", "")

	// Behavior configuration
	viper.SetDefault("behavior.exit_on_snapshot_boot", true)
	viper.SetDefault("behavior.cleanup_old_snapshots", true)

	// Logging
	viper.SetDefault("log_level", "info")

	// Advanced configuration
	viper.SetDefault("advanced.naming.timestamp_format", "2006-01-02_15-04-05")
}

func initLogging() {
	// Configure zerolog
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	// Set log level
	level := viper.GetString("log_level")

	switch level {
	case "trace":
		zerolog.SetGlobalLevel(zerolog.TraceLevel)
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "info":
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case "warn":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	case "fatal":
		zerolog.SetGlobalLevel(zerolog.FatalLevel)
	case "panic":
		zerolog.SetGlobalLevel(zerolog.PanicLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	log.Debug().
		Str("version", getVersion()).
		Str("commit", Commit).
		Str("build_time", BuildTime).
		Str("log_level", level).
		Msg("Logger initialized")
}

func getVersion() string {
	if Version != "" {
		return Version
	}
	return "dev"
}
