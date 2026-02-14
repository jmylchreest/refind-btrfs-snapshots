package cmd

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"text/tabwriter"
	"time"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/fstab"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const maxConcurrentSizeCalculations = 3

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List btrfs volumes and snapshots",
	Long:  `List btrfs volumes and snapshots. Requires a subcommand (volumes or snapshots).`,
	RunE:  runListRoot,
}

var listVolumesCmd = &cobra.Command{
	Use:   "volumes",
	Short: "List all btrfs filesystems/volumes",
	Long: `List all btrfs filesystems/volumes detected on the system.

Shows device path, mount point, and the best available identifier for each volume.
The IDENTIFIER column shows the preferred identifier value, and TYPE shows what
kind of identifier it is (UUID, PARTUUID, LABEL, PARTLABEL, or DEVICE).`,
	RunE: runListVolumes,
}

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

func init() {
	rootCmd.AddCommand(listCmd)
	listCmd.AddCommand(listVolumesCmd)
	listCmd.AddCommand(listSnapshotsCmd)

	// Add flags for list volumes command
	listVolumesCmd.Flags().Bool("json", false, "Output in JSON format")
	listVolumesCmd.Flags().Bool("show-all-ids", false, "Show all device identifiers (UUID, PARTUUID, LABEL, etc.)")

	// Add flags for list snapshots command
	listSnapshotsCmd.Flags().Bool("json", false, "Output in JSON format")
	listSnapshotsCmd.Flags().Bool("show-size", false, "Show snapshot sizes (slower)")
	listSnapshotsCmd.Flags().Bool("show-volume", false, "Show volume column (useful for multi-filesystem setups)")
	listSnapshotsCmd.Flags().Bool("no-staleness", false, "Skip kernel staleness detection (faster, no ESP access needed)")
	listSnapshotsCmd.Flags().String("volume", "", "Show snapshots only for specific volume UUID or device")
	listSnapshotsCmd.Flags().StringSlice("search-dirs", nil, "Override snapshot search directories")

}

func runListRoot(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("subcommand required. Use 'list volumes' or 'list snapshots'")
	}

	// This should not be reached due to cobra's command structure, but provide helpful message
	return fmt.Errorf("unknown subcommand '%s'. Available subcommands: volumes, snapshots", args[0])
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
			// Collect active snapshots
			var active []*SnapshotProgress
			activeSnapshots.Range(func(key, value interface{}) bool {
				progress := value.(*SnapshotProgress)
				active = append(active, progress)
				return true
			})

			// Sort by index
			slices.SortFunc(active, func(a, b *SnapshotProgress) int {
				return cmp.Compare(a.Index, b.Index)
			})

			// Clear the entire line first
			fmt.Print("\r\033[K")

			if len(active) == 0 {
				fmt.Printf("%s Preparing to calculate snapshot sizes...", spinner[i%len(spinner)])
			} else {
				// Show summary of active calculations
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

func runListVolumes(cmd *cobra.Command, args []string) error {
	log.Info().Msg("Listing btrfs volumes")

	// Initialize btrfs manager
	searchDirs := viper.GetStringSlice("snapshot.search_directories")
	maxDepth := viper.GetInt("snapshot.max_depth")
	btrfsManager := btrfs.NewManager(searchDirs, maxDepth)

	// Detect all btrfs filesystems
	filesystems, err := btrfsManager.DetectBtrfsFilesystems()
	if err != nil {
		return fmt.Errorf("failed to detect btrfs filesystems: %w", err)
	}

	if len(filesystems) == 0 {
		fmt.Println("No btrfs filesystems found")
		return nil
	}

	// Check output format flags
	jsonOutput, _ := cmd.Flags().GetBool("json")
	showAllIds, _ := cmd.Flags().GetBool("show-all-ids")

	if jsonOutput {
		return outputVolumesJSON(filesystems)
	}

	return outputVolumesTable(filesystems, showAllIds)
}

func runListSnapshots(cmd *cobra.Command, args []string) error {
	log.Info().Msg("Listing btrfs snapshots")

	// Check flags
	showSize, _ := cmd.Flags().GetBool("show-size")
	if showSize {
		log.Info().Msg("Calculating snapshot sizes...")
	}

	// Initialize btrfs manager - use flag override if provided
	searchDirs := viper.GetStringSlice("snapshot.search_directories")
	if flagDirs, _ := cmd.Flags().GetStringSlice("search-dirs"); len(flagDirs) > 0 {
		searchDirs = flagDirs
		log.Debug().Strs("search_dirs", searchDirs).Msg("Using search directories from --search-dirs flag")
	}
	maxDepth := viper.GetInt("snapshot.max_depth")
	btrfsManager := btrfs.NewManager(searchDirs, maxDepth)

	// Detect all btrfs filesystems
	filesystems, err := btrfsManager.DetectBtrfsFilesystems()
	if err != nil {
		return fmt.Errorf("failed to detect btrfs filesystems: %w", err)
	}

	if len(filesystems) == 0 {
		fmt.Println("No btrfs filesystems found")
		return nil
	}

	// Check flags
	jsonOutput, _ := cmd.Flags().GetBool("json")
	showVolume, _ := cmd.Flags().GetBool("show-volume")
	volumeFilter, _ := cmd.Flags().GetString("volume")
	useLocalTime := viper.GetBool("display.local_time")

	// Filter filesystems if volume specified
	if volumeFilter != "" {
		filesystems = filterFilesystems(filesystems, volumeFilter)
		if len(filesystems) == 0 {
			fmt.Printf("No btrfs filesystem found matching: %s\n", volumeFilter)
			return nil
		}
	}

	// Find snapshots for each filesystem and deduplicate
	var allSnapshots []*SnapshotInfo
	seenSnapshots := make(map[string]bool) // Track by snapshot path to avoid duplicates
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

		// Convert to SnapshotInfo with volume context
		for _, snapshot := range snapshots {
			// Skip if we've already seen this snapshot path
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

	// Calculate sizes after collecting all snapshots so we know the total count
	if showSize {
		// Start parallel progress indicator
		done := make(chan struct{})
		var activeSnapshots sync.Map

		go showParallelProgress(&activeSnapshots, len(allSnapshots), done)

		// Create worker pool for parallel size calculations
		semaphore := make(chan struct{}, maxConcurrentSizeCalculations)
		var wg sync.WaitGroup

		for i, info := range allSnapshots {
			wg.Add(1)
			go func(index int, snapshot *SnapshotInfo) {
				defer wg.Done()

				// Acquire semaphore
				semaphore <- struct{}{}
				defer func() { <-semaphore }()

				// Create progress tracker for this snapshot
				progress := SnapshotProgress{
					Index:     index + 1,
					FileCount: 0,
					Path:      snapshot.Snapshot.FilesystemPath,
				}
				activeSnapshots.Store(index, &progress)

				// Calculate size
				if size, err := btrfs.GetSnapshotSizeWithoutProgress(snapshot.Snapshot.FilesystemPath, &progress.FileCount); err == nil {
					snapshot.Size = size
				}

				// Remove from active snapshots
				activeSnapshots.Delete(index)
			}(i, info)
		}

		// Wait for all calculations to complete
		wg.Wait()

		// Stop progress and clear line
		close(done)
		fmt.Print("\r\033[K")
	}

	// Log summary
	log.Info().
		Int("total_snapshots", len(allSnapshots)).
		Int("filesystems_with_snapshots", filesystemsWithSnapshots).
		Int("total_filesystems", len(filesystems)).
		Msg("Snapshot discovery complete")

	if len(allSnapshots) == 0 {
		fmt.Println("No snapshots found")
		return nil
	}

	// Detect boot sets and compute staleness (default on, unless --no-staleness)
	noStaleness, _ := cmd.Flags().GetBool("no-staleness")
	var bootSets []*kernel.BootSet
	if !noStaleness {
		bootSets = detectBootSets()
	}

	// Get root filesystem for boot mode detection
	rootFS, _ := btrfsManager.GetRootFilesystem()

	// Build planner and checker for per-snapshot boot mode/staleness detection
	fstabMgr := fstab.NewManager()
	var planner *kernel.Planner
	var checker *kernel.Checker
	if len(bootSets) > 0 {
		staleAction := kernel.ParseStaleAction(viper.GetString("kernel.stale_snapshot_action"))
		checker = kernel.NewChecker(staleAction)
	}
	if rootFS != nil {
		planner = kernel.NewPlanner(fstabMgr, checker, bootSets, rootFS)
	}

	// Detect boot mode and compute staleness for each snapshot
	for _, info := range allSnapshots {
		// Per-snapshot boot mode detection
		if planner != nil {
			plans := planner.Plan([]*btrfs.Snapshot{info.Snapshot})
			if len(plans) > 0 {
				info.BootMode = plans[0].Mode
			}
		}
		if info.BootMode == "" {
			info.BootMode = kernel.BootModeESP // default
		}

		// Staleness only applies to ESP-mode snapshots
		if checker != nil && info.BootMode == kernel.BootModeESP {

			for _, bs := range bootSets {
				result := checker.CheckSnapshot(info.Snapshot.FilesystemPath, bs)

				info.Staleness = append(info.Staleness, SnapshotKernelStatus{
					KernelName:      bs.KernelName,
					KernelVersion:   bs.KernelVersion(),
					Status:          result.StatusString(),
					Method:          string(result.Method),
					SnapshotModules: result.SnapshotModules,
					Reason:          string(result.Reason),
				})
			}
		} else if len(bootSets) > 0 && info.BootMode == kernel.BootModeBtrfs {
			// Btrfs-mode: kernels are in-snapshot, staleness is not applicable
			for _, bs := range bootSets {
				info.Staleness = append(info.Staleness, SnapshotKernelStatus{
					KernelName:    bs.KernelName,
					KernelVersion: bs.KernelVersion(),
					Status:        "n/a",
					Method:        "in_snapshot",
				})
			}
		}
	}

	if jsonOutput {
		return outputSnapshotsJSON(allSnapshots)
	}

	return outputSnapshotsTable(allSnapshots, showSize, showVolume, useLocalTime, bootSets)
}

// SnapshotInfo holds snapshot with filesystem context
type SnapshotInfo struct {
	Snapshot   *btrfs.Snapshot        `json:"snapshot"`
	Filesystem *btrfs.Filesystem      `json:"filesystem"`
	Size       string                 `json:"size,omitempty"`
	BootMode   kernel.BootMode        `json:"boot_mode"`
	Staleness  []SnapshotKernelStatus `json:"staleness,omitempty"`
}

// SnapshotKernelStatus holds the staleness result for one snapshot+bootset pair.
type SnapshotKernelStatus struct {
	KernelName      string   `json:"kernel_name"`
	KernelVersion   string   `json:"kernel_version,omitempty"`
	Status          string   `json:"status"` // "fresh", "stale", "unknown"
	Method          string   `json:"method,omitempty"`
	SnapshotModules []string `json:"snapshot_modules,omitempty"`
	Reason          string   `json:"reason,omitempty"`
}

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

func outputSnapshotsTable(snapshots []*SnapshotInfo, showSize bool, showVolume bool, useLocalTime bool, bootSets []*kernel.BootSet) error {
	// Sort snapshots by time descending (newest first)
	slices.SortFunc(snapshots, func(a, b *SnapshotInfo) int {
		return b.Snapshot.SnapshotTime.Compare(a.Snapshot.SnapshotTime)
	})

	hasStaleness := len(bootSets) > 0

	// Print boot sets summary if available
	if hasStaleness {
		fmt.Printf("ESP Boot Sets (%d detected)\n", len(bootSets))
		fmt.Println(strings.Repeat("─", 72))
		for _, bs := range bootSets {
			version := bs.KernelVersion()
			if version == "" {
				version = "(not inspected)"
			}
			kernelPath := "(not found)"
			if bs.Kernel != nil {
				kernelPath = bs.Kernel.Path
			}
			fmt.Printf("  %-16s %s  %s\n", bs.KernelName, version, kernelPath)
		}
		fmt.Println()
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer w.Flush()

	// Build headers based on flags
	var headers []string
	var separators []string

	// Add timezone indicator to the time column header
	timeHeader := "SNAPSHOT TIME (UTC)"
	if useLocalTime {
		timeHeader = "SNAPSHOT TIME (LOCAL)"
	}
	headers = append(headers, timeHeader, "SNAPSHOT PATH")
	separators = append(separators, "───────────────────", "─────────────")

	// Boot mode column (always shown when we have staleness info)
	if hasStaleness {
		headers = append(headers, "BOOT")
		separators = append(separators, "────")
	}

	// Add one staleness column per boot set
	if hasStaleness {
		for _, bs := range bootSets {
			headers = append(headers, strings.ToUpper(bs.KernelName))
			separators = append(separators, strings.Repeat("─", max(len(bs.KernelName), 7)))
		}
		headers = append(headers, "MODULES")
		separators = append(separators, "───────")
	}

	if showVolume {
		headers = append(headers, "VOLUME")
		separators = append(separators, "──────")
	}

	if showSize {
		headers = append(headers, "SIZE")
		separators = append(separators, "────")
	}

	// Print headers
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	fmt.Fprintln(w, strings.Join(separators, "\t"))

	for _, info := range snapshots {
		timeStr := btrfs.FormatSnapshotTimeForDisplay(info.Snapshot.SnapshotTime, useLocalTime)

		// Build row data
		var rowData []string
		rowData = append(rowData, timeStr, info.Snapshot.Path)

		// Add boot mode and staleness columns
		if hasStaleness {
			rowData = append(rowData, string(info.BootMode))

			modules := ""
			for _, sk := range info.Staleness {
				rowData = append(rowData, sk.Status)
				if modules == "" && len(sk.SnapshotModules) > 0 {
					modules = strings.Join(sk.SnapshotModules, ",")
				}
			}
			// Pad if fewer staleness results than boot sets (shouldn't happen, but be safe)
			for len(rowData) < 3+len(bootSets) {
				rowData = append(rowData, "-")
			}
			rowData = append(rowData, modules)
		}

		if showVolume {
			volumeId := info.Filesystem.GetBestIdentifier()
			rowData = append(rowData, volumeId)
		}

		if showSize {
			size := info.Size
			if size == "" {
				size = "unknown"
			}
			rowData = append(rowData, size)
		}

		fmt.Fprintln(w, strings.Join(rowData, "\t"))
	}

	return nil
}

func filterFilesystems(filesystems []*btrfs.Filesystem, filter string) []*btrfs.Filesystem {
	var filtered []*btrfs.Filesystem

	// Return empty slice if filter is empty
	if filter == "" {
		return filtered
	}

	for _, fs := range filesystems {
		// Check if filter matches any identifier
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
