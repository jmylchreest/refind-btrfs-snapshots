// peseal signs PE32+ binaries (UKIs, EFI loaders, anything Authenticode-
// targeted) using a user-supplied key + certificate. Idempotent: re-running
// against an already-signed file is a no-op when the existing signature
// matches the configured cert.
//
// peseal is independent of the rest of this repo's binaries. The
// uki-btrfs-snapshots binary can be configured to invoke peseal after
// writing each clone (uki.sign_command), but peseal itself has no
// awareness of snapshots or UKI semantics — it signs PE files.
package main

import (
	"fmt"
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "peseal",
	Short: "Sign and verify PE32+ binaries (Authenticode)",
	Long: `Sign, verify, and inspect PE32+ Authenticode signatures on
EFI binaries — Unified Kernel Images, bootloaders, loaders. Pure Go
under the hood (wraps github.com/foxboron/go-uefi/authenticode);
no objcopy, sbsign, pesign, or ukify dependency at runtime.`,
	PersistentPreRunE: setupLogging,
}

func init() {
	rootCmd.PersistentFlags().StringP("config", "c", "", "Path to config file (default: /etc/peseal.yaml)")
	rootCmd.PersistentFlags().String("log-level", "", "Log verbosity: trace, debug, info, warn, error")
}

func setupLogging(cmd *cobra.Command, args []string) error {
	level := levelFromFlagThenConfig(cmd)
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if level != "" {
		parsed, err := zerolog.ParseLevel(level)
		if err != nil {
			return fmt.Errorf("invalid log level %q: %w", level, err)
		}
		zerolog.SetGlobalLevel(parsed)
	}
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: "15:04:05"})
	return nil
}

func levelFromFlagThenConfig(cmd *cobra.Command) string {
	if cmd.Flags().Changed("log-level") {
		v, _ := cmd.Flags().GetString("log-level")
		return v
	}
	cfg, err := loadConfig(cmd)
	if err != nil {
		return ""
	}
	return cfg.LogLevel
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
