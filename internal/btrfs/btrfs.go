package btrfs

import (
	"bufio"
	"context"
	"encoding/xml"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/esp"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/runner"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

// Filesystem represents a btrfs filesystem
type Filesystem struct {
	UUID       string      `json:"uuid"`
	PartUUID   string      `json:"partuuid,omitempty"`
	Label      string      `json:"label,omitempty"`
	PartLabel  string      `json:"partlabel,omitempty"`
	Device     string      `json:"device"`
	MountPoint string      `json:"mountpoint"`
	Subvolume  *Subvolume  `json:"subvolume,omitempty"`
	Snapshots  []*Snapshot `json:"snapshots,omitempty"`
}

// Subvolume represents a btrfs subvolume
type Subvolume struct {
	ID          uint64    `json:"id"`
	Path        string    `json:"path"`
	ParentID    uint64    `json:"parent_id"`
	Generation  uint64    `json:"generation"`
	CreatedTime time.Time `json:"created_time"`
	IsSnapshot  bool      `json:"is_snapshot"`
	IsReadOnly  bool      `json:"is_readonly"`
}

// Snapshot represents a btrfs snapshot
type Snapshot struct {
	*Subvolume
	OriginalPath   string    `json:"original_path"`
	FilesystemPath string    `json:"filesystem_path"` // Path on filesystem for btrfs commands and file access
	SnapshotTime   time.Time `json:"snapshot_time"`
	Description    string    `json:"description,omitempty"`
	SnapperNum     int       `json:"snapper_num,omitempty"`
	SnapperType    string    `json:"snapper_type,omitempty"`
}

// SnapperInfo represents the snapper info.xml file structure
type SnapperInfo struct {
	XMLName     xml.Name `xml:"snapshot"`
	Type        string   `xml:"type"`
	Num         int      `xml:"num"`
	Date        string   `xml:"date"`
	Description string   `xml:"description"`
	Cleanup     string   `xml:"cleanup"`
}

// MountInfo represents a mounted filesystem
type MountInfo struct {
	Device     string
	Mountpoint string
	Fstype     string
	UUID       string
	PartUUID   string
	Label      string
	PartLabel  string
}

// DeviceIdentifiers holds various ways to identify a device
// Manager handles btrfs filesystem operations
type Manager struct {
	searchDirs []string
	maxDepth   int
}

// NewManager creates a new btrfs manager
func NewManager(searchDirs []string, maxDepth int) *Manager {
	return &Manager{
		searchDirs: searchDirs,
		maxDepth:   maxDepth,
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
	sort.Slice(allSnapshots, func(i, j int) bool {
		return allSnapshots[i].SnapshotTime.After(allSnapshots[j].SnapshotTime)
	})

	log.Debug().Int("count", len(allSnapshots)).Str("filesystem", fs.GetBestIdentifier()).Str("id_type", fs.GetIdentifierType()).Msg("Found snapshots")
	return allSnapshots, nil
}

// MakeSnapshotWritable changes a snapshot's read-only property to false
func (m *Manager) MakeSnapshotWritable(snapshot *Snapshot, r runner.Runner) error {
	if snapshot == nil || snapshot.Subvolume == nil {
		return fmt.Errorf("invalid snapshot provided")
	}

	err := r.Command("btrfs", []string{"property", "set", snapshot.FilesystemPath, "ro", "false"},
		fmt.Sprintf("Make snapshot writable: %s", snapshot.Path))
	if err != nil {
		return fmt.Errorf("failed to make snapshot writable: %w", err)
	}

	// Update the snapshot's read-only flag (only if not dry-run)
	if !r.IsDryRun() {
		snapshot.IsReadOnly = false
	}

	return nil
}

// MakeSnapshotReadOnly changes a snapshot's read-only property to true
func (m *Manager) MakeSnapshotReadOnly(snapshot *Snapshot, r runner.Runner) error {
	if snapshot == nil || snapshot.Subvolume == nil {
		return fmt.Errorf("invalid snapshot provided")
	}

	err := r.Command("btrfs", []string{"property", "set", snapshot.FilesystemPath, "ro", "true"},
		fmt.Sprintf("Make snapshot read-only: %s", snapshot.Path))
	if err != nil {
		return fmt.Errorf("failed to make snapshot read-only: %w", err)
	}

	// Update the snapshot's read-only flag (only if not dry-run)
	if !r.IsDryRun() {
		snapshot.IsReadOnly = true
	}

	return nil
}

// CreateWritableSnapshot creates a writable snapshot from a read-only snapshot
func (m *Manager) CreateWritableSnapshot(snapshot *Snapshot, destDir string, r runner.Runner) (*Snapshot, error) {
	if snapshot == nil || snapshot.Subvolume == nil {
		return nil, fmt.Errorf("invalid snapshot provided")
	}

	// Generate snapshot name using configured timestamp format
	timestampFormat := viper.GetString("advanced.naming.timestamp_format")
	if timestampFormat == "" {
		timestampFormat = "2006-01-02_15-04-05" // Default format
	}
	snapshotName := fmt.Sprintf("rwsnap_%s_ID%d",
		snapshot.SnapshotTime.Format(timestampFormat),
		snapshot.ID)
	destPath := filepath.Join(destDir, snapshotName)

	// Create destination directory
	if err := r.MkdirAll(destDir, 0755, fmt.Sprintf("Create writable snapshot directory: %s", destDir)); err != nil {
		return nil, fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Check if destination already exists (only for real runs)
	if !r.IsDryRun() {
		if _, err := os.Stat(destPath); err == nil {
			return nil, fmt.Errorf("destination path already exists: %s", destPath)
		}
	}

	// Create btrfs snapshot
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

// IsSnapshotBoot checks if the current boot is from a snapshot
func (m *Manager) IsSnapshotBoot() (bool, error) {
	rootFS, err := m.GetRootFilesystem()
	if err != nil {
		return false, err
	}

	return m.IsSnapshotBootFromRootFS(rootFS), nil
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

// GetBestIdentifier returns the best available identifier for the filesystem (UUID > PartUUID > Label > PartLabel > Device)
func (f *Filesystem) GetBestIdentifier() string {
	identifiers := esp.DeviceIdentifiers{
		UUID:      f.UUID,
		PartUUID:  f.PartUUID,
		Label:     f.Label,
		PartLabel: f.PartLabel,
		Device:    f.Device,
	}

	return identifiers.GetBestIdentifier()
}

// GetIdentifierType returns the type of the best available identifier
func (f *Filesystem) GetIdentifierType() string {
	identifiers := esp.DeviceIdentifiers{
		UUID:      f.UUID,
		PartUUID:  f.PartUUID,
		Label:     f.Label,
		PartLabel: f.PartLabel,
		Device:    f.Device,
	}

	return identifiers.GetIdentifierType()
}

// MatchesDevice checks if a device specification matches this filesystem using any available identifier
func (f *Filesystem) MatchesDevice(device string) bool {
	identifiers := esp.DeviceIdentifiers{
		UUID:      f.UUID,
		PartUUID:  f.PartUUID,
		Label:     f.Label,
		PartLabel: f.PartLabel,
		Device:    f.Device,
	}

	return identifiers.Matches(device)
}

// getRootSubvolume gets information about the root subvolume of a filesystem
func (m *Manager) getRootSubvolume(mountpoint string) (*Subvolume, error) {
	// Check if btrfs command is available
	if _, err := exec.LookPath("btrfs"); err != nil {
		return nil, fmt.Errorf("btrfs command not found: %w", err)
	}

	// Get the subvolume ID of the root subvolume
	cmd := exec.Command("btrfs", "subvolume", "show", mountpoint)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get subvolume info: %w", err)
	}

	return m.parseSubvolumeShow(string(output))
}

// getSubvolumeInfo gets detailed information about a subvolume
func (m *Manager) getSubvolumeInfo(path string) (*Subvolume, error) {
	cmd := exec.Command("btrfs", "subvolume", "show", path)
	output, err := cmd.Output()
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
	if _, err := os.Stat(dir); os.IsNotExist(err) {
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

						// Try to parse snapper info.xml for additional metadata
						if snapperInfo, err := m.parseSnapperInfo(entryPath); err == nil {
							// Use snapper timestamp if available
							if snapperTime, err := m.getSnapperTimestamp(snapperInfo.Date); err == nil {
								snapshot.SnapshotTime = snapperTime
							}
							// Add snapper metadata
							snapshot.Description = snapperInfo.Description
							snapshot.SnapperNum = snapperInfo.Num
							snapshot.SnapperType = snapperInfo.Type

							log.Debug().
								Str("path", snapperSnapshotPath).
								Str("description", snapshot.Description).
								Int("snapper_num", snapshot.SnapperNum).
								Time("snapper_time", snapshot.SnapshotTime).
								Msg("Found snapper snapshot")
						}

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

			// Try to parse snapper info.xml for additional metadata
			if snapperInfo, err := m.parseSnapperInfo(entryPath); err == nil {
				// Use snapper timestamp if available
				if snapperTime, err := m.getSnapperTimestamp(snapperInfo.Date); err == nil {
					snapshot.SnapshotTime = snapperTime
				}
				// Add snapper metadata
				snapshot.Description = snapperInfo.Description
				snapshot.SnapperNum = snapperInfo.Num
				snapshot.SnapperType = snapperInfo.Type

				log.Debug().
					Str("path", entryPath).
					Str("description", snapshot.Description).
					Int("snapper_num", snapshot.SnapperNum).
					Time("snapper_time", snapshot.SnapshotTime).
					Msg("Found snapper metadata")
			} else {
				log.Debug().Err(err).Str("path", entryPath).Msg("No snapper info.xml found, using file timestamp")
			}

			snapshots = append(snapshots, snapshot)
		}
	}

	return snapshots, nil
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

// GetSnapshotSize calculates the size of a snapshot using the most efficient method available
func GetSnapshotSize(path string) (string, error) {
	return GetSnapshotSizeWithProgress(path, 0, 0)
}

// GetSnapshotSizeWithProgress calculates the size of a snapshot with progress indication
func GetSnapshotSizeWithProgress(path string, current, total int) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path cannot be empty")
	}

	// Check if path exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", fmt.Errorf("path does not exist: %s", path)
	}

	// Try qgroups first (fast) - only if quotas are already enabled
	if size, err := getSnapshotSizeFromQgroups(path); err == nil {
		return size, nil
	}

	// Fallback to native Go calculation with progress (slow but accurate)
	return getSnapshotSizeNativeWithProgress(path, current, total)
}

// GetSnapshotSizeWithoutProgress calculates the size of a snapshot using external file counter
func GetSnapshotSizeWithoutProgress(path string, fileCount *int64) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path cannot be empty")
	}

	// Check if path exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", fmt.Errorf("path does not exist: %s", path)
	}

	// Try qgroups first (fast) - only if quotas are already enabled
	if size, err := getSnapshotSizeFromQgroups(path); err == nil {
		return size, nil
	}

	// Fallback to native Go calculation without internal progress (slow but accurate)
	return getSnapshotSizeNativeExternal(path, fileCount)
}

// getSnapshotSizeFromQgroups tries to get snapshot size using btrfs qgroups (fast)
// Only attempts if quotas are already enabled
func getSnapshotSizeFromQgroups(path string) (string, error) {
	// First, check if quotas are enabled by checking filesystem features
	// This is faster than trying qgroup show and getting an error
	cmd := exec.Command("btrfs", "filesystem", "show")
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("btrfs not available")
	}

	// Quick check: try to list qgroups without stderr to avoid noise
	cmd = exec.Command("btrfs", "qgroup", "show", path)
	output, err := cmd.Output()
	if err != nil {
		// Quotas not enabled - this is expected and not an error we should log
		return "", fmt.Errorf("quotas not enabled")
	}

	// Check for inconsistent qgroup data warning
	outputStr := string(output)
	if strings.Contains(outputStr, "qgroup data inconsistent") || strings.Contains(outputStr, "0.00B") {
		return "", fmt.Errorf("qgroup data inconsistent or incomplete")
	}

	// Get the subvolume ID for this path
	subvolCmd := exec.Command("btrfs", "subvolume", "show", path)
	subvolOutput, err := subvolCmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get subvolume info: %w", err)
	}

	// Parse subvolume ID from output
	subvolID := ""
	lines := strings.Split(string(subvolOutput), "\n")
	for _, line := range lines {
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

	// Parse qgroup output to find our subvolume's exclusive size
	qgroupLines := strings.Split(string(output), "\n")
	for _, line := range qgroupLines {
		if strings.Contains(line, "0/"+subvolID) {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				// Return exclusive size (3rd column)
				return parts[2], nil
			}
		}
	}

	return "", fmt.Errorf("subvolume not found in qgroups")
}

// getSnapshotSizeNativeWithProgress calculates snapshot size using native Go with progress indication
func getSnapshotSizeNativeWithProgress(path string, current, total int) (string, error) {
	var totalSize int64
	var fileCount int64

	// Start a goroutine for progress indication
	done := make(chan struct{})
	defer close(done)

	go showProgressWithSnapshot(&fileCount, current, total, done)

	// Use timeout to prevent hanging on large snapshots
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	start := time.Now()
	err := filepath.WalkDir(path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Skip inaccessible files/directories instead of failing
			return nil
		}

		// Check for timeout
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
		atomic.AddInt64(&fileCount, 1)
		return nil
	})

	// Clear progress line only at the end
	fmt.Print("\r\033[K")

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "timeout", nil
		}
		return "", fmt.Errorf("failed to calculate size: %w", err)
	}

	duration := time.Since(start)
	log.Debug().
		Int64("total_size", totalSize).
		Int64("file_count", fileCount).
		Dur("duration", duration).
		Str("path", path).
		Msg("Completed size calculation")

	return formatBytes(totalSize), nil
}

// getSnapshotSizeNativeExternal calculates snapshot size using native Go with external file counter
func getSnapshotSizeNativeExternal(path string, externalFileCount *int64) (string, error) {
	var totalSize int64

	// Use timeout to prevent hanging on large snapshots
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	start := time.Now()
	err := filepath.WalkDir(path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Skip inaccessible files/directories instead of failing
			return nil
		}

		// Check for timeout
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

	duration := time.Since(start)
	log.Debug().
		Int64("total_size", totalSize).
		Int64("file_count", atomic.LoadInt64(externalFileCount)).
		Dur("duration", duration).
		Str("path", path).
		Msg("Completed size calculation")

	return formatBytes(totalSize), nil
}

// showProgressWithSnapshot displays a rotating spinner with file count and snapshot progress
func showProgressWithSnapshot(fileCount *int64, current, total int, done chan struct{}) {
	spinner := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	i := 0

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			count := atomic.LoadInt64(fileCount)
			if total > 0 {
				fmt.Printf("\r%s Calculating snapshot sizes... (%d/%d snapshots, %d files in current)",
					spinner[i%len(spinner)], current, total, count)
			} else {
				fmt.Printf("\r%s Calculating snapshot size... (%d files processed)",
					spinner[i%len(spinner)], count)
			}
			i++
		}
	}
}

// formatBytes converts bytes to human-readable format
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}

	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	if exp >= len(units) {
		exp = len(units) - 1
		div = int64(1)
		for i := 0; i < exp; i++ {
			div *= unit
		}
	}

	return fmt.Sprintf("%.1f %s", float64(bytes)/float64(div), units[exp])
}

// CleanupOldSnapshots removes old writable snapshots from the destination directory
func (m *Manager) CleanupOldSnapshots(destDir string, keepCount int, r runner.Runner) error {
	if keepCount < 0 {
		return fmt.Errorf("keepCount must be non-negative")
	}

	log.Debug().Str("dest_dir", destDir).Int("keep_count", keepCount).Msg("Cleaning up old snapshots")

	entries, err := os.ReadDir(destDir)
	if err != nil {
		if os.IsNotExist(err) {
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
	sort.Strings(snapshots)

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
func (m *Manager) getSnapperTimestamp(dateStr string) (time.Time, error) {
	// Snapper uses format: "2025-06-11 09:00:11"
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04:05 -0700",
		time.RFC3339,
	}

	for _, layout := range layouts {
		if t, err := time.Parse(layout, dateStr); err == nil {
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
