//go:build gendocs

package main

import (
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/mangen"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:    "gen-docs <dir>",
		Short:  "Generate a combined man page into <dir> (build with -tags=gendocs)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return mangen.Write(rootCmd, args[0])
		},
	})
}
