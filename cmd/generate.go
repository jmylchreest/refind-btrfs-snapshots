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

		// Validate ESP access using the detected ESP
		detector := esp.NewESPDetector("")
		if err := detector.ValidateESPAccess(); err != nil {
			return fmt.Errorf("ESP validation failed: %w", err)
		}
	} else if viper.GetString("esp.mount_point") != "" {
		espPath = viper.GetString("esp.mount_point")
		log.Info().Str("path", espPath).Msg("Using configured ESP path")

		// Validate manually configured ESP path
		detector := esp.NewESPDetector(espPath)
		if err := detector.ValidateESPAccess(); err != nil {
			return fmt.Errorf("ESP validation failed: %w", err)
		}
	} else {
		return fmt.Errorf("ESP path not configured and auto-detection disabled")
	}

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

	// Collect all changes in a unified patch
	unifiedPatch := diff.NewPatchDiff()
	operationSummary := &OperationSummary{
		IncludedSnapshots: make([]string, 0),
		AddedSnapshots:    make([]string, 0),
		RemovedSnapshots:  make([]string, 0),
		UpdatedFstabs:     make([]string, 0),
		UpdatedConfigs:    make([]string, 0),
		WritableChanges:   make([]string, 0),
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
	refindParser := refind.NewParser(espPath)

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
	generator := refind.NewGenerator(espPath)

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
		// Only process original entries that have rootflags=subvol=@ (not our generated snapshot entries)
		if entry.BootOptions != nil && entry.BootOptions.Subvol == "@" {
			refindLinuxFiles[entry.SourceFile] = append(refindLinuxFiles[entry.SourceFile], entry)
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
				snapshotDisplayName := snapshot.SnapshotTime.Format("2006-01-02_15-04-05")
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
					snapshotDisplayName := snapshot.SnapshotTime.Format("2006-01-02_15-04-05")
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
		snapshotDisplayName := snapshot.SnapshotTime.Format("2006-01-02_15-04-05")
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

		log.Info().Str("path", fileDiff.Path).Msg("Successfully updated file")
	}

	return nil
}

// isBootableEntry determines if a menu entry is suitable for snapshot generation
func isBootableEntry(entry *refind.MenuEntry, rootFS *btrfs.Filesystem) bool {
	// Must have boot options
	if entry.BootOptions == nil {
		return false
	}

	// Must have root parameter
	if entry.BootOptions.Root == "" {
		return false
	}

	// Must have subvol or subvolid in rootflags
	if entry.BootOptions.Subvol == "" && entry.BootOptions.SubvolID == "" {
		return false
	}

	// Check if root parameter matches our filesystem using any available identifier
	if !rootFS.MatchesDevice(entry.BootOptions.Root) {
		return false
	}

	// Also verify that the subvolume matches our root subvolume
	if rootFS.Subvolume != nil {
		// Check if subvol matches
		if entry.BootOptions.Subvol != "" && entry.BootOptions.Subvol != rootFS.Subvolume.Path {
			return false
		}
		// Check if subvolid matches
		if entry.BootOptions.SubvolID != "" {
			if subvolID, err := strconv.ParseUint(entry.BootOptions.SubvolID, 10, 64); err == nil {
				if subvolID != rootFS.Subvolume.ID {
					return false
				}
			}
		}
	}

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
