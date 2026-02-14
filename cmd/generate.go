// Copyright (c) 2024 John Mylchreest <jmylchreest@gmail.com>
//
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
	"fmt"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/diff"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/esp"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/fstab"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/refind"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/runner"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var generateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate rEFInd boot entries for btrfs snapshots",
	Long: `Generate rEFInd boot configuration files for btrfs snapshots.

Automatically detects snapshots, updates fstab files, and creates boot entries.
Prefers refind_linux.conf updates but can generate include files when needed.`,
	RunE: runGenerate,
}

func init() {
	rootCmd.AddCommand(generateCmd)

	// Add command-specific flags
	generateCmd.Flags().StringP("config-path", "c", "", "Path to rEFInd main config file")
	generateCmd.Flags().StringP("esp-path", "e", "", "Path to ESP mount point")
	generateCmd.Flags().IntP("count", "n", 0, "Number of snapshots to include (0 = all snapshots)")
	generateCmd.Flags().Bool("dry-run", false, "Show what would be done without making changes")
	generateCmd.Flags().Bool("force", false, "Force generation even if booted from snapshot")
	generateCmd.Flags().Bool("update-refind-conf", false, "Update main rEFInd config file")
	generateCmd.Flags().BoolP("generate-include", "g", false, "Force generation of refind-btrfs-snapshots.conf for inclusion into refind.conf")
	generateCmd.Flags().BoolP("yes", "y", false, "Automatically approve all changes without prompting")

	// Bind flags to viper
	viper.BindPFlag("refind.config_path", generateCmd.Flags().Lookup("config-path"))
	viper.BindPFlag("esp.mount_point", generateCmd.Flags().Lookup("esp-path"))
	viper.BindPFlag("snapshot.selection_count", generateCmd.Flags().Lookup("count"))
	viper.BindPFlag("dry_run", generateCmd.Flags().Lookup("dry-run"))
	viper.BindPFlag("force", generateCmd.Flags().Lookup("force"))
	viper.BindPFlag("update_refind_conf", generateCmd.Flags().Lookup("update-refind-conf"))
	viper.BindPFlag("generate_include", generateCmd.Flags().Lookup("generate-include"))
	viper.BindPFlag("yes", generateCmd.Flags().Lookup("yes"))
}

func runGenerate(cmd *cobra.Command, args []string) error {
	log.Info().Msg("Starting rEFInd btrfs snapshot generation")

	// Check if running as root and warn if not
	if err := checkRootPrivileges(); err != nil {
		log.Warn().Err(err).Msg("Not running as root - some operations may fail")
	}

	// Initialize managers and runner
	searchDirs := viper.GetStringSlice("snapshot.search_directories")
	maxDepth := viper.GetInt("snapshot.max_depth")
	btrfsManager := btrfs.NewManager(searchDirs, maxDepth)
	fstabManager := fstab.NewManager()
	r := runner.New(viper.GetBool("dry_run"))

	// Get root filesystem first, to be reused
	rootFS, err := btrfsManager.GetRootFilesystem()
	if err != nil {
		return fmt.Errorf("failed to get root filesystem: %w", err)
	}

	// Check if we're booted from a snapshot
	if !viper.GetBool("force") && viper.GetBool("behavior.exit_on_snapshot_boot") {
		if btrfsManager.IsSnapshotBootFromRootFS(rootFS) {
			log.Warn().Msg("Currently booted from a snapshot. Use --force to override or disable this check in config.")
			return fmt.Errorf("refusing to generate configs while booted from snapshot")
		}
	}

	// Detect ESP
	espUUID := viper.GetString("esp.uuid")
	espDetector := esp.NewESPDetector(espUUID)

	var espPath string
	if viper.GetBool("esp.auto_detect") {
		// Find ESP once and reuse the result
		detectedESP, err := espDetector.FindESP()
		if err != nil {
			return fmt.Errorf("failed to detect ESP: %w", err)
		}

		if detectedESP.MountPoint == "" {
			return fmt.Errorf("ESP is not mounted")
		}

		espPath = detectedESP.MountPoint
		log.Info().Str("path", espPath).Msg("Auto-detected ESP path")

		// Validate ESP access using the detected ESP path
		if err := espDetector.ValidateESPPath(detectedESP.MountPoint); err != nil {
			return fmt.Errorf("ESP validation failed: %w", err)
		}
	} else if viper.GetString("esp.mount_point") != "" {
		espPath = viper.GetString("esp.mount_point")
		log.Info().Str("path", espPath).Msg("Using configured ESP path")

		// Validate manually configured ESP path
		detector := esp.NewESPDetector("")
		if err := detector.ValidateESPPath(espPath); err != nil {
			return fmt.Errorf("ESP validation failed: %w", err)
		}
	} else {
		return fmt.Errorf("ESP path not configured and auto-detection disabled")
	}

	// Scan for boot images on the ESP using the kernel scanner
	var bootImagePatterns []kernel.PatternConfig
	if patterns := viper.Get("kernel.boot_image_patterns"); patterns != nil {
		// Attempt to load custom patterns from config
		if patternList, ok := patterns.([]interface{}); ok {
			for _, p := range patternList {
				if pm, ok := p.(map[string]interface{}); ok {
					pc := kernel.PatternConfig{}
					if g, ok := pm["glob"].(string); ok {
						pc.Glob = g
					}
					if r, ok := pm["role"].(string); ok {
						role, err := kernel.ParseImageRole(r)
						if err != nil {
							log.Warn().Err(err).Str("glob", pc.Glob).Msg("Invalid role in boot_image_patterns, skipping")
							continue
						}
						pc.Role = role
					}
					if sp, ok := pm["strip_prefix"].(string); ok {
						pc.StripPrefix = sp
					}
					if ss, ok := pm["strip_suffix"].(string); ok {
						pc.StripSuffix = ss
					}
					if kn, ok := pm["kernel_name"].(string); ok {
						pc.KernelName = kn
					}
					bootImagePatterns = append(bootImagePatterns, pc)
				}
			}
		}
	}
	// nil/empty patterns will cause NewScanner to use DefaultPatterns()
	kernelScanner := kernel.NewScanner(espPath, bootImagePatterns)

	// Root filesystem was already retrieved above

	logEntry := log.Info().
		Str("device", rootFS.Device).
		Str("identifier", rootFS.GetBestIdentifier()).
		Str("id_type", rootFS.GetIdentifierType())

	if rootFS.Subvolume != nil {
		logEntry.Str("subvolume", rootFS.Subvolume.Path)
	} else {
		logEntry.Str("subvolume", "<unknown>")
	}

	logEntry.Msg("Found root btrfs filesystem")

	// Find snapshots
	snapshots, err := btrfsManager.FindSnapshots(rootFS)
	if err != nil {
		return fmt.Errorf("failed to find snapshots: %w", err)
	}

	if len(snapshots) == 0 {
		log.Info().Msg("No snapshots found")
		return nil
	}

	// Select snapshots to use
	selectionCount := viper.GetInt("snapshot.selection_count")
	var selectedSnapshots []*btrfs.Snapshot

	// Handle special values for "all snapshots"
	if selectionCount <= 0 {
		selectedSnapshots = snapshots
	} else {
		if selectionCount > len(snapshots) {
			selectionCount = len(snapshots)
		}
		selectedSnapshots = snapshots[:selectionCount]
	}

	log.Info().
		Int("total", len(snapshots)).
		Int("selected", len(selectedSnapshots)).
		Msg("Selected snapshots for processing")

	// Process snapshots for writability
	var processedSnapshots []*btrfs.Snapshot
	writableMethod := viper.GetString("snapshot.writable_method")

	log.Info().Str("method", writableMethod).Msg("Using writable snapshot method")

	if writableMethod == "toggle" {
		// Toggle approach: change read-only flag on original snapshots
		processedSnapshots = selectedSnapshots
		for _, snapshot := range processedSnapshots {
			if snapshot.IsReadOnly {
				if err := btrfsManager.MakeSnapshotWritable(snapshot, r); err != nil {
					log.Error().Err(err).Str("path", snapshot.Path).Msg("Failed to make snapshot writable")
					continue
				}
			}
		}

		// Clean up writability: make unselected snapshots read-only
		if viper.GetBool("behavior.cleanup_old_snapshots") {
			if err := btrfsManager.CleanupSnapshotWritability(snapshots, selectedSnapshots, r); err != nil {
				log.Warn().Err(err).Msg("Failed to cleanup snapshot writability")
			}
		}
	} else if writableMethod == "copy" {
		// Copy approach: create writable copies (legacy method)
		destDir := viper.GetString("snapshot.destination_dir")

		for _, snapshot := range selectedSnapshots {
			if snapshot.IsReadOnly {
				log.Info().
					Str("source", snapshot.Path).
					Msg("Creating writable snapshot")

				writableSnapshot, err := btrfsManager.CreateWritableSnapshot(snapshot, destDir, r)
				if err != nil {
					log.Error().Err(err).Str("source", snapshot.Path).Msg("Failed to create writable snapshot")
					continue
				}
				processedSnapshots = append(processedSnapshots, writableSnapshot)
			} else {
				processedSnapshots = append(processedSnapshots, snapshot)
			}
		}

		// Clean up old snapshots for copy method
		if viper.GetBool("behavior.cleanup_old_snapshots") {
			if err := btrfsManager.CleanupOldSnapshots(destDir, selectionCount, r); err != nil {
				log.Warn().Err(err).Msg("Failed to cleanup old snapshots")
			}
		}
	} else {
		return fmt.Errorf("invalid writable_method: %s (must be 'toggle' or 'copy')", writableMethod)
	}

	if len(processedSnapshots) == 0 {
		log.Warn().Msg("No snapshots available for processing")
		return nil
	}

	// Scan ESP for boot images and build boot sets
	// Find directories containing kernels on the ESP (scan common locations)
	var bootSets []*kernel.BootSet
	kernelSearchDirs := []string{
		filepath.Join(espPath, "boot"),
		filepath.Join(espPath, "EFI", "Linux"),
		espPath,
	}

	var allImages []*kernel.BootImage
	for _, searchDir := range kernelSearchDirs {
		images, err := kernelScanner.ScanDir(searchDir)
		if err != nil {
			log.Trace().Err(err).Str("dir", searchDir).Msg("No boot images found in directory")
			continue
		}
		if len(images) > 0 {
			allImages = append(allImages, images...)
			log.Debug().Str("dir", searchDir).Int("count", len(images)).Msg("Found boot images")
		}
	}

	if len(allImages) > 0 {
		// Inspect kernel binaries for version info (best-effort)
		kernelScanner.InspectAll(allImages)

		// Group into boot sets
		bootSets = kernelScanner.BuildBootSets(allImages)

		log.Info().Int("boot_sets", len(bootSets)).Msg("Detected boot configurations on ESP")
	} else {
		log.Debug().Msg("No boot images found on ESP, staleness checking will be unavailable")
	}

	// Run staleness checks if we have boot sets
	staleAction := kernel.ParseStaleAction(viper.GetString("kernel.stale_snapshot_action"))
	stalenessChecker := kernel.NewChecker(staleAction)

	// Map from snapshot path to staleness results (one per boot set)
	type snapshotStaleness struct {
		results []*kernel.StalenessResult
		bootSet *kernel.BootSet
	}
	staleSnapshots := make(map[string][]snapshotStaleness)

	if len(bootSets) > 0 {
		for _, snapshot := range processedSnapshots {
			for _, bs := range bootSets {
				result := stalenessChecker.CheckSnapshot(snapshot.Path, bs)

				if result.IsStale {
					staleSnapshots[snapshot.Path] = append(staleSnapshots[snapshot.Path], snapshotStaleness{
						results: []*kernel.StalenessResult{result},
						bootSet: bs,
					})

					logEntry := log.Warn().
						Str("snapshot", snapshot.Path).
						Str("kernel", bs.KernelName).
						Str("action", string(result.Action)).
						Str("reason", string(result.Reason)).
						Str("method", string(result.Method))

					if result.ExpectedVersion != "" {
						logEntry.Str("expected_version", result.ExpectedVersion)
					}
					if len(result.SnapshotModules) > 0 {
						logEntry.Strs("snapshot_modules", result.SnapshotModules)
					}

					logEntry.Msg("Snapshot is stale for boot kernel")

					// Handle delete action — remove snapshot from processed list
					if result.Action == kernel.ActionDelete {
						log.Info().
							Str("snapshot", snapshot.Path).
							Str("kernel", bs.KernelName).
							Msg("Skipping stale snapshot (stale_snapshot_action=delete)")
					}
				} else {
					log.Debug().
						Str("snapshot", snapshot.Path).
						Str("kernel", bs.KernelName).
						Str("method", string(result.Method)).
						Msg("Snapshot is fresh for boot kernel")
				}
			}
		}

		// Filter out snapshots that should be deleted for ALL boot sets
		if staleAction == kernel.ActionDelete {
			var filteredSnapshots []*btrfs.Snapshot
			for _, snapshot := range processedSnapshots {
				stalePairs := staleSnapshots[snapshot.Path]
				allStale := len(stalePairs) > 0 && len(stalePairs) == len(bootSets)
				allDelete := true
				for _, sp := range stalePairs {
					for _, r := range sp.results {
						if r.Action != kernel.ActionDelete {
							allDelete = false
							break
						}
					}
				}
				if allStale && allDelete {
					log.Info().Str("snapshot", snapshot.Path).Msg("Removing stale snapshot from generation (delete action)")
				} else {
					filteredSnapshots = append(filteredSnapshots, snapshot)
				}
			}
			processedSnapshots = filteredSnapshots

			if len(processedSnapshots) == 0 {
				log.Warn().Msg("All snapshots were stale and removed (stale_snapshot_action=delete)")
				return nil
			}
		}
	}

	// Collect all changes in a unified patch
	unifiedPatch := diff.NewPatchDiff()
	operationSummary := &OperationSummary{
		IncludedSnapshots: make([]string, 0),
		AddedSnapshots:    make([]string, 0),
		RemovedSnapshots:  make([]string, 0),
		StaleSnapshots:    make([]string, 0),
		UpdatedFstabs:     make([]string, 0),
		UpdatedConfigs:    make([]string, 0),
		WritableChanges:   make([]string, 0),
	}

	// Record stale snapshots in summary
	for snapshotPath, stalePairs := range staleSnapshots {
		for _, sp := range stalePairs {
			for _, r := range sp.results {
				operationSummary.StaleSnapshots = append(operationSummary.StaleSnapshots,
					fmt.Sprintf("%s (kernel=%s, action=%s)", snapshotPath, sp.bootSet.KernelName, r.Action))
			}
		}
	}

	// Update fstab files in snapshots
	for _, snapshot := range processedSnapshots {
		if fileDiff, err := fstabManager.UpdateSnapshotFstabDiff(snapshot, rootFS); err != nil {
			log.Warn().Err(err).Str("snapshot", snapshot.Path).Msg("Failed to update snapshot fstab")
		} else if fileDiff != nil {
			unifiedPatch.AddFile(fileDiff)
			operationSummary.UpdatedFstabs = append(operationSummary.UpdatedFstabs, snapshot.Path+"/etc/fstab")
		}
	}

	// Parse rEFInd configuration
	refindParser := refind.NewParserWithScanner(espPath, kernelScanner)

	// Try to find rEFInd config automatically first
	configPath := viper.GetString("refind.config_path")
	if configPath == "/EFI/refind/refind.conf" { // Default value
		// Try to auto-detect
		if detectedPath, err := refindParser.FindRefindConfigPath(); err == nil {
			configPath = detectedPath
			log.Info().Str("path", configPath).Msg("Auto-detected rEFInd config")
		} else {
			// Fall back to configured path
			if !filepath.IsAbs(configPath) {
				configPath = filepath.Join(espPath, configPath)
			}
			log.Debug().Str("path", configPath).Msg("Using configured rEFInd config path")
		}
	} else {
		// User specified a custom path
		if !filepath.IsAbs(configPath) {
			configPath = filepath.Join(espPath, configPath)
		}
		log.Info().Str("path", configPath).Msg("Using custom rEFInd config path")
	}

	config, err := refindParser.ParseConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to parse rEFInd config: %w", err)
	}

	// Find suitable menu entries for snapshot generation
	var sourceEntries []*refind.MenuEntry
	for _, entry := range config.Entries {
		if isBootableEntry(entry, rootFS) {
			sourceEntries = append(sourceEntries, entry)
		}
	}

	if len(sourceEntries) == 0 {
		return fmt.Errorf("no suitable boot entries found in rEFInd config")
	}

	log.Info().
		Int("total_entries", len(config.Entries)).
		Int("valid_entries", len(sourceEntries)).
		Msg("Checking valid entries")

	// Generate snapshot configurations
	generator := refind.NewGeneratorWithBootSets(espPath, kernelScanner, bootSets)

	// Separate entries by source type
	var refindLinuxEntries []*refind.MenuEntry
	var otherEntries []*refind.MenuEntry

	for _, entry := range sourceEntries {
		if entry.SourceFile != "" && strings.HasSuffix(entry.SourceFile, "refind_linux.conf") {
			refindLinuxEntries = append(refindLinuxEntries, entry)
		} else {
			otherEntries = append(otherEntries, entry)
		}
	}

	// Handle refind_linux.conf entries - group by file and update once per file
	var updatedRefindLinuxConf bool
	refindLinuxFiles := make(map[string][]*refind.MenuEntry)

	// Group entries by their source file
	for _, entry := range refindLinuxEntries {
		// Only process original entries that have rootflags=subvol=@ or /@ (not our generated snapshot entries)
		if entry.BootOptions != nil && entry.BootOptions.Subvol != "" {
			// Normalize subvol path for comparison (same logic as isBootableEntry)
			entrySubvol := strings.TrimPrefix(entry.BootOptions.Subvol, "/")
			if entrySubvol == "@" {
				refindLinuxFiles[entry.SourceFile] = append(refindLinuxFiles[entry.SourceFile], entry)
			}
		}
	}

	// Update each refind_linux.conf file once with all its matching entries
	// Sort the file paths to ensure consistent processing order
	var filePaths []string
	for filePath := range refindLinuxFiles {
		filePaths = append(filePaths, filePath)
	}
	sort.Strings(filePaths)

	for _, filePath := range filePaths {
		entries := refindLinuxFiles[filePath]
		log.Info().
			Str("source_file", filePath).
			Int("entries", len(entries)).
			Msg("Updating refind_linux.conf with snapshots")

		if configDiff, err := generator.UpdateRefindLinuxConfWithAllEntries(processedSnapshots, entries, rootFS); err != nil {
			log.Error().Err(err).Str("source_file", filePath).Msg("Failed to update refind_linux.conf")
		} else if configDiff != nil {
			unifiedPatch.AddFile(configDiff)
			operationSummary.UpdatedConfigs = append(operationSummary.UpdatedConfigs, configDiff.Path)
			updatedRefindLinuxConf = true

			// Since we're adding configs, record that snapshots are being added
			for _, snapshot := range processedSnapshots {
				snapshotDisplayName := btrfs.FormatSnapshotTimeForMenu(snapshot.SnapshotTime, viper.GetString("advanced.naming.menu_format"), viper.GetBool("display.local_time"))
				operationSummary.AddedSnapshots = append(operationSummary.AddedSnapshots, snapshotDisplayName)
			}
		}
	}

	// Create managed config file if:
	// 1. We haven't updated any refind_linux.conf files AND we have other entries, OR
	// 2. User explicitly requested include file generation with --generate-include
	forceGenerateInclude := viper.GetBool("generate_include")
	shouldGenerateInclude := (!updatedRefindLinuxConf && len(otherEntries) > 0) || forceGenerateInclude

	if shouldGenerateInclude {
		managedConfigPath := refindParser.GetManagedConfigPath(configPath)

		// If forced generation, use all suitable entries if no other entries exist
		entriesToUse := otherEntries
		if forceGenerateInclude && len(otherEntries) == 0 {
			entriesToUse = sourceEntries
		}

		log.Info().
			Int("entries", len(entriesToUse)).
			Int("snapshots", len(processedSnapshots)).
			Str("config_path", managedConfigPath).
			Bool("forced", forceGenerateInclude).
			Msg("Generating managed rEFInd config")

		// Generate unified config with all entries and snapshots
		if configDiff, err := generator.GenerateManagedConfigDiff(entriesToUse, processedSnapshots, rootFS, managedConfigPath); err != nil {
			log.Error().Err(err).Msg("Failed to generate managed config")
		} else if configDiff != nil {
			unifiedPatch.AddFile(configDiff)
			operationSummary.UpdatedConfigs = append(operationSummary.UpdatedConfigs, configDiff.Path)

			// Since we're adding configs, record that snapshots are being added (avoid duplicates)
			if len(operationSummary.AddedSnapshots) == 0 {
				for _, snapshot := range processedSnapshots {
					snapshotDisplayName := btrfs.FormatSnapshotTimeForMenu(snapshot.SnapshotTime, viper.GetString("advanced.naming.menu_format"), viper.GetBool("display.local_time"))
					operationSummary.AddedSnapshots = append(operationSummary.AddedSnapshots, snapshotDisplayName)
				}
			}

		}
	} else if updatedRefindLinuxConf && len(otherEntries) > 0 {
		if forceGenerateInclude {
			log.Info().
				Int("skipped_entries", len(otherEntries)).
				Msg("Generated include file despite refind_linux.conf updates (forced with --generate-include)")
		} else {
			log.Info().
				Int("skipped_entries", len(otherEntries)).
				Msg("Skipping managed config generation - refind_linux.conf files were updated for this root volume")
		}
	}

	// Record included snapshots (all snapshots selected for this run)
	for _, snapshot := range processedSnapshots {
		snapshotDisplayName := btrfs.FormatSnapshotTimeForMenu(snapshot.SnapshotTime, viper.GetString("advanced.naming.menu_format"), viper.GetBool("display.local_time"))
		operationSummary.IncludedSnapshots = append(operationSummary.IncludedSnapshots, snapshotDisplayName)
	}

	// AddedSnapshots will be populated when configs are actually updated

	// Show unified diff and ask for confirmation
	if len(unifiedPatch.Files) > 0 {
		autoApprove := viper.GetBool("yes")
		if r.IsDryRun() {
			// Always show diff, allow pager only if not auto-approving
			diff.ShowPatchWithPager(unifiedPatch, !autoApprove)
			log.Info().Msg("[DRY RUN] Would apply all changes shown above")
		} else {
			if !autoApprove {
				if !diff.ConfirmPatchChanges(unifiedPatch, false) {
					log.Info().Msg("User declined changes - operation cancelled")
					return nil
				}
			} else {
				// Show diff without pager when auto-approving
				diff.ShowPatchWithPager(unifiedPatch, false)
				log.Info().Msg("Auto-approving all changes")
			}

			// Apply all changes
			if err := applyAllChanges(unifiedPatch, fstabManager, generator, rootFS, processedSnapshots, configPath, r); err != nil {
				return fmt.Errorf("failed to apply changes: %w", err)
			}
		}
	} else {
		log.Info().Msg("No changes needed - configurations are up to date")
	}

	// Log comprehensive summary
	logOperationSummary(operationSummary, r.IsDryRun())

	if r.IsDryRun() {
		log.Info().Msg("Dry run completed - no changes made")
	} else {
		log.Info().Msg("Successfully generated rEFInd snapshot configurations")
	}

	return nil
}

// OperationSummary tracks all operations performed during generation
type OperationSummary struct {
	IncludedSnapshots []string // All snapshots selected for this run
	AddedSnapshots    []string // Snapshots actually added to configs (new ones)
	RemovedSnapshots  []string // Snapshots removed from configs (due to cleanup/age)
	StaleSnapshots    []string // Snapshots detected as stale
	UpdatedFstabs     []string
	UpdatedConfigs    []string
	WritableChanges   []string
}

// logOperationSummary logs a comprehensive summary of all operations
func logOperationSummary(summary *OperationSummary, isDryRun bool) {
	prefix := ""
	if isDryRun {
		prefix = "[DRY RUN] "
	}

	logEntry := log.Info().
		Strs("included_snapshots", summary.IncludedSnapshots).
		Strs("added_snapshots", summary.AddedSnapshots).
		Strs("removed_snapshots", summary.RemovedSnapshots).
		Strs("stale_snapshots", summary.StaleSnapshots).
		Strs("updated_fstabs", summary.UpdatedFstabs).
		Strs("updated_configs", summary.UpdatedConfigs).
		Strs("writable_changes", summary.WritableChanges)

	logEntry.Msg(prefix + "Operation summary")
}

// applyAllChanges applies all collected changes
func applyAllChanges(patch *diff.PatchDiff, fstabManager *fstab.Manager, generator *refind.Generator, rootFS *btrfs.Filesystem, snapshots []*btrfs.Snapshot, configPath string, r runner.Runner) error {
	// Apply fstab changes
	for _, snapshot := range snapshots {
		if err := fstabManager.UpdateSnapshotFstab(snapshot, rootFS, r); err != nil {
			log.Warn().Err(err).Str("snapshot", snapshot.Path).Msg("Failed to update snapshot fstab")
		}
	}

	// Apply all file changes from the patch
	for _, fileDiff := range patch.Files {
		// Create directory if needed
		if err := r.MkdirAll(filepath.Dir(fileDiff.Path), 0755, fmt.Sprintf("Create directory for %s", fileDiff.Path)); err != nil {
			log.Warn().Err(err).Str("path", fileDiff.Path).Msg("Failed to create directory")
			continue
		}

		// Write the file
		if err := r.WriteFile(fileDiff.Path, []byte(fileDiff.Modified), 0644, fmt.Sprintf("Write %s", fileDiff.Path)); err != nil {
			log.Warn().Err(err).Str("path", fileDiff.Path).Msg("Failed to write file")
			continue
		}

		// Log with file type for better categorization
		fileType := getFileType(fileDiff.Path)
		log.Info().Str("path", fileDiff.Path).Str("type", fileType).Msg("Successfully updated file")
	}

	return nil
}

// isBootableEntry determines if a menu entry is suitable for snapshot generation
func isBootableEntry(entry *refind.MenuEntry, rootFS *btrfs.Filesystem) bool {
	// Must have boot options
	if entry.BootOptions == nil {
		log.Trace().Str("title", entry.Title).Msg("Entry rejected: no boot options")
		return false
	}

	// Must have root parameter
	if entry.BootOptions.Root == "" {
		log.Trace().Str("title", entry.Title).Msg("Entry rejected: no root parameter")
		return false
	}

	// Must have subvol or subvolid in rootflags
	if entry.BootOptions.Subvol == "" && entry.BootOptions.SubvolID == "" {
		log.Trace().
			Str("title", entry.Title).
			Str("subvol", entry.BootOptions.Subvol).
			Str("subvolid", entry.BootOptions.SubvolID).
			Msg("Entry rejected: no subvol or subvolid")
		return false
	}

	// Check if root parameter matches our filesystem using any available identifier
	if !rootFS.MatchesDevice(entry.BootOptions.Root) {
		log.Trace().
			Str("title", entry.Title).
			Str("entry_root", entry.BootOptions.Root).
			Str("rootfs_device", rootFS.Device).
			Str("rootfs_uuid", rootFS.UUID).
			Msg("Entry rejected: device mismatch")
		return false
	}

	// Also verify that the subvolume matches our root subvolume
	if rootFS.Subvolume != nil {
		// Check if subvol matches - normalize paths by removing leading slash for comparison
		if entry.BootOptions.Subvol != "" {
			entrySubvol := strings.TrimPrefix(entry.BootOptions.Subvol, "/")
			rootFSSubvol := strings.TrimPrefix(rootFS.Subvolume.Path, "/")
			if entrySubvol != rootFSSubvol {
				log.Trace().
					Str("title", entry.Title).
					Str("entry_subvol", entry.BootOptions.Subvol).
					Str("entry_subvol_normalized", entrySubvol).
					Str("rootfs_subvol", rootFS.Subvolume.Path).
					Str("rootfs_subvol_normalized", rootFSSubvol).
					Msg("Entry rejected: subvol mismatch")
				return false
			}
		}
		// Check if subvolid matches
		if entry.BootOptions.SubvolID != "" {
			if subvolID, err := strconv.ParseUint(entry.BootOptions.SubvolID, 10, 64); err == nil {
				if subvolID != rootFS.Subvolume.ID {
					log.Trace().
						Str("title", entry.Title).
						Uint64("entry_subvolid", subvolID).
						Uint64("rootfs_subvolid", rootFS.Subvolume.ID).
						Msg("Entry rejected: subvolid mismatch")
					return false
				}
			}
		}
	}

	log.Debug().
		Str("title", entry.Title).
		Str("root", entry.BootOptions.Root).
		Str("subvol", entry.BootOptions.Subvol).
		Str("subvolid", entry.BootOptions.SubvolID).
		Msg("Entry accepted as bootable")
	return true
}

// checkRootPrivileges checks if the current user has root privileges
func checkRootPrivileges() error {
	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}

	if currentUser.Uid != "0" {
		return fmt.Errorf("not running as root (UID: %s)", currentUser.Uid)
	}

	return nil
}

// getFileType determines the type of file based on its path
func getFileType(path string) string {
	if strings.HasSuffix(path, "/etc/fstab") {
		return "fstab"
	}
	if strings.HasSuffix(path, "refind.conf") {
		return "refind_config"
	}
	if strings.HasSuffix(path, "refind_linux.conf") {
		return "refind_linux"
	}
	if strings.HasSuffix(path, "refind-btrfs-snapshots.conf") {
		return "refind_include"
	}
	if strings.Contains(path, "/EFI/") && strings.HasSuffix(path, ".conf") {
		return "refind_config"
	}
	return "unknown"
}
