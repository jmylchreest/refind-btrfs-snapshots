package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
)

func outputVolumesJSON(filesystems []*btrfs.Filesystem) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(filesystems)
}

func outputVolumesTable(filesystems []*btrfs.Filesystem, showAllIds bool) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer w.Flush()

	if showAllIds {
		fmt.Fprintln(w, "DEVICE\tMOUNT POINT\tUUID\tPARTUUID\tLABEL\tPARTLABEL\tSUBVOLUME")
		fmt.Fprintln(w, "------\t-----------\t----\t--------\t-----\t---------\t---------")
	} else {
		fmt.Fprintln(w, "DEVICE\tMOUNT POINT\tIDENTIFIER\tTYPE\tSUBVOLUME")
		fmt.Fprintln(w, "------\t-----------\t----------\t----\t---------")
	}

	for _, fs := range filesystems {
		subvolPath := ""
		if fs.Subvolume != nil {
			subvolPath = fs.Subvolume.Path
		}

		if showAllIds {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				fs.Device,
				fs.MountPoint,
				fs.UUID,
				fs.PartUUID,
				fs.Label,
				fs.PartLabel,
				subvolPath)
		} else {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				fs.Device,
				fs.MountPoint,
				fs.GetBestIdentifier(),
				fs.GetIdentifierType(),
				subvolPath)
		}
	}

	return nil
}

func outputSnapshotsJSON(snapshots []*SnapshotInfo) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(snapshots)
}

func outputSnapshotsTable(snapshots []*SnapshotInfo, showSize bool, showVolume bool, useLocalTime bool) error {
	slices.SortFunc(snapshots, func(a, b *SnapshotInfo) int {
		return b.Snapshot.SnapshotTime.Compare(a.Snapshot.SnapshotTime)
	})

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer w.Flush()

	timeHeader := "SNAPSHOT TIME (UTC)"
	if useLocalTime {
		timeHeader = "SNAPSHOT TIME (LOCAL)"
	}
	headers := []string{timeHeader, "SNAPSHOT PATH"}
	separators := []string{"───────────────────", "─────────────"}

	if showVolume {
		headers = append(headers, "VOLUME")
		separators = append(separators, "──────")
	}
	if showSize {
		headers = append(headers, "SIZE")
		separators = append(separators, "────")
	}

	fmt.Fprintln(w, strings.Join(headers, "\t"))
	fmt.Fprintln(w, strings.Join(separators, "\t"))

	for _, info := range snapshots {
		row := []string{
			btrfs.FormatSnapshotTimeForDisplay(info.Snapshot.SnapshotTime, useLocalTime),
			info.Snapshot.Path,
		}
		if showVolume {
			row = append(row, info.Filesystem.GetBestIdentifier())
		}
		if showSize {
			size := info.Size
			if size == "" {
				size = "unknown"
			}
			row = append(row, size)
		}
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}

	return nil
}
