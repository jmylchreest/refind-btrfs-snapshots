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
	"strings"
	"text/tabwriter"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
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

	// Output
	jsonOutput, _ := cmd.Flags().GetBool("json")
	showImages, _ := cmd.Flags().GetBool("show-images")

	if jsonOutput {
		return outputBootsetsJSON(bootSets, allImages, showImages)
	}

	return outputBootsetsTable(bootSets, allImages, showImages)
}

func outputBootsetsJSON(bootSets []*kernel.BootSet, allImages []*kernel.BootImage, showImages bool) error {
	type output struct {
		BootSets []bootSetJSON   `json:"boot_sets"`
		Images   []bootImageJSON `json:"images,omitempty"`
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

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(out)
}

func outputBootsetsTable(bootSets []*kernel.BootSet, allImages []*kernel.BootImage, showImages bool) error {
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

	return nil
}
