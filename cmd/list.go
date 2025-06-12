package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"text/tabwriter"
	"time"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
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

	// Add command-specific flags for main list command (backward compatibility)
	listCmd.Flags().Bool("all", false, "Show all snapshots, including non-bootable ones")
	listCmd.Flags().StringP("format", "f", "table", "Output format: table, json, yaml")
	listCmd.Flags().Bool("show-size", false, "Calculate and show snapshot sizes (slower)")
	listCmd.Flags().StringSlice("search-dirs", nil, "Override snapshot search directories")

	// Add flags for list volumes command
	listVolumesCmd.Flags().Bool("json", false, "Output in JSON format")
	listVolumesCmd.Flags().Bool("show-all-ids", false, "Show all device identifiers (UUID, PARTUUID, LABEL, etc.)")

	// Add flags for list snapshots command
	listSnapshotsCmd.Flags().Bool("json", false, "Output in JSON format")
	listSnapshotsCmd.Flags().Bool("show-size", false, "Show snapshot sizes (slower)")
	listSnapshotsCmd.Flags().Bool("show-volume", false, "Show volume column (useful for multi-filesystem setups)")
	listSnapshotsCmd.Flags().String("volume", "", "Show snapshots only for specific volume UUID or device")

	// Bind flags to viper for backward compatibility
	viper.BindPFlag("list.show_all", listCmd.Flags().Lookup("all"))
	viper.BindPFlag("list.format", listCmd.Flags().Lookup("format"))
	viper.BindPFlag("list.show_size", listCmd.Flags().Lookup("show-size"))
	viper.BindPFlag("list.search_dirs", listCmd.Flags().Lookup("search-dirs"))
}

func runListRoot(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("subcommand required. Use 'list volumes' or 'list snapshots'")
	}

	// This should not be reached due to cobra's command structure, but provide helpful message
	return fmt.Errorf("unknown subcommand '%s'. Available subcommands: volumes, snapshots", args[0])
}

func runList(cmd *cobra.Command, args []string) error {
	log.Info().Msg("Listing btrfs snapshots")

	// Initialize btrfs manager
	searchDirs := viper.GetStringSlice("list.search_dirs")
	if len(searchDirs) == 0 {
		searchDirs = viper.GetStringSlice("snapshot.search_directories")
	}
	maxDepth := viper.GetInt("snapshot.max_depth")
	btrfsManager := btrfs.NewManager(searchDirs, maxDepth)

	// Get root filesystem
	rootFS, err := btrfsManager.GetRootFilesystem()
	if err != nil {
		return fmt.Errorf("failed to get root filesystem: %w", err)
	}

	log.Debug().
		Str("device", rootFS.Device).
		Str("uuid", rootFS.UUID).
		Str("subvolume", rootFS.Subvolume.Path).
		Msg("Found root btrfs filesystem")

	// Find snapshots
	snapshots, err := btrfsManager.FindSnapshots(rootFS)
	if err != nil {
		return fmt.Errorf("failed to find snapshots: %w", err)
	}

	if len(snapshots) == 0 {
		fmt.Println("No snapshots found.")
		return nil
	}

	// Filter snapshots if not showing all
	var displaySnapshots []*btrfs.Snapshot
	if viper.GetBool("list.show_all") {
		displaySnapshots = snapshots
	} else {
		// Only show snapshots that would be used for boot entries
		selectionCount := viper.GetInt("snapshot.selection_count")
		if selectionCount > len(snapshots) {
			selectionCount = len(snapshots)
		}
		displaySnapshots = snapshots[:selectionCount]
	}

	// Display snapshots based on format
	format := viper.GetString("list.format")
	switch format {
	case "json":
		return displaySnapshotsJSON(displaySnapshots)
	case "yaml":
		return displaySnapshotsYAML(displaySnapshots)
	case "table":
		fallthrough
	default:
		return displaySnapshotsTable(displaySnapshots, viper.GetBool("list.show_size"))
	}
}

func displaySnapshotsTable(snapshots []*btrfs.Snapshot, showSize bool) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer w.Flush()

	// Print header
	if showSize {
		fmt.Fprintln(w, "PATH\tCREATED\tID\tREAD-ONLY\tSIZE")
	} else {
		fmt.Fprintln(w, "PATH\tCREATED\tID\tREAD-ONLY")
	}

	// Print snapshots
	for _, snapshot := range snapshots {
		created := snapshot.SnapshotTime.Format("2006-01-02 15:04:05")
		readOnly := "No"
		if snapshot.IsReadOnly {
			readOnly = "Yes"
		}

		if showSize {
			size := getSnapshotSize(snapshot)
			fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n",
				snapshot.Path, created, snapshot.ID, readOnly, size)
		} else {
			fmt.Fprintf(w, "%s\t%s\t%d\t%s\n",
				snapshot.Path, created, snapshot.ID, readOnly)
		}
	}

	return nil
}

func displaySnapshotsJSON(snapshots []*btrfs.Snapshot) error {
	// Create a simplified structure for JSON output
	type SnapshotInfo struct {
		Path         string    `json:"path"`
		ID           uint64    `json:"id"`
		Created      time.Time `json:"created"`
		IsReadOnly   bool      `json:"is_readonly"`
		OriginalPath string    `json:"original_path"`
	}

	var snapshotInfos []SnapshotInfo
	for _, snapshot := range snapshots {
		snapshotInfos = append(snapshotInfos, SnapshotInfo{
			Path:         snapshot.Path,
			ID:           snapshot.ID,
			Created:      snapshot.SnapshotTime,
			IsReadOnly:   snapshot.IsReadOnly,
			OriginalPath: snapshot.OriginalPath,
		})
	}

	// Use encoding/json to output
	fmt.Printf("{\n  \"snapshots\": [\n")
	for i, info := range snapshotInfos {
		fmt.Printf("    {\n")
		fmt.Printf("      \"path\": \"%s\",\n", info.Path)
		fmt.Printf("      \"id\": %d,\n", info.ID)
		fmt.Printf("      \"created\": \"%s\",\n", info.Created.Format(time.RFC3339))
		fmt.Printf("      \"is_readonly\": %t,\n", info.IsReadOnly)
		fmt.Printf("      \"original_path\": \"%s\"\n", info.OriginalPath)
		if i < len(snapshotInfos)-1 {
			fmt.Printf("    },\n")
		} else {
			fmt.Printf("    }\n")
		}
	}
	fmt.Printf("  ]\n}\n")

	return nil
}

func displaySnapshotsYAML(snapshots []*btrfs.Snapshot) error {
	fmt.Println("snapshots:")
	for _, snapshot := range snapshots {
		fmt.Printf("  - path: %s\n", snapshot.Path)
		fmt.Printf("    id: %d\n", snapshot.ID)
		fmt.Printf("    created: %s\n", snapshot.SnapshotTime.Format(time.RFC3339))
		fmt.Printf("    is_readonly: %t\n", snapshot.IsReadOnly)
		fmt.Printf("    original_path: %s\n", snapshot.OriginalPath)
	}
	return nil
}

func getSnapshotSize(snapshot *btrfs.Snapshot) string {
	size, err := btrfs.GetSnapshotSize(snapshot.FilesystemPath)
	if err != nil {
		log.Debug().Err(err).Str("path", snapshot.FilesystemPath).Msg("Failed to get snapshot size")
		return "N/A"
	}
	return size
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
			sort.Slice(active, func(i, j int) bool {
				return active[i].Index < active[j].Index
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

	// Check flags
	jsonOutput, _ := cmd.Flags().GetBool("json")
	showVolume, _ := cmd.Flags().GetBool("show-volume")
	volumeFilter, _ := cmd.Flags().GetString("volume")

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
	totalSnapshotsFound := 0
	filesystemsWithSnapshots := 0

	for _, fs := range filesystems {
		snapshots, err := btrfsManager.FindSnapshots(fs)
		if err != nil {
			log.Warn().Err(err).Str("filesystem", fs.GetBestIdentifier()).Msg("Failed to find snapshots")
			continue
		}

		if len(snapshots) > 0 {
			filesystemsWithSnapshots++
			totalSnapshotsFound += len(snapshots)

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

	if jsonOutput {
		return outputSnapshotsJSON(allSnapshots)
	}

	return outputSnapshotsTable(allSnapshots, showSize, showVolume)
}

// SnapshotInfo holds snapshot with filesystem context
type SnapshotInfo struct {
	Snapshot   *btrfs.Snapshot   `json:"snapshot"`
	Filesystem *btrfs.Filesystem `json:"filesystem"`
	Size       string            `json:"size,omitempty"`
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

func outputSnapshotsTable(snapshots []*SnapshotInfo, showSize bool, showVolume bool) error {
	// Sort snapshots by time descending (newest first)
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Snapshot.SnapshotTime.After(snapshots[j].Snapshot.SnapshotTime)
	})

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer w.Flush()

	// Build headers based on flags
	var headers []string
	var separators []string

	headers = append(headers, "SNAPSHOT TIME", "SNAPSHOT PATH", "READ-ONLY", "SUBVOL ID")
	separators = append(separators, "-------------", "-------------", "---------", "---------")

	if showVolume {
		headers = append(headers, "VOLUME")
		separators = append(separators, "------")
	}

	if showSize {
		headers = append(headers, "SIZE")
		separators = append(separators, "----")
	}

	// Print headers
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	fmt.Fprintln(w, strings.Join(separators, "\t"))

	for _, info := range snapshots {
		readOnly := "No"
		if info.Snapshot.IsReadOnly {
			readOnly = "Yes"
		}

		timeStr := info.Snapshot.SnapshotTime.Format("2006-01-02 15:04")

		// Build row data
		var rowData []string
		rowData = append(rowData, timeStr, info.Snapshot.Path, readOnly, fmt.Sprintf("%d", info.Snapshot.ID))

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
