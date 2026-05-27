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

package main

import (
	"os"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/config"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/version"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var cfgFile string

// loadedCfg is the resolved configuration for the currently-executing command.
// Populated by rootCmd.PersistentPreRunE before any subcommand's RunE runs;
// subcommands and helpers read from it instead of holding their own copy.
// Tests bypass this by constructing Config literals and invoking the typed
// orchestration directly.
var loadedCfg *config.Config

var rootCmd = &cobra.Command{
	Use:   "refind-btrfs-snapshots",
	Short: "Generate rEFInd boot entries for btrfs snapshots",
	Long: `Generate rEFInd boot menu entries for btrfs snapshots with automatic
ESP detection, snapshot discovery, and configuration management.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig(cmd)
		if err != nil {
			return err
		}
		loadedCfg = cfg
		initLogging(cfg.LogLevel)
		return nil
	},
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	log.Logger = log.Output(zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: "15:04:05",
		NoColor:    false,
	})

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is /etc/refind-btrfs-snapshots.yaml)")
	rootCmd.PersistentFlags().String("log-level", "info", "log level (trace, debug, info, warn, error, fatal, panic)")
	rootCmd.PersistentFlags().Bool("local-time", false, "Display times in local time instead of UTC")
}

func initLogging(level string) {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	parsed, err := zerolog.ParseLevel(level)
	if err != nil || parsed == zerolog.NoLevel {
		parsed = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(parsed)

	log.Debug().
		Str("version", version.String()).
		Str("commit", version.Commit).
		Str("build_time", version.BuildTime).
		Str("log_level", level).
		Msg("Logger initialized")
}

