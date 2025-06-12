package fstab

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/diff"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/params"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/runner"
	"github.com/rs/zerolog/log"
)

// Entry represents a single fstab entry
type Entry struct {
	Device     string `json:"device"`
	Mountpoint string `json:"mountpoint"`
	FSType     string `json:"fstype"`
	Options    string `json:"options"`
	Dump       string `json:"dump"`
	Pass       string `json:"pass"`
	Original   string `json:"original"` // Original line for preservation
}

// Fstab represents an fstab file
type Fstab struct {
	Entries []*Entry `json:"entries"`
	Lines   []string `json:"lines"` // All lines including comments
}

// Manager handles fstab operations
type Manager struct{}

// NewManager creates a new fstab manager
func NewManager() *Manager {
	return &Manager{}
}

// ParseFstab parses an fstab file
func (m *Manager) ParseFstab(path string) (*Fstab, error) {
	log.Debug().Str("path", path).Msg("Parsing fstab file")

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open fstab file: %w", err)
	}
	defer file.Close()

	fstab := &Fstab{
		Entries: []*Entry{},
		Lines:   []string{},
	}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		fstab.Lines = append(fstab.Lines, line)

		// Skip empty lines and comments
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		entry := m.parseFstabLine(line)
		if entry != nil {
			fstab.Entries = append(fstab.Entries, entry)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading fstab file: %w", err)
	}

	log.Debug().Int("entries", len(fstab.Entries)).Msg("Parsed fstab file")
	return fstab, nil
}

// parseFstabLine parses a single fstab line
func (m *Manager) parseFstabLine(line string) *Entry {
	// Split on whitespace, handling multiple spaces/tabs
	fields := regexp.MustCompile(`\s+`).Split(strings.TrimSpace(line), -1)

	if len(fields) < 4 {
		return nil
	}

	entry := &Entry{
		Device:     fields[0],
		Mountpoint: fields[1],
		FSType:     fields[2],
		Options:    fields[3],
		Original:   line,
	}

	if len(fields) >= 5 {
		entry.Dump = fields[4]
	} else {
		entry.Dump = "0"
	}

	if len(fields) >= 6 {
		entry.Pass = fields[5]
	} else {
		entry.Pass = "0"
	}

	return entry
}

// UpdateSnapshotFstab updates the fstab file in a snapshot to reflect the snapshot's subvolume
func (m *Manager) UpdateSnapshotFstab(snapshot *btrfs.Snapshot, rootFS *btrfs.Filesystem, r runner.Runner) error {
	return m.updateSnapshotFstab(snapshot, rootFS, r, false)
}

// UpdateSnapshotFstabWithConfirmation shows what would be changed and asks for confirmation
func (m *Manager) UpdateSnapshotFstabWithConfirmation(snapshot *btrfs.Snapshot, rootFS *btrfs.Filesystem, r runner.Runner, autoApprove bool) error {
	return m.updateSnapshotFstab(snapshot, rootFS, r, !autoApprove)
}

// UpdateSnapshotFstabDiff generates a diff for fstab changes without applying them
func (m *Manager) UpdateSnapshotFstabDiff(snapshot *btrfs.Snapshot, rootFS *btrfs.Filesystem) (*diff.FileDiff, error) {
	if snapshot == nil || snapshot.Subvolume == nil {
		return nil, fmt.Errorf("invalid snapshot provided")
	}
	
	fstabPath := btrfs.GetSnapshotFstabPath(snapshot)
	log.Debug().Str("path", fstabPath).Str("snapshot", snapshot.Path).Msg("Generating fstab diff")

	// Check if fstab exists
	if _, err := os.Stat(fstabPath); os.IsNotExist(err) {
		log.Warn().Str("path", fstabPath).Msg("Fstab file does not exist in snapshot")
		return nil, nil
	}

	// Read original file content
	originalContent, err := os.ReadFile(fstabPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read original fstab: %w", err)
	}

	// Parse existing fstab
	fstab, err := m.ParseFstab(fstabPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse snapshot fstab: %w", err)
	}

	// Update root mount entries
	modified := false
	modifiedEntries := make(map[string]bool)
	for _, entry := range fstab.Entries {
		if m.isRootMount(entry, rootFS) {
			if m.updateRootEntry(entry, snapshot, rootFS) {
				modified = true
				modifiedEntries[entry.Original] = true
			}
		}
	}

	if !modified {
		log.Debug().Str("path", fstabPath).Msg("No changes needed in fstab")
		return nil, nil
	}

	// Generate the new content
	newContent, err := m.generateFstabContentWithModifications(fstab, modifiedEntries)
	if err != nil {
		return nil, fmt.Errorf("failed to generate fstab content: %w", err)
	}

	return &diff.FileDiff{
		Path:     fstabPath,
		Original: string(originalContent),
		Modified: newContent,
		IsNew:    false,
	}, nil
}

// updateSnapshotFstab updates the fstab file in a snapshot to reflect the snapshot's subvolume
func (m *Manager) updateSnapshotFstab(snapshot *btrfs.Snapshot, rootFS *btrfs.Filesystem, r runner.Runner, askConfirmation bool) error {
	fstabPath := btrfs.GetSnapshotFstabPath(snapshot)
	log.Debug().Str("path", fstabPath).Str("snapshot", snapshot.Path).Msg("Updating snapshot fstab")

	// Check if fstab exists
	if _, err := os.Stat(fstabPath); os.IsNotExist(err) {
		log.Warn().Str("path", fstabPath).Msg("Fstab file does not exist in snapshot")
		return nil
	}

	// Read original file content for diff
	originalContent, err := os.ReadFile(fstabPath)
	if err != nil {
		return fmt.Errorf("failed to read original fstab: %w", err)
	}

	// Parse existing fstab
	fstab, err := m.ParseFstab(fstabPath)
	if err != nil {
		return fmt.Errorf("failed to parse snapshot fstab: %w", err)
	}

	// Update root mount entries
	modified := false
	modifiedEntries := make(map[string]bool)
	for _, entry := range fstab.Entries {
		if m.isRootMount(entry, rootFS) {
			if m.updateRootEntry(entry, snapshot, rootFS) {
				modified = true
				modifiedEntries[entry.Original] = true
			}
		}
	}

	if !modified {
		log.Debug().Str("path", fstabPath).Msg("No changes needed in fstab")
		return nil
	}

	// Generate the new content that would be written
	newContent, err := m.generateFstabContentWithModifications(fstab, modifiedEntries)
	if err != nil {
		return fmt.Errorf("failed to generate fstab content: %w", err)
	}

	fileDiff := &diff.FileDiff{
		Path:     fstabPath,
		Original: string(originalContent),
		Modified: newContent,
		IsNew:    false,
	}

	if r.IsDryRun() {
		// Show diff only
		diff.ShowDiff(fileDiff)
		log.Info().Str("path", fstabPath).Msg("[DRY RUN] Would update snapshot fstab")
		return nil
	}

	if askConfirmation {
		// Show diff and ask for confirmation
		if !diff.ConfirmChanges(fileDiff, false) {
			log.Info().Str("path", fstabPath).Msg("Skipped updating snapshot fstab (user declined)")
			return nil
		}
	}

	// Write updated fstab using runner
	if err := m.writeFstabWithRunner(fstabPath, fstab, modifiedEntries, r); err != nil {
		return fmt.Errorf("failed to write updated fstab: %w", err)
	}

	log.Info().Str("path", fstabPath).Msg("Updated snapshot fstab")
	return nil
}

// isRootMount determines if an fstab entry is for the root filesystem
func (m *Manager) isRootMount(entry *Entry, rootFS *btrfs.Filesystem) bool {
	// Check if mountpoint is root
	if entry.Mountpoint != "/" {
		return false
	}

	// Check if filesystem type is btrfs
	if entry.FSType != "btrfs" {
		return false
	}

	// Check if device matches (by UUID, LABEL, or device path)
	return m.deviceMatches(entry.Device, rootFS)
}

// updateRootEntry updates a root mount entry for the snapshot
func (m *Manager) updateRootEntry(entry *Entry, snapshot *btrfs.Snapshot, rootFS *btrfs.Filesystem) bool {
	modified := false

	// Update subvol option - ensure proper leading slash format
	subvolPath := snapshot.Path
	if !strings.HasPrefix(subvolPath, "/") {
		subvolPath = "/" + subvolPath
	}
	newOptions := m.updateSubvolOption(entry.Options, subvolPath)
	if newOptions != entry.Options {
		entry.Options = newOptions
		modified = true
	}

	// Update subvolid option
	newOptions = m.updateSubvolidOption(entry.Options, snapshot.ID)
	if newOptions != entry.Options {
		entry.Options = newOptions
		modified = true
	}

	return modified
}

// updateSubvolOption updates the subvol option in mount options
func (m *Manager) updateSubvolOption(options, newSubvol string) string {
	parser := params.NewCommaParameterParser()
	return parser.Update(options, "subvol", newSubvol)
}

// updateSubvolidOption updates the subvolid option in mount options
func (m *Manager) updateSubvolidOption(options string, newSubvolid uint64) string {
	parser := params.NewCommaParameterParser()
	return parser.Update(options, "subvolid", fmt.Sprintf("%d", newSubvolid))
}

// deviceMatches checks if the fstab device specification matches the filesystem
func (m *Manager) deviceMatches(device string, rootFS *btrfs.Filesystem) bool {
	return rootFS.MatchesDevice(device)
}

// isValidDeviceSpec checks if a device specification is valid
func (m *Manager) isValidDeviceSpec(device string) bool {
	// UUID format
	if strings.HasPrefix(device, "UUID=") {
		uuid := strings.TrimPrefix(device, "UUID=")
		// Basic UUID format check (36 characters with hyphens)
		uuidPattern := regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
		return uuidPattern.MatchString(uuid)
	}

	// LABEL format
	if strings.HasPrefix(device, "LABEL=") {
		label := strings.TrimPrefix(device, "LABEL=")
		return label != ""
	}

	// PARTUUID format
	if strings.HasPrefix(device, "PARTUUID=") {
		partuuid := strings.TrimPrefix(device, "PARTUUID=")
		return partuuid != ""
	}

	// PARTLABEL format
	if strings.HasPrefix(device, "PARTLABEL=") {
		partlabel := strings.TrimPrefix(device, "PARTLABEL=")
		return partlabel != ""
	}

	// Device path format
	if strings.HasPrefix(device, "/dev/") {
		return true
	}

	// Special cases
	if device == "none" || device == "tmpfs" || device == "proc" || device == "sysfs" {
		return true
	}

	return false
}

// writeFstabWithRunner writes an fstab structure back to a file using runner, preserving formatting of unchanged lines
func (m *Manager) writeFstabWithRunner(path string, fstab *Fstab, modifiedEntries map[string]bool, r runner.Runner) error {
	content, err := m.generateFstabContentWithModifications(fstab, modifiedEntries)
	if err != nil {
		return fmt.Errorf("failed to generate fstab content: %w", err)
	}

	return r.WriteFile(path, []byte(content), 0644, fmt.Sprintf("Update snapshot fstab: %s", path))
}

// generateFstabContentWithModifications generates fstab content, only reformatting modified entries
func (m *Manager) generateFstabContentWithModifications(fstab *Fstab, modifiedEntries map[string]bool) (string, error) {
	var content strings.Builder

	// Create a map of original lines to updated entries
	entryMap := make(map[string]*Entry)
	for _, entry := range fstab.Entries {
		entryMap[entry.Original] = entry
	}

	// Write lines, only reformatting entries that were actually modified
	for _, line := range fstab.Lines {
		trimmed := strings.TrimSpace(line)

		// Skip empty lines and comments - write as-is
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			content.WriteString(line + "\n")
			continue
		}

		// Check if this line has an updated entry
		if entry, exists := entryMap[line]; exists {
			// Only reformat if this entry was actually modified
			if modifiedEntries[line] {
				// Preserve original formatting but update options field
				updatedLine := m.updateLineWithNewOptions(line, entry.Options)
				content.WriteString(updatedLine + "\n")
			} else {
				// Write original line unchanged to preserve formatting
				content.WriteString(line + "\n")
			}
		} else {
			// Write original line
			content.WriteString(line + "\n")
		}
	}

	return content.String(), nil
}

// updateLineWithNewOptions updates only the options field in an fstab line while preserving original formatting
func (m *Manager) updateLineWithNewOptions(originalLine, newOptions string) string {
	// Parse the original line to find field boundaries
	fields := regexp.MustCompile(`\s+`).Split(strings.TrimSpace(originalLine), -1)
	if len(fields) < 4 {
		// Fallback to original line if parsing fails
		return originalLine
	}

	// Find the position where the options field starts and ends in the original line
	device := fields[0]
	mountpoint := fields[1]
	fstype := fields[2]

	// Find where the options field starts in the original line
	deviceEnd := strings.Index(originalLine, device) + len(device)
	mountpointStart := strings.Index(originalLine[deviceEnd:], mountpoint) + deviceEnd
	mountpointEnd := mountpointStart + len(mountpoint)
	fstypeStart := strings.Index(originalLine[mountpointEnd:], fstype) + mountpointEnd
	fstypeEnd := fstypeStart + len(fstype)

	// Find start of options field (skip whitespace after fstype)
	optionsStart := fstypeEnd
	for optionsStart < len(originalLine) && (originalLine[optionsStart] == ' ' || originalLine[optionsStart] == '\t') {
		optionsStart++
	}

	// Find end of options field (next whitespace)
	optionsEnd := optionsStart
	for optionsEnd < len(originalLine) && originalLine[optionsEnd] != ' ' && originalLine[optionsEnd] != '\t' {
		optionsEnd++
	}

	// Replace only the options part
	if optionsStart < len(originalLine) && optionsEnd <= len(originalLine) {
		return originalLine[:optionsStart] + newOptions + originalLine[optionsEnd:]
	}

	// Fallback: couldn't parse properly, use tab formatting
	return fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s",
		device, mountpoint, fstype, newOptions,
		getFieldOrDefault(fields, 4, "0"), getFieldOrDefault(fields, 5, "0"))
}

// getFieldOrDefault safely gets a field from a slice or returns default
func getFieldOrDefault(fields []string, index int, defaultValue string) string {
	if index >= 0 && index < len(fields) {
		return fields[index]
	}
	return defaultValue
}
