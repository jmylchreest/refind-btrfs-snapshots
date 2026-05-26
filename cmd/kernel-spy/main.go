// kernel-spy is a standalone tool that exercises the boot image discovery
// and inspection pipeline of refind-btrfs-snapshots without requiring any
// configuration. It autodetects the ESP, walks the usual locations for
// kernels, initramfs, microcode, BLS Type #1 entries, and UKIs, and prints
// what it found and how it identified each artefact.
//
// Spec references printed alongside detected layouts:
//
//	https://uapi-group.org/specifications/specs/boot_loader_specification/
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/bls"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/discovery"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
)

func main() {
	verbose := flag.Bool("v", false, "verbose logging (debug)")
	trace := flag.Bool("vv", false, "trace logging")
	espPath := flag.String("esp", "", "ESP mount point (default: autodetect)")
	flag.Usage = usage
	flag.Parse()

	zerolog.SetGlobalLevel(zerolog.WarnLevel)
	if *verbose {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}
	if *trace {
		zerolog.SetGlobalLevel(zerolog.TraceLevel)
	}
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: "15:04:05"})

	resolvedESP := *espPath
	if resolvedESP == "" {
		mp, err := discovery.ResolveESP(discovery.ESPOptions{AutoDetect: true})
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not autodetect ESP: %v\n", err)
		} else {
			resolvedESP = mp
		}
	}

	scanDirs := flag.Args()
	if len(scanDirs) == 0 {
		scanDirs = defaultScanDirs(resolvedESP)
	}
	scanDirs = dedupePaths(scanDirs)

	blsDirs := dedupePaths(defaultBLSDirs(resolvedESP))

	scanner := kernel.NewScanner(resolvedESP, nil)
	images, err := scanner.ScanDir(scanDirs...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: scan failed: %v\n", err)
		os.Exit(1)
	}
	scanner.InspectAll(images)

	entries := bls.ScanEntriesDir(blsDirs...)
	sets := scanner.BuildBootSets(images)
	attachBLSEntries(sets, entries)

	printReport(resolvedESP, scanDirs, blsDirs, images, entries, sets)
}

func usage() {
	fmt.Fprintf(os.Stderr, `kernel-spy — discover kernels, initramfs, microcode, BLS entries, and UKIs.

USAGE:
  kernel-spy [flags] [dir...]

If no directories are passed, kernel-spy scans the usual locations:
  /boot
  <esp>/EFI/Linux
  <esp>/loader/entries  (parsed as BLS Type #1)
  /boot/loader/entries  (parsed as BLS Type #1)

FLAGS:
`)
	flag.PrintDefaults()
}

func defaultScanDirs(espPath string) []string {
	dirs := []string{"/boot"}
	if espPath != "" {
		dirs = append(dirs,
			filepath.Join(espPath, "EFI", "Linux"),
			filepath.Join(espPath, "boot"),
			espPath,
		)
	}
	return dirs
}

func defaultBLSDirs(espPath string) []string {
	dirs := []string{"/boot/loader/entries"}
	if espPath != "" {
		dirs = append(dirs, filepath.Join(espPath, "loader", "entries"))
	}
	return dirs
}

// attachBLSEntries marks split-layout boot sets as LayoutBLS when a parsed
// BLS entry references the same kernel image, and stores the entry on the set.
// Matches are done by basename of the linux= path.
func attachBLSEntries(sets []*kernel.BootSet, entries []*bls.Entry) {
	if len(entries) == 0 {
		return
	}
	byKernelFile := make(map[string]*kernel.BootSet)
	for _, bs := range sets {
		if bs.Layout != kernel.LayoutSplit || bs.Kernel == nil {
			continue
		}
		byKernelFile[bs.Kernel.Filename] = bs
	}
	for _, e := range entries {
		if e.Linux == "" {
			continue
		}
		base := filepath.Base(e.Linux)
		if bs, ok := byKernelFile[base]; ok {
			bs.Layout = kernel.LayoutBLS
			bs.Entry = e
		}
	}
}

func printReport(espPath string, scanDirs, blsDirs []string, images []*kernel.BootImage, entries []*bls.Entry, sets []*kernel.BootSet) {
	fmt.Printf("== kernel-spy report ==\n")
	if espPath != "" {
		fmt.Printf("ESP:        %s\n", espPath)
	} else {
		fmt.Printf("ESP:        (not detected)\n")
	}
	fmt.Printf("scan dirs:  %s\n", strings.Join(scanDirs, "  "))
	fmt.Printf("bls dirs:   %s\n", strings.Join(blsDirs, "  "))
	fmt.Println()

	fmt.Printf("-- Discovered images (%d) --\n", len(images))
	byRole := groupImagesByRole(images)
	for _, role := range []kernel.ImageRole{kernel.RoleKernel, kernel.RoleUKI, kernel.RoleInitramfs, kernel.RoleFallbackInitramfs, kernel.RoleMicrocode} {
		if imgs, ok := byRole[role]; ok {
			fmt.Printf("  [%s] %d\n", role, len(imgs))
			for _, img := range imgs {
				printImage(img, "    ")
			}
		}
	}
	fmt.Println()

	fmt.Printf("-- BLS Type #1 entries (%d) --\n", len(entries))
	for _, e := range entries {
		fmt.Printf("  %s  (%s)\n", e.ID, e.Path)
		if e.Title != "" {
			fmt.Printf("    title:   %s\n", e.Title)
		}
		if e.Version != "" {
			fmt.Printf("    version: %s\n", e.Version)
		}
		if e.Linux != "" {
			fmt.Printf("    linux:   %s\n", e.Linux)
		}
		for _, i := range e.Initrd {
			fmt.Printf("    initrd:  %s\n", i)
		}
		if opts := e.OptionsString(); opts != "" {
			fmt.Printf("    options: %s\n", opts)
		}
	}
	fmt.Println()

	fmt.Printf("-- Assembled boot sets (%d) --\n", len(sets))
	for _, bs := range sets {
		fmt.Printf("  %s  [layout=%s]\n", bs.KernelName, bs.Layout)
		switch bs.Layout {
		case kernel.LayoutSplit, kernel.LayoutBLS:
			if bs.Kernel != nil {
				fmt.Printf("    kernel:    %s\n", bs.Kernel.AbsPath)
			}
			if bs.Initramfs != nil {
				fmt.Printf("    initrd:    %s\n", bs.Initramfs.AbsPath)
			}
			if bs.Fallback != nil {
				fmt.Printf("    fallback:  %s\n", bs.Fallback.AbsPath)
			}
			if bs.Layout == kernel.LayoutBLS && bs.Entry != nil {
				if e, ok := bs.Entry.(*bls.Entry); ok {
					fmt.Printf("    bls-entry: %s\n", e.Path)
				}
			}
		case kernel.LayoutUKI:
			if bs.UKI != nil {
				fmt.Printf("    uki:       %s\n", bs.UKI.AbsPath)
			}
		}
		for _, mc := range bs.Microcode {
			fmt.Printf("    microcode: %s\n", mc.AbsPath)
		}
		if v := bs.KernelVersion(); v != "" {
			fmt.Printf("    version:   %s\n", v)
		}
	}
}

func printImage(img *kernel.BootImage, indent string) {
	fmt.Printf("%s%s\n", indent, img.AbsPath)
	if img.Role == kernel.RoleMicrocode {
		fmt.Printf("%s  shared:      microcode\n", indent)
	} else {
		fmt.Printf("%s  kernel-name: %s\n", indent, img.KernelName)
	}
	if img.Inspected != nil {
		m := img.Inspected
		fmt.Printf("%s  format:      %s\n", indent, m.Format)
		if m.Version != "" {
			fmt.Printf("%s  version:     %s\n", indent, m.Version)
		}
		if m.BootProtocol != "" {
			fmt.Printf("%s  boot-proto:  %s\n", indent, m.BootProtocol)
		}
		if m.CompressFormat != "" {
			fmt.Printf("%s  compression: %s\n", indent, m.CompressFormat)
		}
		if m.Cmdline != "" {
			fmt.Printf("%s  cmdline:     %s\n", indent, m.Cmdline)
		}
		if m.OSReleasePrettyName != "" {
			fmt.Printf("%s  os-release:  %s\n", indent, m.OSReleasePrettyName)
		}
		if m.MicrocodeVendor != "" {
			fmt.Printf("%s  vendor:      %s\n", indent, m.MicrocodeVendor)
			fmt.Printf("%s  blocks:      %d\n", indent, m.MicrocodeBlockCount)
			if m.MicrocodeLatestDate != "" {
				fmt.Printf("%s  latest-date: %s\n", indent, m.MicrocodeLatestDate)
			}
			sigCount := len(m.MicrocodeProcessorSignatures)
			if sigCount > 0 {
				fmt.Printf("%s  cpu-sigs:    %d unique CPU identifiers\n", indent, countUnique(m.MicrocodeProcessorSignatures))
			}
		}
	} else {
		fmt.Printf("%s  (not inspected — filename-only)\n", indent)
	}
}

func countUnique(xs []uint32) int {
	seen := make(map[uint32]struct{}, len(xs))
	for _, x := range xs {
		seen[x] = struct{}{}
	}
	return len(seen)
}

func dedupePaths(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, p := range in {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		abs = filepath.Clean(abs)
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
	}
	return out
}

func groupImagesByRole(images []*kernel.BootImage) map[kernel.ImageRole][]*kernel.BootImage {
	m := make(map[kernel.ImageRole][]*kernel.BootImage)
	for _, img := range images {
		m[img.Role] = append(m[img.Role], img)
	}
	for role := range m {
		sort.Slice(m[role], func(i, j int) bool { return m[role][i].Filename < m[role][j].Filename })
	}
	return m
}
