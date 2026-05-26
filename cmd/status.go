// Copyright (c) 2024 John Mylchreest <jmylchreest@gmail.com>
//
// This file is part of refind-btrfs-snapshots.
//
// refind-btrfs-snapshots is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/fstab"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show snapshot bootability against detected ESP boot sets",
	Long: `Show the bootability status of every snapshot against every detected boot set.

This is the diagnostic command for answering "if my /boot breaks, which of my
snapshots are real fallbacks?" It joins the snapshot inventory (from btrfs) with
the boot image inventory (from the ESP) and renders a compatibility matrix.

Each row is a snapshot; each kernel column shows whether the snapshot's modules
match that kernel's version (fresh) or not (stale). Snapshots in btrfs-mode
embed their own kernel and are marked n/a — staleness is impossible for them.`,
	RunE: runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
	statusCmd.Flags().Bool("json", false, "Output in JSON format")
	statusCmd.Flags().Bool("unbootable-only", false, "Show only snapshots that are stale or unbootable against all boot sets")
}

func runStatus(cmd *cobra.Command, args []string) error {
	cfg := loadedCfg

	bootSets := detectBootSets(cfg)
	if len(bootSets) == 0 {
		return fmt.Errorf("no boot sets detected on ESP — cannot compute compatibility")
	}

	snapshots, btrfsManager := discoverSnapshots(cfg, nil)
	if len(snapshots) == 0 {
		fmt.Println("No snapshots found")
		return nil
	}

	rootFS, _ := btrfsManager.GetRootFilesystem()
	fstabMgr := fstab.NewManager()
	staleAction := kernel.ParseStaleAction(cfg.Kernel.StaleSnapshotAction)
	checker := kernel.NewChecker(staleAction)
	var planner *kernel.Planner
	if rootFS != nil {
		ukiStrategy := kernel.ParseUKIStrategy(cfg.UKI.SnapshotStrategy)
		planner = kernel.NewPlanner(fstabMgr, checker, bootSets, rootFS, ukiStrategy)
	}

	matrix := buildCompatibilityMatrix(snapshots, bootSets, planner, checker)

	unbootableOnly, _ := cmd.Flags().GetBool("unbootable-only")
	if unbootableOnly {
		matrix = filterUnbootable(matrix)
	}

	jsonOutput, _ := cmd.Flags().GetBool("json")
	if jsonOutput {
		return outputStatusJSON(bootSets, matrix, cfg.Display.LocalTime.IsTrue())
	}
	return outputStatusTable(bootSets, matrix, cfg.Display.LocalTime.IsTrue())
}

// snapshotCompatibility holds one snapshot's staleness results against all boot sets.
type snapshotCompatibility struct {
	Snapshot *btrfs.Snapshot
	BootMode kernel.BootMode
	Modules  []string
	Results  []*kernel.StalenessResult
}

func buildCompatibilityMatrix(snapshots []*btrfs.Snapshot, bootSets []*kernel.BootSet, planner *kernel.Planner, checker *kernel.Checker) []snapshotCompatibility {
	var matrix []snapshotCompatibility
	for _, snap := range snapshots {
		bootMode := kernel.BootModeESP
		if planner != nil {
			plans := planner.Plan([]*btrfs.Snapshot{snap})
			if len(plans) > 0 {
				bootMode = plans[0].Mode
			}
		}

		row := snapshotCompatibility{
			Snapshot: snap,
			BootMode: bootMode,
			Modules:  kernel.GetSnapshotModuleVersions(snap.FilesystemPath),
		}

		if bootMode == kernel.BootModeBtrfs {
			// btrfs-mode: staleness not applicable; emit placeholder
			// results so the columns line up.
			for range bootSets {
				row.Results = append(row.Results, &kernel.StalenessResult{
					IsStale: false,
					Method:  "in_snapshot",
				})
			}
		} else {
			for _, bs := range bootSets {
				row.Results = append(row.Results, checker.CheckSnapshot(snap.FilesystemPath, bs))
			}
		}

		matrix = append(matrix, row)
	}
	return matrix
}

// filterUnbootable returns only rows where every result is stale.
// btrfs-mode rows are always considered bootable (their kernel ships with them).
func filterUnbootable(matrix []snapshotCompatibility) []snapshotCompatibility {
	var out []snapshotCompatibility
	for _, row := range matrix {
		if row.BootMode == kernel.BootModeBtrfs {
			continue
		}
		allStale := len(row.Results) > 0
		for _, r := range row.Results {
			if r == nil || !r.IsStale {
				allStale = false
				break
			}
		}
		if allStale {
			out = append(out, row)
		}
	}
	return out
}

type compatibilityJSON struct {
	SnapshotPath string            `json:"snapshot_path"`
	SnapshotTime string            `json:"snapshot_time"`
	BootMode     string            `json:"boot_mode"`
	Modules      []string          `json:"modules"`
	BootSets     []compatEntryJSON `json:"boot_sets"`
}

type compatEntryJSON struct {
	KernelName string `json:"kernel_name"`
	Status     string `json:"status"`
	Method     string `json:"method,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Action     string `json:"action,omitempty"`
}

func outputStatusJSON(bootSets []*kernel.BootSet, matrix []snapshotCompatibility, useLocalTime bool) error {
	type output struct {
		Compatibility []compatibilityJSON `json:"compatibility"`
	}
	out := output{}

	for _, row := range matrix {
		cj := compatibilityJSON{
			SnapshotPath: row.Snapshot.Path,
			SnapshotTime: btrfs.FormatSnapshotTimeForDisplay(row.Snapshot.SnapshotTime, useLocalTime),
			BootMode:     string(row.BootMode),
			Modules:      row.Modules,
		}
		for i, result := range row.Results {
			if i >= len(bootSets) {
				log.Warn().Int("index", i).Int("boot_sets", len(bootSets)).Msg("Result index out of bounds")
				break
			}
			if row.BootMode == kernel.BootModeBtrfs {
				cj.BootSets = append(cj.BootSets, compatEntryJSON{
					KernelName: bootSets[i].KernelName,
					Status:     "n/a",
					Method:     "in_snapshot",
				})
				continue
			}
			entry := compatEntryJSON{
				KernelName: bootSets[i].KernelName,
				Status:     result.StatusString(),
				Method:     string(result.Method),
				Reason:     string(result.Reason),
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

func outputStatusTable(bootSets []*kernel.BootSet, matrix []snapshotCompatibility, useLocalTime bool) error {
	if len(matrix) == 0 {
		fmt.Println("No snapshots to report.")
		return nil
	}

	fmt.Printf("Snapshot Compatibility (%d snapshot(s), %d boot set(s))\n", len(matrix), len(bootSets))
	fmt.Println(strings.Repeat("─", 80))

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	headers := []string{"SNAPSHOT TIME", "SNAPSHOT PATH", "BOOT"}
	separators := []string{"─────────────", "─────────────", "────"}
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
		cols := []string{timeStr, row.Snapshot.Path, string(row.BootMode)}

		for _, result := range row.Results {
			if row.BootMode == kernel.BootModeBtrfs {
				cols = append(cols, "n/a")
			} else {
				cols = append(cols, result.StatusString())
			}
		}

		modules := strings.Join(row.Modules, ", ")
		if modules == "" {
			modules = "(none)"
		}
		cols = append(cols, modules)

		fmt.Fprintln(w, strings.Join(cols, "\t"))
	}

	return w.Flush()
}
