package cmd

import (
	"fmt"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var listVolumesCmd = &cobra.Command{
	Use:   "volumes",
	Short: "List all btrfs filesystems/volumes",
	Long: `List all btrfs filesystems/volumes detected on the system.

Shows device path, mount point, and the best available identifier for each volume.
The IDENTIFIER column shows the preferred identifier value, and TYPE shows what
kind of identifier it is (UUID, PARTUUID, LABEL, PARTLABEL, or DEVICE).`,
	RunE: runListVolumes,
}

func runListVolumes(cmd *cobra.Command, args []string) error {
	log.Info().Msg("Listing btrfs volumes")

	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	btrfsManager := btrfs.NewManager(cfg.Snapshot.SearchDirectories, cfg.Snapshot.MaxDepth, cfg.Advanced.Naming.RwsnapFormat, cfg.Display.LocalTime.IsTrue())

	filesystems, err := btrfsManager.DetectBtrfsFilesystems()
	if err != nil {
		return fmt.Errorf("failed to detect btrfs filesystems: %w", err)
	}

	if len(filesystems) == 0 {
		fmt.Println("No btrfs filesystems found")
		return nil
	}

	jsonOutput, _ := cmd.Flags().GetBool("json")
	showAllIds, _ := cmd.Flags().GetBool("show-all-ids")

	if jsonOutput {
		return outputVolumesJSON(filesystems)
	}

	return outputVolumesTable(filesystems, showAllIds)
}

func filterFilesystems(filesystems []*btrfs.Filesystem, filter string) []*btrfs.Filesystem {
	var filtered []*btrfs.Filesystem

	if filter == "" {
		return filtered
	}

	for _, fs := range filesystems {
		if fs.UUID == filter ||
			fs.PartUUID == filter ||
			fs.Label == filter ||
			fs.PartLabel == filter ||
			fs.Device == filter ||
			fs.MountPoint == filter {
			filtered = append(filtered, fs)
		}
	}

	return filtered
}
