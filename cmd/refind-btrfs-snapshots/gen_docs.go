//go:build gendocs

package main

import (
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:    "gen-docs <dir>",
		Short:  "Generate man pages into <dir> (build with -tags=gendocs)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doc.GenManTree(rootCmd, &doc.GenManHeader{
				Title:   "REFIND-BTRFS-SNAPSHOTS",
				Section: "1",
				Source:  "refind-btrfs-snapshots",
				Manual:  "refind-btrfs-snapshots Manual",
			}, args[0])
		},
	})
}
