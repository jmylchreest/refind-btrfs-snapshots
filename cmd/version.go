package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	Long: `Display version information including build details.

This command shows:
- Application version
- Git commit hash
- Build timestamp
- Go version used for compilation`,
	Run: runVersion,
}

func init() {
	rootCmd.AddCommand(versionCmd)
}

func runVersion(cmd *cobra.Command, args []string) {
	fmt.Printf("refind-btrfs-snapshots %s\n", getVersion())
	fmt.Printf("Commit: %s\n", Commit)
	fmt.Printf("Built: %s\n", BuildTime)
	fmt.Printf("Go version: %s\n", runtime.Version())
}
