package btrfs

import (
	"bufio"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/esp"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/runner"
	"github.com/rs/zerolog/log"
)

// Manager handles btrfs filesystem operations
type Manager struct {
	searchDirs     []string
	maxDepth       int
	rwsnapFormat   string
	useLocalTime   bool
}

// NewManager creates a new btrfs manager.
// rwsnapFormat is the time.Format layout used for naming writable snapshot
// copies (e.g. "2006-01-02_15-04-05"); useLocalTime renders the timestamp
// in local time instead of UTC.
func NewManager(searchDirs []string, maxDepth int, rwsnapFormat string, useLocalTime bool) *Manager {
	if rwsnapFormat == "" {
		rwsnapFormat = "2006-01-02_15-04-05"
	}
	return &Manager{
		searchDirs:   searchDirs,
		maxDepth:     maxDepth,
		rwsnapFormat: rwsnapFormat,
		useLocalTime: useLocalTime,
	}
}

// DetectBtrfsFilesystems discovers all btrfs filesystems on the system
func (m *Manager) DetectBtrfsFilesystems() ([]*Filesystem, error) {
	log.Debug().Msg("Detecting btrfs filesystems")

	// Get mounted filesystem information
	mounts, err := m.getMountedFilesystems()
	if err != nil {
		return nil, fmt.Errorf("failed to get mounted filesystems: %w", err)
	}

	var filesystems []*Filesystem

	// Find btrfs filesystems
	for _, mount := range mounts {
		if mount.Fstype == "btrfs" {
			fs := &Filesystem{
				UUID:       mount.UUID,
				PartUUID:   mount.PartUUID,
				Label:      mount.Label,
				PartLabel:  mount.PartLabel,
				Device:     mount.Device,
				MountPoint: mount.Mountpoint,
			}

			// Get root subvolume information
			subvol, err := m.getRootSubvolume(mount.Mountpoint)
			if err != nil {
				log.Warn().Err(err).Str("mountpoint", mount.Mountpoint).Msg("Failed to get root subvolume")
				// Continue with filesystem but without subvolume info for now
				// This allows the tool to work even when btrfs subvolume show fails
			} else {
				fs.Subvolume = subvol
			}

			filesystems = append(filesystems, fs)
		}
	}

	log.Info().Int("count", len(filesystems)).Msg("Found btrfs filesystems")
	return filesystems, nil
}

// FindSnapshots finds all snapshots for the given filesystem
func (m *Manager) FindSnapshots(fs *Filesystem) ([]*Snapshot, error) {
	log.Debug().Str("filesystem", fs.GetBestIdentifier()).Str("id_type", fs.GetIdentifierType()).Msg("Finding snapshots")

	var allSnapshots []*Snapshot

	for _, searchDir := range m.searchDirs {
		// Convert relative paths to absolute paths based on mountpoint
		searchPath := searchDir
		if !filepath.IsAbs(searchPath) {
			searchPath = filepath.Join(fs.MountPoint, searchDir)
		}

		snapshots, err := m.findSnapshotsInDir(searchPath, fs, 0)
		if err != nil {
			log.Warn().Err(err).Str("search_dir", searchPath).Msg("Failed to find snapshots in directory")
			continue
		}

		allSnapshots = append(allSnapshots, snapshots...)
	}

	// Sort snapshots by creation time (newest first)
	slices.SortFunc(allSnapshots, func(a, b *Snapshot) int {
		return b.SnapshotTime.Compare(a.SnapshotTime)
	})

	log.Debug().Int("count", len(allSnapshots)).Str("filesystem", fs.GetBestIdentifier()).Str("id_type", fs.GetIdentifierType()).Msg("Found snapshots")
	return allSnapshots, nil
}

// MakeSnapshotWritable changes a snapshot's read-only property to false
func (m *Manager) MakeSnapshotWritable(snapshot *Snapshot, r runner.Runner) error {
	return m.setSnapshotReadOnly(snapshot, false, r)
}

// MakeSnapshotReadOnly changes a snapshot's read-only property to true
func (m *Manager) MakeSnapshotReadOnly(snapshot *Snapshot, r runner.Runner) error {
	return m.setSnapshotReadOnly(snapshot, true, r)
}

// setSnapshotReadOnly sets the snapshot's read-only property
func (m *Manager) setSnapshotReadOnly(snapshot *Snapshot, readOnly bool, r runner.Runner) error {
	if snapshot == nil || snapshot.Subvolume == nil {
		return fmt.Errorf("invalid snapshot provided")
	}

	roValue := "false"
	desc := "writable"
	if readOnly {
		roValue = "true"
		desc = "read-only"
	}

	err := r.Command("btrfs", []string{"property", "set", snapshot.FilesystemPath, "ro", roValue},
		fmt.Sprintf("Make snapshot %s: %s", desc, snapshot.Path))
	if err != nil {
		return fmt.Errorf("failed to make snapshot %s: %w", desc, err)
	}

	if !r.IsDryRun() {
		snapshot.IsReadOnly = readOnly
	}

	return nil
}

// CreateWritableSnapshot creates a writable snapshot from a read-only snapshot
func (m *Manager) CreateWritableSnapshot(snapshot *Snapshot, destDir string, r runner.Runner) (*Snapshot, error) {
	if snapshot == nil || snapshot.Subvolume == nil {
		return nil, fmt.Errorf("invalid snapshot provided")
	}

	formattedTime := FormatSnapshotTimeForRwsnap(snapshot.SnapshotTime, m.rwsnapFormat, m.useLocalTime)
	snapshotName := fmt.Sprintf("rwsnap_%s_ID%d", formattedTime, snapshot.ID)
	destPath := filepath.Join(destDir, snapshotName)

	// Create destination directory
	if err := r.MkdirAll(destDir, 0755, fmt.Sprintf("Create writable snapshot directory: %s", destDir)); err != nil {
		return nil, fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Create btrfs snapshot (btrfs will fail atomically if destPath exists)
	err := r.Command("btrfs", []string{"subvolume", "snapshot", snapshot.Path, destPath},
		fmt.Sprintf("Create writable snapshot: %s -> %s", snapshot.Path, destPath))
	if err != nil {
		return nil, fmt.Errorf("failed to create writable snapshot: %w", err)
	}

	// For dry-run, return a copy of the original snapshot with updated path
	if r.IsDryRun() {
		writable := &Snapshot{
			Subvolume:    snapshot.Subvolume,
			OriginalPath: snapshot.Path,
			SnapshotTime: snapshot.SnapshotTime,
		}
		writable.Path = destPath
		return writable, nil
	}

	// Get new snapshot information (real run only)
	newSnapshot, err := m.getSubvolumeInfo(destPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get new snapshot info: %w", err)
	}

	writable := &Snapshot{
		Subvolume:    newSnapshot,
		OriginalPath: snapshot.Path,
		SnapshotTime: snapshot.SnapshotTime,
	}

	return writable, nil
}

// GetRootFilesystem finds the filesystem that contains the root mount point
func (m *Manager) GetRootFilesystem() (*Filesystem, error) {
	filesystems, err := m.DetectBtrfsFilesystems()
	if err != nil {
		return nil, err
	}

	for _, fs := range filesystems {
		if fs.MountPoint == "/" {
			return fs, nil
		}
	}

	return nil, fmt.Errorf("no btrfs filesystem mounted at root")
}

// IsSnapshotBootFromRootFS checks if we're booted from a snapshot using an existing root filesystem
func (m *Manager) IsSnapshotBootFromRootFS(rootFS *Filesystem) bool {
	if rootFS.Subvolume == nil {
		return false
	}

	// Check if the root subvolume path indicates it's a snapshot
	// This is a heuristic check - snapshots typically have specific naming patterns
	path := rootFS.Subvolume.Path
	return strings.Contains(path, "snapshot") ||
		strings.Contains(path, "rwsnap") ||
		rootFS.Subvolume.IsSnapshot
}

// getMountedFilesystems gets mounted filesystem information from /proc/mounts
func (m *Manager) getMountedFilesystems() ([]*MountInfo, error) {
	file, err := os.Open("/proc/mounts")
	if err != nil {
		return nil, fmt.Errorf("failed to open /proc/mounts: %w", err)
	}
	defer file.Close()

	var mounts []*MountInfo
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		device := fields[0]
		mountpoint := fields[1]
		fstype := fields[2]

		// Skip non-btrfs filesystems
		if fstype != "btrfs" {
			continue
		}

		// Get device identifiers for btrfs filesystems
		identifiers := m.getDeviceIdentifiers(device)

		mount := &MountInfo{
			Device:     device,
			Mountpoint: mountpoint,
			Fstype:     fstype,
			UUID:       identifiers.UUID,
			PartUUID:   identifiers.PartUUID,
			Label:      identifiers.Label,
			PartLabel:  identifiers.PartLabel,
		}

		mounts = append(mounts, mount)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read /proc/mounts: %w", err)
	}

	return mounts, nil
}

// getDeviceIdentifiers gets various identifiers for a device using /dev/disk/by-* directories
func (m *Manager) getDeviceIdentifiers(device string) *esp.DeviceIdentifiers {
	identifiers := &esp.DeviceIdentifiers{}

	// Get the real device path by resolving any symlinks
	realDevice, err := filepath.EvalSymlinks(device)
	if err != nil {
		realDevice = device
	}

	// Check /dev/disk/by-uuid/
	identifiers.UUID = m.findIdentifierInDir("/dev/disk/by-uuid", realDevice)

	// Check /dev/disk/by-partuuid/
	identifiers.PartUUID = m.findIdentifierInDir("/dev/disk/by-partuuid", realDevice)

	// Check /dev/disk/by-label/
	identifiers.Label = m.findIdentifierInDir("/dev/disk/by-label", realDevice)

	// Check /dev/disk/by-partlabel/
	identifiers.PartLabel = m.findIdentifierInDir("/dev/disk/by-partlabel", realDevice)

	return identifiers
}

// findIdentifierInDir searches for a device in a /dev/disk/by-* directory and returns the identifier
func (m *Manager) findIdentifierInDir(byDir, targetDevice string) string {
	entries, err := os.ReadDir(byDir)
	if err != nil {
		return ""
	}

	for _, entry := range entries {
		linkPath := filepath.Join(byDir, entry.Name())
		linkedDevice, err := filepath.EvalSymlinks(linkPath)
		if err != nil {
			continue
		}

		// Compare the resolved paths
		if linkedDevice == targetDevice {
			return entry.Name()
		}
	}

	return ""
}

// deviceIdentifiers returns the DeviceIdentifiers for this filesystem.
func (f *Filesystem) deviceIdentifiers() *esp.DeviceIdentifiers {
	return &esp.DeviceIdentifiers{
		UUID:      f.UUID,
		PartUUID:  f.PartUUID,
		Label:     f.Label,
		PartLabel: f.PartLabel,
		Device:    f.Device,
	}
}

// GetBestIdentifier returns the best available identifier for the filesystem (UUID > PartUUID > Label > PartLabel > Device)
func (f *Filesystem) GetBestIdentifier() string {
	return f.deviceIdentifiers().GetBestIdentifier()
}

// GetIdentifierType returns the type of the best available identifier
func (f *Filesystem) GetIdentifierType() string {
	return f.deviceIdentifiers().GetIdentifierType()
}

// MatchesDevice checks if a device specification matches this filesystem using any available identifier
func (f *Filesystem) MatchesDevice(device string) bool {
	return f.deviceIdentifiers().Matches(device)
}

// getRootSubvolume gets information about the root subvolume of a filesystem
func (m *Manager) getRootSubvolume(mountpoint string) (*Subvolume, error) {
	if _, err := exec.LookPath("btrfs"); err != nil {
		return nil, fmt.Errorf("btrfs command not found: %w", err)
	}
	return m.runSubvolumeShow(mountpoint)
}

// getSubvolumeInfo gets detailed information about a subvolume
func (m *Manager) getSubvolumeInfo(path string) (*Subvolume, error) {
	return m.runSubvolumeShow(path)
}

// runSubvolumeShow runs `btrfs subvolume show <path>` and parses the output.
// Shared by getRootSubvolume and getSubvolumeInfo to avoid duplicating the
// exec+parse pattern in two places.
func (m *Manager) runSubvolumeShow(path string) (*Subvolume, error) {
	output, err := exec.Command("btrfs", "subvolume", "show", path).Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get subvolume info: %w", err)
	}
	return m.parseSubvolumeShow(string(output))
}

// parseSubvolumeShow parses the output of 'btrfs subvolume show'
func (m *Manager) parseSubvolumeShow(output string) (*Subvolume, error) {
	subvol := &Subvolume{}
	scanner := bufio.NewScanner(strings.NewReader(output))
	lineNum := 0

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		lineNum++

		// First line is often just the subvolume name/path (e.g., "@")
		if lineNum == 1 && !strings.Contains(line, ":") {
			subvol.Path = line
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "Name":
			// Use Name field if Path wasn't set from first line
			if subvol.Path == "" {
				subvol.Path = value
			}
		case "Subvolume ID":
			if id, err := strconv.ParseUint(value, 10, 64); err == nil {
				subvol.ID = id
			}
		case "Path":
			subvol.Path = value
		case "Parent ID":
			if id, err := strconv.ParseUint(value, 10, 64); err == nil {
				subvol.ParentID = id
			}
		case "Generation":
			if gen, err := strconv.ParseUint(value, 10, 64); err == nil {
				subvol.Generation = gen
			}
		case "Flags":
			subvol.IsReadOnly = strings.Contains(value, "readonly")
			subvol.IsSnapshot = strings.Contains(value, "snapshot")
		case "Creation time":
			if t, err := time.Parse("2006-01-02 15:04:05 -0700", value); err == nil {
				subvol.CreatedTime = t
			}
		}
	}

	// Validate that we got essential information
	if subvol.Path == "" || subvol.ID == 0 {
		return nil, fmt.Errorf("failed to parse essential subvolume information")
	}

	return subvol, nil
}

// findSnapshotsInDir recursively finds snapshots in a directory
func (m *Manager) findSnapshotsInDir(dir string, fs *Filesystem, depth int) ([]*Snapshot, error) {
	if depth > m.maxDepth {
		return nil, nil
	}

	var snapshots []*Snapshot

	// Check if directory exists
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return snapshots, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %s: %w", dir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		entryPath := filepath.Join(dir, entry.Name())

		// Check if this is a snapper-style directory (contains "snapshot" subdir and "info.xml")
		snapperSnapshotPath := filepath.Join(entryPath, "snapshot")
		snapperInfoPath := filepath.Join(entryPath, "info.xml")

		if _, err := os.Stat(snapperSnapshotPath); err == nil {
			if _, err := os.Stat(snapperInfoPath); err == nil {
				// This is a snapper directory, check the "snapshot" subdirectory
				subvol, err := m.getSubvolumeInfo(snapperSnapshotPath)
				if err == nil {
					if m.isSnapshotOfRoot(subvol, fs.Subvolume) {
						// Get file info for creation time
						info, err := entry.Info()
						if err != nil {
							log.Warn().Err(err).Str("path", entryPath).Msg("Failed to get file info")
							continue
						}

						snapshot := &Snapshot{
							Subvolume:      subvol,
							OriginalPath:   fs.Subvolume.Path,
							FilesystemPath: snapperSnapshotPath, // filesystem path for btrfs commands
							SnapshotTime:   info.ModTime(),
						}

						// subvol.Path contains the btrfs subvolume path for fstab/boot options
						// FilesystemPath contains the filesystem path for btrfs commands and file access

						m.applySnapperMetadata(snapshot, entryPath)
						snapshots = append(snapshots, snapshot)
						continue
					}
				}
			}
		}

		// Check if this is a direct snapshot by trying to get subvolume info
		subvol, err := m.getSubvolumeInfo(entryPath)
		if err != nil {
			// Not a subvolume, continue searching recursively
			if depth < m.maxDepth {
				subSnapshots, err := m.findSnapshotsInDir(entryPath, fs, depth+1)
				if err != nil {
					log.Warn().Err(err).Str("path", entryPath).Msg("Failed to search subdirectory")
					continue
				}
				snapshots = append(snapshots, subSnapshots...)
			}
			continue
		}

		// Check if this subvolume is related to our root filesystem
		isSnapshot := m.isSnapshotOfRoot(subvol, fs.Subvolume)
		log.Debug().
			Str("path", entryPath).
			Str("subvol_path", subvol.Path).
			Bool("is_snapshot_flag", subvol.IsSnapshot).
			Bool("is_valid_snapshot", isSnapshot).
			Uint64("subvol_id", subvol.ID).
			Uint64("parent_id", subvol.ParentID).
			Msg("Evaluated potential snapshot")

		if isSnapshot {
			// Get file info for creation time
			info, err := entry.Info()
			if err != nil {
				log.Warn().Err(err).Str("path", entryPath).Msg("Failed to get file info")
				continue
			}

			snapshot := &Snapshot{
				Subvolume:      subvol,
				OriginalPath:   fs.Subvolume.Path,
				FilesystemPath: entryPath, // filesystem path for btrfs commands
				SnapshotTime:   info.ModTime(),
			}

			// subvol.Path contains the btrfs subvolume path for fstab/boot options
			// FilesystemPath contains the filesystem path for btrfs commands and file access

			m.applySnapperMetadata(snapshot, entryPath)
			snapshots = append(snapshots, snapshot)
		}
	}

	return snapshots, nil
}

// applySnapperMetadata enriches a snapshot with metadata from snapper's info.xml if available.
func (m *Manager) applySnapperMetadata(snapshot *Snapshot, entryPath string) {
	snapperInfo, err := m.parseSnapperInfo(entryPath)
	if err != nil {
		log.Debug().Err(err).Str("path", entryPath).Msg("No snapper info.xml found, using file timestamp")
		return
	}
	if snapperTime, err := m.getSnapperTimestamp(snapperInfo.Date); err == nil {
		snapshot.SnapshotTime = snapperTime
	}
	snapshot.Description = snapperInfo.Description
	snapshot.SnapperNum = snapperInfo.Num
	snapshot.SnapperType = snapperInfo.Type

	log.Debug().
		Str("path", snapshot.FilesystemPath).
		Str("description", snapshot.Description).
		Int("snapper_num", snapshot.SnapperNum).
		Time("snapper_time", snapshot.SnapshotTime).
		Msg("Found snapper metadata")
}

// isSnapshotOfRoot determines if a subvolume is a snapshot of the root subvolume
func (m *Manager) isSnapshotOfRoot(subvol, root *Subvolume) bool {
	if subvol == nil {
		return false
	}

	// If root subvolume info is missing, be more permissive
	if root == nil {
		// Without root info, use heuristics: if it's marked as snapshot or looks like one
		return subvol.IsSnapshot || m.looksLikeSnapshot(subvol)
	}

	// Primary check: explicitly marked as a snapshot with proper parent relationship
	if subvol.IsSnapshot {
		// Check parent ID relationship - snapshots typically have the root subvolume as parent
		if subvol.ParentID == root.ID {
			return true
		}

		// For nested snapshots, check if parent ID matches root's parent (common case)
		if subvol.ParentID == root.ParentID && root.ParentID != 0 {
			return true
		}
	}

	// Secondary check: use heuristics for snapshots that may not have the flag set correctly
	if m.looksLikeSnapshot(subvol) {
		// Additional validation: check if generation is reasonable for a snapshot
		if root.Generation > 0 && subvol.Generation > 0 && subvol.Generation <= root.Generation {
			return true
		}

		// If generation check isn't conclusive, allow it anyway for compatibility
		return true
	}

	return false
}

// looksLikeSnapshot uses heuristics to identify potential snapshots
func (m *Manager) looksLikeSnapshot(subvol *Subvolume) bool {
	if subvol == nil {
		return false
	}

	// Check if path contains common snapshot directory patterns
	path := strings.ToLower(subvol.Path)
	snapshotPatterns := []string{
		"snapshot", ".snapshot", "snapshots", ".snapshots",
		"backup", "bkp", "@snapshot", "snap",
	}

	for _, pattern := range snapshotPatterns {
		if strings.Contains(path, pattern) {
			return true
		}
	}

	// Check if path is in known snapshot locations relative to search directories
	for _, searchDir := range m.searchDirs {
		if strings.HasPrefix(subvol.Path, searchDir) || strings.HasPrefix(subvol.Path, strings.TrimPrefix(searchDir, "/")) {
			return true
		}
	}

	return false
}

// GetSnapshotFstabPath returns the path to the fstab file in a snapshot
func GetSnapshotFstabPath(snapshot *Snapshot) string {
	return filepath.Join(snapshot.FilesystemPath, "etc", "fstab")
}

// CleanupOldSnapshots removes old writable snapshots from the destination directory
func (m *Manager) CleanupOldSnapshots(destDir string, keepCount int, r runner.Runner) error {
	if keepCount < 0 {
		return fmt.Errorf("keepCount must be non-negative")
	}

	log.Debug().Str("dest_dir", destDir).Int("keep_count", keepCount).Msg("Cleaning up old snapshots")

	entries, err := os.ReadDir(destDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // Directory doesn't exist, nothing to clean
		}
		return fmt.Errorf("failed to read destination directory: %w", err)
	}

	var snapshots []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "rwsnap_") {
			snapshots = append(snapshots, entry.Name())
		}
	}

	// Sort snapshots by name (which includes timestamp)
	slices.Sort(snapshots)

	// Remove old snapshots if we have more than keepCount
	if len(snapshots) > keepCount {
		toRemove := snapshots[:len(snapshots)-keepCount]
		for _, snapshot := range toRemove {
			snapshotPath := filepath.Join(destDir, snapshot)
			log.Info().Str("path", snapshotPath).Msg("Removing old snapshot")

			// Verify it's a btrfs subvolume before attempting deletion
			if _, err := m.getSubvolumeInfo(snapshotPath); err != nil {
				log.Warn().Err(err).Str("path", snapshotPath).Msg("Not a valid subvolume, skipping deletion")
				continue
			}

			// Remove btrfs subvolume
			if err := r.Command("btrfs", []string{"subvolume", "delete", snapshotPath}, "Remove old snapshot"); err != nil {
				log.Warn().Err(err).Str("path", snapshotPath).Msg("Failed to remove old snapshot")
			}
		}
	}

	return nil
}

// parseSnapperInfo reads and parses snapper info.xml file
func (m *Manager) parseSnapperInfo(snapshotDir string) (*SnapperInfo, error) {
	infoPath := filepath.Join(snapshotDir, "info.xml")
	data, err := os.ReadFile(infoPath)
	if err != nil {
		return nil, err
	}

	var info SnapperInfo
	if err := xml.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("failed to parse info.xml: %w", err)
	}

	return &info, nil
}

// getSnapperTimestamp parses snapper date format and returns time.Time
// Times in info.xml are assumed to be in UTC
func (m *Manager) getSnapperTimestamp(dateStr string) (time.Time, error) {
	// Snapper uses format: "2025-06-11 09:00:11"
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04:05 -0700",
		time.RFC3339,
	}

	for _, layout := range layouts {
		if t, err := time.Parse(layout, dateStr); err == nil {
			// If the parsed time has no timezone info (first layout), assume UTC
			if layout == "2006-01-02 15:04:05" {
				return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC), nil
			}
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unable to parse snapper date: %s", dateStr)
}

// CleanupSnapshotWritability ensures only selected snapshots are writable
func (m *Manager) CleanupSnapshotWritability(allSnapshots []*Snapshot, selectedSnapshots []*Snapshot, r runner.Runner) error {
	log.Debug().Int("total", len(allSnapshots)).Int("selected", len(selectedSnapshots)).Msg("Cleaning up snapshot writability")

	// Create a map of selected snapshot paths for quick lookup
	selectedPaths := make(map[string]bool)
	for _, snapshot := range selectedSnapshots {
		selectedPaths[snapshot.Path] = true
	}

	// Make unselected snapshots read-only
	for _, snapshot := range allSnapshots {
		if !selectedPaths[snapshot.Path] && !snapshot.IsReadOnly {
			if err := m.MakeSnapshotReadOnly(snapshot, r); err != nil {
				log.Warn().Err(err).Str("path", snapshot.Path).Msg("Failed to make snapshot read-only")
			}
		}
	}

	return nil
}
