package cmd

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

const maxConcurrentSizeCalculations = 3

var listSnapshotsCmd = &cobra.Command{
	Use:   "snapshots",
	Short: "List all snapshots for detected volumes",
	Long: `List all snapshots for each detected btrfs volume.

Shows snapshot path, creation time, and parent volume for each snapshot.

Size calculation (--show-size) performance:
  • Fast: Uses btrfs quotas if already enabled
  • Slower: Falls back to native file scanning with progress indicator
  • Note: Large snapshots may take time to calculate`,
	RunE: runListSnapshots,
}

// SnapshotInfo holds snapshot with filesystem context
type SnapshotInfo struct {
	Snapshot   *btrfs.Snapshot   `json:"snapshot"`
	Filesystem *btrfs.Filesystem `json:"filesystem"`
	Size       string            `json:"size,omitempty"`
}

// SnapshotProgress tracks progress for a single snapshot calculation
type SnapshotProgress struct {
	Index     int
	FileCount int64
	Path      string
}

// showParallelProgress displays progress indicators for parallel snapshot calculations
func showParallelProgress(activeSnapshots *sync.Map, totalSnapshots int, done chan struct{}) {
	spinner := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	i := 0

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			var active []*SnapshotProgress
			activeSnapshots.Range(func(key, value interface{}) bool {
				progress := value.(*SnapshotProgress)
				active = append(active, progress)
				return true
			})

			slices.SortFunc(active, func(a, b *SnapshotProgress) int {
				return cmp.Compare(a.Index, b.Index)
			})

			fmt.Print("\r\033[K")

			if len(active) == 0 {
				fmt.Printf("%s Preparing to calculate snapshot sizes...", spinner[i%len(spinner)])
			} else {
				var summary strings.Builder
				summary.WriteString(fmt.Sprintf("%s Calculating: ", spinner[i%len(spinner)]))
				for idx, progress := range active {
					if idx > 0 {
						summary.WriteString(", ")
					}
					files := atomic.LoadInt64(&progress.FileCount)
					summary.WriteString(fmt.Sprintf("snapshot %d/%d (%dk files)",
						progress.Index, totalSnapshots, files/1000))
				}
				fmt.Print(summary.String())
			}

			i++
		}
	}
}

func runListSnapshots(cmd *cobra.Command, args []string) error {
	log.Info().Msg("Listing btrfs snapshots")

	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	showSize, _ := cmd.Flags().GetBool("show-size")
	if showSize {
		log.Info().Msg("Calculating snapshot sizes...")
	}

	searchDirs := cfg.Snapshot.SearchDirectories
	if flagDirs, _ := cmd.Flags().GetStringSlice("search-dirs"); len(flagDirs) > 0 {
		searchDirs = flagDirs
		log.Debug().Strs("search_dirs", searchDirs).Msg("Using search directories from --search-dirs flag")
	}
	btrfsManager := btrfs.NewManager(searchDirs, cfg.Snapshot.MaxDepth, cfg.Advanced.Naming.RwsnapFormat, cfg.Display.LocalTime.IsTrue())

	filesystems, err := btrfsManager.DetectBtrfsFilesystems()
	if err != nil {
		return fmt.Errorf("failed to detect btrfs filesystems: %w", err)
	}

	if len(filesystems) == 0 {
		fmt.Println("No btrfs filesystems found")
		return nil
	}

	jsonOutput, _ := cmd.Flags().GetBool("json")
	showVolume, _ := cmd.Flags().GetBool("show-volume")
	volumeFilter, _ := cmd.Flags().GetString("volume")
	useLocalTime := cfg.Display.LocalTime.IsTrue()

	if volumeFilter != "" {
		filesystems = filterFilesystems(filesystems, volumeFilter)
		if len(filesystems) == 0 {
			fmt.Printf("No btrfs filesystem found matching: %s\n", volumeFilter)
			return nil
		}
	}

	var allSnapshots []*SnapshotInfo
	seenSnapshots := make(map[string]bool)
	filesystemsWithSnapshots := 0

	for _, fs := range filesystems {
		snapshots, err := btrfsManager.FindSnapshots(fs)
		if err != nil {
			log.Warn().Err(err).Str("filesystem", fs.GetBestIdentifier()).Msg("Failed to find snapshots")
			continue
		}

		if len(snapshots) > 0 {
			filesystemsWithSnapshots++

			log.Debug().
				Int("count", len(snapshots)).
				Str("filesystem", fs.GetBestIdentifier()).
				Str("id_type", fs.GetIdentifierType()).
				Msg("Found snapshots for filesystem")
		}

		for _, snapshot := range snapshots {
			if seenSnapshots[snapshot.Path] {
				continue
			}
			seenSnapshots[snapshot.Path] = true

			info := &SnapshotInfo{
				Snapshot:   snapshot,
				Filesystem: fs,
			}

			allSnapshots = append(allSnapshots, info)
		}
	}

	if showSize {
		done := make(chan struct{})
		var activeSnapshots sync.Map

		go showParallelProgress(&activeSnapshots, len(allSnapshots), done)

		semaphore := make(chan struct{}, maxConcurrentSizeCalculations)
		var wg sync.WaitGroup

		for i, info := range allSnapshots {
			wg.Add(1)
			go func(index int, snapshot *SnapshotInfo) {
				defer wg.Done()

				semaphore <- struct{}{}
				defer func() { <-semaphore }()

				progress := SnapshotProgress{
					Index:     index + 1,
					FileCount: 0,
					Path:      snapshot.Snapshot.FilesystemPath,
				}
				activeSnapshots.Store(index, &progress)

				if size, err := btrfs.GetSnapshotSizeWithoutProgress(snapshot.Snapshot.FilesystemPath, &progress.FileCount); err == nil {
					snapshot.Size = size
				}

				activeSnapshots.Delete(index)
			}(i, info)
		}

		wg.Wait()

		close(done)
		fmt.Print("\r\033[K")
	}

	log.Info().
		Int("total_snapshots", len(allSnapshots)).
		Int("filesystems_with_snapshots", filesystemsWithSnapshots).
		Int("total_filesystems", len(filesystems)).
		Msg("Snapshot discovery complete")

	if len(allSnapshots) == 0 {
		fmt.Println("No snapshots found")
		return nil
	}

	if jsonOutput {
		return outputSnapshotsJSON(allSnapshots)
	}

	return outputSnapshotsTable(allSnapshots, showSize, showVolume, useLocalTime)
}
