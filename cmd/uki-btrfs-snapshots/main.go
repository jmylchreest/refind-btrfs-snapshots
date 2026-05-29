// uki-btrfs-snapshots clones source Unified Kernel Images per btrfs snapshot
// with a per-snapshot kernel command line embedded. This is the only way to
// make snapshots bootable on systems whose boot path is UKI-only: the
// cmdline lives inside the signed .cmdline PE section of the UKI itself, so
// neither refind-btrfs-snapshots nor bls-btrfs-snapshots can influence what
// the kernel actually receives — they emit external bootloader config that
// systemd-stub ignores.
//
// Spec: https://uapi-group.org/specifications/specs/unified_kernel_image/
package main

import (
	"fmt"
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "uki-btrfs-snapshots",
	Short: "Clone UKIs per btrfs snapshot with snapshot-rooted cmdlines",
	Long: `Discover btrfs snapshots and write one cloned UKI per (snapshot ×
source UKI) under uki.output_dir, each carrying a .cmdline section that
points at the snapshot's subvolume.

Use this only if your boot path is UKI-only (systemd-boot or direct
EFI Boot Manager entries pointing at <esp>/EFI/Linux/*.efi). Systems
booting via rEFInd or via BLS Type #1 entries don't need it — see
refind-btrfs-snapshots or bls-btrfs-snapshots instead.`,
	PersistentPreRunE: setupLogging,
}

func init() {
	rootCmd.PersistentFlags().StringP("config", "c", "", "Path to config file (default: /etc/uki-btrfs-snapshots.yaml)")
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

// levelFromFlagThenConfig: --log-level wins; else fall back to YAML
// cfg.LogLevel. Config-load failures yield "" so runGenerate surfaces the
// real error.
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
