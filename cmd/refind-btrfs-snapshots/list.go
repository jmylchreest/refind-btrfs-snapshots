package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List btrfs volumes and snapshots",
	Long:  `List btrfs volumes and snapshots. Requires a subcommand (volumes or snapshots).`,
	RunE:  runListRoot,
}

func init() {
	rootCmd.AddCommand(listCmd)
	listCmd.AddCommand(listVolumesCmd)
	listCmd.AddCommand(listSnapshotsCmd)

	listVolumesCmd.Flags().Bool("json", false, "Output in JSON format")
	listVolumesCmd.Flags().Bool("show-all-ids", false, "Show all device identifiers (UUID, PARTUUID, LABEL, etc.)")

	listSnapshotsCmd.Flags().Bool("json", false, "Output in JSON format")
	listSnapshotsCmd.Flags().Bool("show-size", false, "Show snapshot sizes (slower)")
	listSnapshotsCmd.Flags().Bool("show-volume", false, "Show volume column (useful for multi-filesystem setups)")
	listSnapshotsCmd.Flags().String("volume", "", "Show snapshots only for specific volume UUID or device")
	listSnapshotsCmd.Flags().StringSlice("search-dirs", nil, "Override snapshot search directories")
}

func runListRoot(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("subcommand required. Use 'list volumes' or 'list snapshots'")
	}
	return fmt.Errorf("unknown subcommand '%s'. Available subcommands: volumes, snapshots", args[0])
}
