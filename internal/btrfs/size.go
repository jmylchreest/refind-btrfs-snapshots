package btrfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
)

// GetSnapshotSizeWithoutProgress calculates the size of a snapshot using an
// external file counter. Tries btrfs qgroups first (fast, when quotas are
// enabled), falls back to native filesystem walking with a 120s timeout.
func GetSnapshotSizeWithoutProgress(path string, fileCount *int64) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path cannot be empty")
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("path does not exist: %s", path)
	}

	if size, err := getSnapshotSizeFromQgroups(path); err == nil {
		return size, nil
	}
	return getSnapshotSizeNativeExternal(path, fileCount)
}

// getSnapshotSizeFromQgroups asks btrfs for the snapshot's exclusive size via
// qgroups. Only works when quotas are enabled; returns an error otherwise so
// the caller falls back to native counting.
func getSnapshotSizeFromQgroups(path string) (string, error) {
	if err := exec.Command("btrfs", "filesystem", "show").Run(); err != nil {
		return "", fmt.Errorf("btrfs not available")
	}

	output, err := exec.Command("btrfs", "qgroup", "show", path).Output()
	if err != nil {
		return "", fmt.Errorf("quotas not enabled")
	}

	outputStr := string(output)
	if strings.Contains(outputStr, "qgroup data inconsistent") || strings.Contains(outputStr, "0.00B") {
		return "", fmt.Errorf("qgroup data inconsistent or incomplete")
	}

	subvolOutput, err := exec.Command("btrfs", "subvolume", "show", path).Output()
	if err != nil {
		return "", fmt.Errorf("failed to get subvolume info: %w", err)
	}

	subvolID := ""
	for _, line := range strings.Split(string(subvolOutput), "\n") {
		if strings.Contains(line, "Subvolume ID:") {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				subvolID = parts[2]
				break
			}
		}
	}
	if subvolID == "" {
		return "", fmt.Errorf("could not find subvolume ID")
	}

	for _, line := range strings.Split(string(output), "\n") {
		if strings.Contains(line, "0/"+subvolID) {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				return parts[2], nil
			}
		}
	}

	return "", fmt.Errorf("subvolume not found in qgroups")
}

// getSnapshotSizeNativeExternal walks the snapshot directory and sums file
// sizes, updating the supplied counter atomically. Bounded by a 120s timeout
// so a hung walk on a corrupt subvolume doesn't lock the caller.
func getSnapshotSizeNativeExternal(path string, externalFileCount *int64) (string, error) {
	var totalSize int64

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	start := time.Now()
	err := filepath.WalkDir(path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Skip inaccessible files/directories instead of failing
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if !d.IsDir() {
			if info, err := d.Info(); err == nil {
				atomic.AddInt64(&totalSize, info.Size())
			}
		}
		atomic.AddInt64(externalFileCount, 1)
		return nil
	})

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "timeout", nil
		}
		return "", fmt.Errorf("failed to calculate size: %w", err)
	}

	log.Debug().
		Int64("total_size", totalSize).
		Int64("file_count", atomic.LoadInt64(externalFileCount)).
		Dur("duration", time.Since(start)).
		Str("path", path).
		Msg("Completed size calculation")

	return formatBytes(totalSize), nil
}

// formatBytes converts bytes to human-readable IEC units (KiB, MiB, etc).
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}

	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit && exp < len(units)-1; n /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %s", float64(bytes)/float64(div), units[exp])
}
