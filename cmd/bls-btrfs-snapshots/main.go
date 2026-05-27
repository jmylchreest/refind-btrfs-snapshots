// bls-btrfs-snapshots writes Boot Loader Specification Type #1 entries for
// btrfs snapshots, consumed by systemd-boot and BLS-aware GRUB builds.
// rEFInd users should use refind-btrfs-snapshots instead — it generates
// rEFInd-native config and handles btrfs-mode boot via rEFInd's btrfs driver,
// which systemd-boot lacks.
//
// Spec: https://uapi-group.org/specifications/specs/boot_loader_specification/
package main

import (
	"fmt"
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "bls-btrfs-snapshots",
	Short: "Write BLS Type #1 entries for btrfs snapshots",
	Long: `Discover btrfs snapshots and emit BLS Type #1 entries under
<esp>/loader/entries/, consumed by systemd-boot or BLS-aware GRUB.

Only ESP-mode snapshots are emitted: systemd-boot cannot traverse btrfs
subvolumes to reach kernels inside a snapshot. Use refind-btrfs-snapshots
for btrfs-mode coverage via rEFInd's btrfs driver.`,
	PersistentPreRunE: setupLogging,
}

func init() {
	rootCmd.PersistentFlags().StringP("config", "c", "", "Path to config file (default: /etc/bls-btrfs-snapshots.yaml)")
	rootCmd.PersistentFlags().String("log-level", "", "Log verbosity: trace, debug, info, warn, error")
}

func setupLogging(cmd *cobra.Command, args []string) error {
	level, _ := cmd.Flags().GetString("log-level")
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if level != "" {
		parsed, err := zerolog.ParseLevel(level)
		if err != nil {
			return fmt.Errorf("invalid --log-level %q: %w", level, err)
		}
		zerolog.SetGlobalLevel(parsed)
	}
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: "15:04:05"})
	return nil
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
