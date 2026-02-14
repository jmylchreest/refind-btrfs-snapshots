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

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var listBootsetsCmd = &cobra.Command{
	Use:   "bootsets",
	Short: "List detected boot image sets on the ESP",
	Long: `Scan the EFI System Partition for boot images and display the detected boot sets.

Each boot set groups related boot images by kernel name (e.g., "linux", "linux-lts").
A boot set typically contains a kernel, its matching initramfs, an optional fallback
initramfs, and any shared microcode images.

The command also inspects kernel binaries to extract version strings from their
bzImage headers when possible.`,
	RunE: runListBootsets,
}

func init() {
	listCmd.AddCommand(listBootsetsCmd)

	listBootsetsCmd.Flags().Bool("json", false, "Output in JSON format")
	listBootsetsCmd.Flags().Bool("show-images", false, "Show individual boot images in addition to boot sets")
}

// bootSetJSON is the JSON-serializable representation of a boot set.
type bootSetJSON struct {
	KernelName    string          `json:"kernel_name"`
	KernelVersion string          `json:"kernel_version,omitempty"`
	Kernel        *bootImageJSON  `json:"kernel,omitempty"`
	Initramfs     *bootImageJSON  `json:"initramfs,omitempty"`
	Fallback      *bootImageJSON  `json:"fallback,omitempty"`
	Microcode     []bootImageJSON `json:"microcode,omitempty"`
}

// bootImageJSON is the JSON-serializable representation of a boot image.
type bootImageJSON struct {
	Path           string `json:"path"`
	Filename       string `json:"filename"`
	Role           string `json:"role"`
	KernelName     string `json:"kernel_name,omitempty"`
	Version        string `json:"version,omitempty"`
	VersionFull    string `json:"version_full,omitempty"`
	BootProtocol   string `json:"boot_protocol,omitempty"`
	CompressFormat string `json:"compress_format,omitempty"`
}

func toBootImageJSON(img *kernel.BootImage) *bootImageJSON {
	if img == nil {
		return nil
	}
	j := &bootImageJSON{
		Path:       img.Path,
		Filename:   img.Filename,
		Role:       string(img.Role),
		KernelName: img.KernelName,
	}
	if img.Inspected != nil {
		j.Version = img.Inspected.Version
		j.VersionFull = img.Inspected.VersionFull
		j.BootProtocol = img.Inspected.BootProtocol
		j.CompressFormat = img.Inspected.CompressFormat
	}
	return j
}

func runListBootsets(cmd *cobra.Command, args []string) error {
	// Detect ESP
	espPath, err := detectESPPath()
	if err != nil {
		return err
	}

	// Build scanner and discover images
	scanner := buildKernelScanner(espPath)
	allImages := scanBootImages(espPath, scanner)

	if len(allImages) == 0 {
		fmt.Println("No boot images found on ESP")
		return nil
	}

	// Inspect kernel binaries for version info
	scanner.InspectAll(allImages)

	// Build boot sets
	bootSets := scanner.BuildBootSets(allImages)

	log.Info().
		Int("images", len(allImages)).
		Int("boot_sets", len(bootSets)).
		Str("esp", espPath).
		Msg("Boot image scan complete")

	// Discover snapshots for the compatibility matrix
	searchDirs := viper.GetStringSlice("snapshot.search_directories")
	maxDepth := viper.GetInt("snapshot.max_depth")
	btrfsManager := btrfs.NewManager(searchDirs, maxDepth)

	filesystems, err := btrfsManager.DetectBtrfsFilesystems()
	if err != nil {
		log.Warn().Err(err).Msg("Could not detect btrfs filesystems for compatibility matrix")
	}

	var snapshots []*btrfs.Snapshot
	if len(filesystems) > 0 {
		seen := make(map[string]bool)
		for _, fs := range filesystems {
			found, err := btrfsManager.FindSnapshots(fs)
			if err != nil {
				log.Warn().Err(err).Str("fs", fs.GetBestIdentifier()).Msg("Failed to find snapshots")
				continue
			}
			for _, s := range found {
				if !seen[s.Path] {
					seen[s.Path] = true
					snapshots = append(snapshots, s)
				}
			}
		}
	}

	// Sort snapshots newest first
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].SnapshotTime.After(snapshots[j].SnapshotTime)
	})

	// Apply selection count if configured
	if count := viper.GetInt("snapshot.selection_count"); count > 0 && len(snapshots) > count {
		snapshots = snapshots[:count]
	}

	// Compute compatibility matrix
	var matrix []snapshotCompatibility
	if len(snapshots) > 0 && len(bootSets) > 0 {
		staleAction := kernel.ParseStaleAction(viper.GetString("kernel.stale_snapshot_action"))
		checker := kernel.NewChecker(staleAction)

		for _, snap := range snapshots {
			compat := snapshotCompatibility{
				Snapshot: snap,
				Modules:  kernel.GetSnapshotModuleVersions(snap.FilesystemPath),
			}
			for _, bs := range bootSets {
				result := checker.CheckSnapshot(snap.FilesystemPath, bs)
				compat.Results = append(compat.Results, result)
			}
			matrix = append(matrix, compat)
		}
	}

	// Output
	jsonOutput, _ := cmd.Flags().GetBool("json")
	showImages, _ := cmd.Flags().GetBool("show-images")
	useLocalTime := viper.GetBool("display.local_time")

	if jsonOutput {
		return outputBootsetsJSON(bootSets, allImages, showImages, matrix, useLocalTime)
	}

	return outputBootsetsTable(bootSets, allImages, showImages, matrix, useLocalTime)
}

// snapshotCompatibility holds one snapshot's staleness results against all boot sets.
type snapshotCompatibility struct {
	Snapshot *btrfs.Snapshot
	Modules  []string
	Results  []*kernel.StalenessResult
}

// compatibilityJSON is the JSON representation of one snapshot's compatibility.
type compatibilityJSON struct {
	SnapshotPath string            `json:"snapshot_path"`
	SnapshotTime string            `json:"snapshot_time"`
	Modules      []string          `json:"modules"`
	BootSets     []compatEntryJSON `json:"boot_sets"`
}

type compatEntryJSON struct {
	KernelName string `json:"kernel_name"`
	Status     string `json:"status"` // fresh, stale, unknown
	Method     string `json:"method,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Action     string `json:"action,omitempty"`
}

func outputBootsetsJSON(bootSets []*kernel.BootSet, allImages []*kernel.BootImage, showImages bool, matrix []snapshotCompatibility, useLocalTime bool) error {
	type output struct {
		BootSets      []bootSetJSON       `json:"boot_sets"`
		Images        []bootImageJSON     `json:"images,omitempty"`
		Compatibility []compatibilityJSON `json:"compatibility,omitempty"`
	}

	out := output{}

	for _, bs := range bootSets {
		bsj := bootSetJSON{
			KernelName:    bs.KernelName,
			KernelVersion: bs.KernelVersion(),
			Kernel:        toBootImageJSON(bs.Kernel),
			Initramfs:     toBootImageJSON(bs.Initramfs),
			Fallback:      toBootImageJSON(bs.Fallback),
		}
		for _, mc := range bs.Microcode {
			bsj.Microcode = append(bsj.Microcode, *toBootImageJSON(mc))
		}
		out.BootSets = append(out.BootSets, bsj)
	}

	if showImages {
		for _, img := range allImages {
			out.Images = append(out.Images, *toBootImageJSON(img))
		}
	}

	for _, row := range matrix {
		cj := compatibilityJSON{
			SnapshotPath: row.Snapshot.Path,
			SnapshotTime: btrfs.FormatSnapshotTimeForDisplay(row.Snapshot.SnapshotTime, useLocalTime),
			Modules:      row.Modules,
		}
		for i, result := range row.Results {
			status := "fresh"
			reason := ""
			if result.IsStale {
				status = "stale"
				reason = string(result.Reason)
			} else if result.Method == kernel.MatchAssumedFresh {
				status = "unknown"
			}
			entry := compatEntryJSON{
				KernelName: bootSets[i].KernelName,
				Status:     status,
				Method:     string(result.Method),
				Reason:     reason,
			}
			if result.IsStale {
				entry.Action = string(result.Action)
			}
			cj.BootSets = append(cj.BootSets, entry)
		}
		out.Compatibility = append(out.Compatibility, cj)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(out)
}

func outputBootsetsTable(bootSets []*kernel.BootSet, allImages []*kernel.BootImage, showImages bool, matrix []snapshotCompatibility, useLocalTime bool) error {
	if showImages {
		fmt.Printf("Boot Images (%d found)\n", len(allImages))
		fmt.Println(strings.Repeat("─", 80))

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ROLE\tFILENAME\tKERNEL NAME\tVERSION/INFO")
		fmt.Fprintln(w, "────\t────────\t───────────\t────────────")

		for _, img := range allImages {
			info := ""
			if img.Inspected != nil {
				if img.Inspected.Version != "" {
					info = img.Inspected.Version
				} else if img.Inspected.CompressFormat != "" {
					info = img.Inspected.CompressFormat
				}
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				img.Role, img.Filename, img.KernelName, info)
		}
		w.Flush()
		fmt.Println()
	}

	fmt.Printf("Boot Sets (%d detected)\n", len(bootSets))
	fmt.Println(strings.Repeat("─", 80))

	for i, bs := range bootSets {
		if i > 0 {
			fmt.Println()
		}

		version := bs.KernelVersion()
		if version == "" {
			version = "(not inspected)"
		}

		fmt.Printf("  %s\n", bs.DisplayName())
		fmt.Printf("    Kernel name:    %s\n", bs.KernelName)
		fmt.Printf("    Kernel version: %s\n", version)

		if bs.Kernel != nil {
			fmt.Printf("    Kernel:         %s\n", bs.Kernel.Path)
		} else {
			fmt.Printf("    Kernel:         (not found)\n")
		}

		if bs.Initramfs != nil {
			info := bs.Initramfs.Path
			if bs.Initramfs.Inspected != nil && bs.Initramfs.Inspected.CompressFormat != "" {
				info += fmt.Sprintf(" [%s]", bs.Initramfs.Inspected.CompressFormat)
			}
			fmt.Printf("    Initramfs:      %s\n", info)
		} else {
			fmt.Printf("    Initramfs:      (not found)\n")
		}

		if bs.Fallback != nil {
			info := bs.Fallback.Path
			if bs.Fallback.Inspected != nil && bs.Fallback.Inspected.CompressFormat != "" {
				info += fmt.Sprintf(" [%s]", bs.Fallback.Inspected.CompressFormat)
			}
			fmt.Printf("    Fallback:       %s\n", info)
		}

		if len(bs.Microcode) > 0 {
			var mcPaths []string
			for _, mc := range bs.Microcode {
				mcPaths = append(mcPaths, mc.Path)
			}
			fmt.Printf("    Microcode:      %s\n", strings.Join(mcPaths, ", "))
		}
	}

	// Compatibility matrix
	if len(matrix) > 0 {
		fmt.Println()
		fmt.Printf("Snapshot Compatibility (%d snapshots)\n", len(matrix))
		fmt.Println(strings.Repeat("─", 80))

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

		// Header row
		headers := []string{"SNAPSHOT TIME", "SNAPSHOT PATH"}
		separators := []string{"─────────────", "─────────────"}
		for _, bs := range bootSets {
			headers = append(headers, strings.ToUpper(bs.KernelName))
			separators = append(separators, strings.Repeat("─", max(len(bs.KernelName), 7)))
		}
		headers = append(headers, "MODULES")
		separators = append(separators, "───────")

		fmt.Fprintln(w, strings.Join(headers, "\t"))
		fmt.Fprintln(w, strings.Join(separators, "\t"))

		for _, row := range matrix {
			timeStr := btrfs.FormatSnapshotTimeForDisplay(row.Snapshot.SnapshotTime, useLocalTime)
			cols := []string{timeStr, row.Snapshot.Path}

			for _, result := range row.Results {
				if result.IsStale {
					cols = append(cols, "stale")
				} else if result.Method == kernel.MatchAssumedFresh {
					cols = append(cols, "unknown")
				} else {
					cols = append(cols, "fresh")
				}
			}

			modules := strings.Join(row.Modules, ", ")
			if modules == "" {
				modules = "(none)"
			}
			cols = append(cols, modules)

			fmt.Fprintln(w, strings.Join(cols, "\t"))
		}

		w.Flush()
	} else if len(bootSets) > 0 {
		fmt.Println()
		fmt.Println("No snapshots found for compatibility check")
	}

	return nil
}
