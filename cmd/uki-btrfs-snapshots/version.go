package main

import (
	"fmt"
	"runtime"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/version"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("uki-btrfs-snapshots %s\n", version.String())
		fmt.Printf("Commit: %s\n", version.Commit)
		fmt.Printf("Built: %s\n", version.BuildTime)
		fmt.Printf("Go version: %s\n", runtime.Version())
	},
}
