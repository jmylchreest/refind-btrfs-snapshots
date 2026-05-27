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
	"fmt"
	"os/user"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/diff"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/fstab"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/generator"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/runner"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var generateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate rEFInd boot entries for btrfs snapshots",
	Long: `Generate rEFInd boot configuration files for btrfs snapshots.

Automatically detects snapshots, updates fstab files, and creates boot entries.
Prefers refind_linux.conf updates but can generate include files when needed.`,
	RunE: runGenerate,
}

func init() {
	rootCmd.AddCommand(generateCmd)

	// Add command-specific flags
	generateCmd.Flags().String("config-path", "", "Path to rEFInd main config file")
	generateCmd.Flags().StringP("esp-path", "e", "", "Path to ESP mount point")
	generateCmd.Flags().IntP("count", "n", 0, "Number of snapshots to include (0 = all snapshots)")
	generateCmd.Flags().Bool("dry-run", false, "Show what would be done without making changes")
	generateCmd.Flags().Bool("force", false, "Force generation even if booted from snapshot")
	generateCmd.Flags().BoolP("generate-include", "g", false, "Force generation of refind-btrfs-snapshots.conf for inclusion into refind.conf")
	generateCmd.Flags().BoolP("yes", "y", false, "Automatically approve all changes without prompting")
}

func runGenerate(cmd *cobra.Command, args []string) error {
	log.Info().Msg("Starting rEFInd btrfs snapshot generation")

	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	if err := checkRootPrivileges(); err != nil {
		log.Warn().Err(err).Msg("Not running as root - some operations may fail")
	}

	espPath, err := detectESPPath(cfg)
	if err != nil {
		return err
	}

	kernelScanner := buildKernelScanner(espPath, cfg.Kernel.BootImagePatterns)
	allImages := scanBootImages(espPath, kernelScanner)
	var bootSets []*kernel.BootSet
	if len(allImages) > 0 {
		kernelScanner.InspectAll(allImages)
		bootSets = kernelScanner.BuildBootSets(allImages)
		log.Info().Int("boot_sets", len(bootSets)).Msg("Detected boot configurations on ESP")
	} else {
		log.Debug().Msg("No boot images found on ESP, staleness checking will be unavailable")
	}

	r := runner.New(cfg.DryRun.IsTrue())
	pipeline := &generator.Pipeline{
		Cfg:           cfg,
		Btrfs:         btrfs.NewManager(cfg.Snapshot.SearchDirectories, cfg.Snapshot.MaxDepth, cfg.Advanced.Naming.RwsnapFormat, cfg.Display.LocalTime.IsTrue()),
		Fstab:         fstab.NewManager(),
		Runner:        r,
		ESPPath:       espPath,
		KernelScanner: kernelScanner,
		BootSets:      bootSets,
	}

	plan, err := pipeline.Discover()
	if err != nil {
		return err
	}

	patch, summary, err := pipeline.BuildPatch(plan)
	if err != nil {
		return err
	}

	if len(patch.Files) == 0 {
		log.Info().Msg("No changes needed - configurations are up to date")
	} else if r.IsDryRun() {
		diff.ShowPatchWithPager(patch, !cfg.AutoApprove.IsTrue())
		log.Info().Msg("[DRY RUN] Would apply all changes shown above")
	} else {
		if !cfg.AutoApprove.IsTrue() {
			if !diff.ConfirmPatchChanges(patch, false) {
				log.Info().Msg("User declined changes - operation cancelled")
				return nil
			}
		} else {
			diff.ShowPatchWithPager(patch, false)
			log.Info().Msg("Auto-approving all changes")
		}
		if err := diff.Apply(patch, r); err != nil {
			return fmt.Errorf("failed to apply changes: %w", err)
		}
	}

	generator.LogSummary(summary, r.IsDryRun())
	if r.IsDryRun() {
		log.Info().Msg("Dry run completed - no changes made")
	} else {
		log.Info().Msg("Successfully generated rEFInd snapshot configurations")
	}
	return nil
}

// checkRootPrivileges checks if the current user has root privileges
func checkRootPrivileges() error {
	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}
	if currentUser.Uid != "0" {
		return fmt.Errorf("not running as root (UID: %s)", currentUser.Uid)
	}
	return nil
}
