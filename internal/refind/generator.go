package refind

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/diff"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/params"
	"github.com/rs/zerolog/log"
)

// Package-level compiled regexps for patterns used in hot paths.
var (
	// legacyTimestampPattern matches titles like "Some Title (2024-01-15_12-30-00)"
	legacyTimestampPattern = regexp.MustCompile(`^.+\s+\([^)]*\d{4}[^)]*\d{2}[^)]*\d{2}[^)]*\)$`)
	// extractTimestampPattern matches trailing "(YYYY-MM-DD_HH-MM-SS)" in titles
	extractTimestampPattern = regexp.MustCompile(`\s*\(\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}\)$`)
)

// Generator handles rEFInd config generation
type Generator struct {
	parser       *Parser
	espPath      string
	bootSets     []*kernel.BootSet
	bootPlans    []*kernel.BootPlan
	menuFormat   string
	useLocalTime bool
}

// NewGenerator creates a new rEFInd config generator.
// menuFormat is the time.Format layout used for snapshot display names;
// useLocalTime renders timestamps in local time instead of UTC.
func NewGenerator(espPath, menuFormat string, useLocalTime bool) *Generator {
	return &Generator{
		parser:       NewParser(espPath),
		espPath:      espPath,
		menuFormat:   menuFormat,
		useLocalTime: useLocalTime,
	}
}

// NewGeneratorWithBootPlans creates a new rEFInd config generator with detected boot sets
// and per-snapshot boot plans. Boot plans enable btrfs-mode submenu generation where
// kernels are loaded from inside the snapshot rather than the ESP.
func NewGeneratorWithBootPlans(espPath, menuFormat string, useLocalTime bool, scanner *kernel.Scanner, bootSets []*kernel.BootSet, bootPlans []*kernel.BootPlan) *Generator {
	return &Generator{
		parser:       NewParserWithScanner(espPath, scanner),
		espPath:      espPath,
		bootSets:     bootSets,
		bootPlans:    bootPlans,
		menuFormat:   menuFormat,
		useLocalTime: useLocalTime,
	}
}

// updateOptionsForSnapshot updates boot options to point to the snapshot
func (g *Generator) updateOptionsForSnapshot(originalOptions string, snapshot *btrfs.Snapshot) string {
	if originalOptions == "" {
		return ""
	}

	parser := params.NewBootOptionsParser()
	options := originalOptions

	// Update rootflags subvol parameter
	// Preserve the original subvolume format (@ vs /@) used by the user
	rootflags := parser.ExtractRootFlags(originalOptions)
	originalSubvol := parser.ExtractSubvol(rootflags)

	var snapshotSubvol string

	// Always apply format preservation based on user's original preference
	// snapshot.Path format varies, so extract the actual snapshot path part
	var snapshotPathPart string
	if strings.HasPrefix(snapshot.Path, "@") {
		// snapshot.Path is "@/.snapshots/X/snapshot", extract the "/.snapshots/X/snapshot" part
		snapshotPathPart = strings.TrimPrefix(snapshot.Path, "@")
	} else {
		// snapshot.Path is "/.snapshots/X/snapshot", use as-is
		snapshotPathPart = snapshot.Path
	}

	// Determine the user's preferred format from their original subvol setting
	if originalSubvol != "" && strings.HasPrefix(originalSubvol, "/@") {
		// User prefers /@ format: /@/.snapshots/388/snapshot
		snapshotSubvol = "/@" + snapshotPathPart
	} else {
		// User prefers @ format or fallback: @/.snapshots/388/snapshot
		snapshotSubvol = "@" + snapshotPathPart
	}

	options = parser.UpdateSubvol(options, snapshotSubvol)

	// Update rootflags subvolid parameter
	options = parser.UpdateSubvolID(options, fmt.Sprintf("%d", snapshot.ID))

	// Handle multiple initrd parameters if present
	initrds := parser.SpaceParser.ExtractMultiple(options, "initrd")
	if len(initrds) > 0 {
		// Remove all existing initrd parameters
		options = parser.SpaceParser.RemoveAll(options, "initrd")

		// Add back all initrd parameters (they don't need path updates for snapshots)
		for _, initrd := range initrds {
			options = options + fmt.Sprintf(" initrd=%s", initrd)
		}
	}

	return options
}

// getSnapshotDisplayName generates a display name for a snapshot
func (g *Generator) getSnapshotDisplayName(snapshot *btrfs.Snapshot) string {
	if strings.HasPrefix(filepath.Base(snapshot.Path), "rwsnap_") {
		// Extract timestamp and ID from rwsnap naming
		name := filepath.Base(snapshot.Path)
		parts := strings.Split(name, "_")
		if len(parts) >= 3 {
			timestamp := strings.Join(parts[1:len(parts)-1], "_")
			return timestamp
		}
	}

	// Fallback to snapshot time using configured menu format
	return btrfs.FormatSnapshotTimeForMenu(snapshot.SnapshotTime, g.menuFormat, g.useLocalTime)
}

// UpdateRefindLinuxConfWithAllEntries generates a diff for updating refind_linux.conf with all matching entries.
// When snapshots is empty, any previously generated marker section is cleaned up and the diff
// reflects only that cleanup (no new entries are written).
func (g *Generator) UpdateRefindLinuxConfWithAllEntries(snapshots []*btrfs.Snapshot, sourceEntries []*MenuEntry, rootFS *btrfs.Filesystem) (*diff.FileDiff, error) {
	if len(sourceEntries) == 0 {
		return nil, nil
	}

	// All entries should be from the same file
	linuxConfPath := sourceEntries[0].SourceFile
	for _, entry := range sourceEntries {
		if entry.SourceFile != linuxConfPath {
			return nil, fmt.Errorf("all entries must be from the same refind_linux.conf file")
		}
	}

	// Read original file content
	originalContent, err := os.ReadFile(linuxConfPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read refind_linux.conf: %w", err)
	}

	// Generate new content with all snapshot entries for all source entries
	newContent, err := g.generateRefindLinuxConfWithAllEntries(string(originalContent), snapshots, sourceEntries, rootFS)
	if err != nil {
		return nil, fmt.Errorf("failed to generate refind_linux.conf content: %w", err)
	}

	// Only return diff if content actually changed
	if newContent == string(originalContent) {
		log.Debug().Str("path", linuxConfPath).Msg("File content matches, no changes required")
		return nil, nil
	}

	return &diff.FileDiff{
		Path:     linuxConfPath,
		Original: string(originalContent),
		Modified: newContent,
		IsNew:    false,
	}, nil
}

// generateRefindLinuxConfWithAllEntries processes all entries and generates content with cleanup
func (g *Generator) generateRefindLinuxConfWithAllEntries(originalContent string, snapshots []*btrfs.Snapshot, sourceEntries []*MenuEntry, rootFS *btrfs.Filesystem) (string, error) {
	var lines []string
	var inGeneratedSection bool
	var foundMarkers bool

	// First pass: check if we have any markers
	markerScanner := bufio.NewScanner(strings.NewReader(originalContent))
	for markerScanner.Scan() {
		line := markerScanner.Text()
		if strings.Contains(line, "##refind-btrfs-snapshots-start") || strings.Contains(line, "##refind-btrfs-snapshots-end") {
			foundMarkers = true
			break
		}
	}

	// Parse existing content and remove any previously generated entries
	scanner := bufio.NewScanner(strings.NewReader(originalContent))
	for scanner.Scan() {
		line := scanner.Text()

		if foundMarkers {
			// Use marker-based cleanup
			if strings.Contains(line, "##refind-btrfs-snapshots-start") {
				inGeneratedSection = true
				continue // Skip the start marker line
			}
			if strings.Contains(line, "##refind-btrfs-snapshots-end") {
				inGeneratedSection = false
				continue // Skip the end marker line
			}

			// Skip lines within generated sections
			if inGeneratedSection {
				continue
			}
		} else {
			// Fallback to old comment-based cleanup for backward compatibility
			if strings.Contains(line, "# Snapshot entries generated by refind-btrfs-snapshots") {
				inGeneratedSection = true
				continue // Skip the comment line
			}

			// If we're in a generated section, check if this is a generated entry
			if inGeneratedSection {
				if strings.TrimSpace(line) != "" && g.isLegacyGeneratedSnapshotEntry(line) {
					continue // Skip generated entries
				} else if strings.TrimSpace(line) == "" {
					continue // Skip empty lines in generated section
				} else {
					// This is not a generated entry, we're out of the generated section
					inGeneratedSection = false
					lines = append(lines, line)
				}
			}
		}

		if !inGeneratedSection {
			lines = append(lines, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	// Skip markers entirely when there are no snapshots to avoid leaving an empty marker pair.
	if len(sourceEntries) > 0 && len(snapshots) > 0 {
		// Add marker section (only add empty line if we have content before)
		if len(lines) > 0 && lines[len(lines)-1] != "" {
			lines = append(lines, "")
		}
		lines = append(lines, "##refind-btrfs-snapshots-start")

		// Keep source entries in their original file order for consistency

		// Add snapshot entries for each source entry
		for _, sourceEntry := range sourceEntries {
			for _, snapshot := range snapshots {
				snapshotTitle := fmt.Sprintf("%s (%s)", sourceEntry.Title, g.getSnapshotDisplayName(snapshot))
				snapshotOptions := g.updateOptionsForSnapshot(sourceEntry.Options, snapshot)

				// Format as quoted line: "Title" "options"
				snapshotLine := fmt.Sprintf("\"%s\" \"%s\"", snapshotTitle, snapshotOptions)
				lines = append(lines, snapshotLine)
			}
		}

		lines = append(lines, "##refind-btrfs-snapshots-end")
	}

	return strings.Join(lines, "\n") + "\n", nil
}

// isLegacyGeneratedSnapshotEntry checks if a line is a legacy generated snapshot entry
// Used for backward compatibility when no markers are found
func (g *Generator) isLegacyGeneratedSnapshotEntry(line string) bool {
	// Parse the line to extract the title
	parts := g.parser.parseQuotedLine(strings.TrimSpace(line))
	if len(parts) < 1 {
		return false
	}

	title := parts[0]

	// Check if title matches pattern: "Some Title (TIMESTAMP)" where TIMESTAMP looks like a date/time
	return legacyTimestampPattern.MatchString(title)
}

// GenerateManagedConfigDiff generates a single managed config file with proper menuentry/submenu structure
func (g *Generator) GenerateManagedConfigDiff(sourceEntries []*MenuEntry, snapshots []*btrfs.Snapshot, rootFS *btrfs.Filesystem, configPath string) (*diff.FileDiff, error) {
	log.Debug().Int("entries", len(sourceEntries)).Int("snapshots", len(snapshots)).Msg("Generating managed config")

	// Check for existing content to preserve user customizations
	var originalContent string
	var existingEntries map[string]*MenuEntry
	var isNewFile bool

	if existingFileContent, err := os.ReadFile(configPath); err == nil {
		originalContent = string(existingFileContent)
		existingEntries = g.parseExistingManagedConfig(originalContent)
		isNewFile = false
	} else {
		existingEntries = make(map[string]*MenuEntry)
		isNewFile = true
	}

	var content strings.Builder

	// Updated header comment
	content.WriteString("# Generated by refind-btrfs-snapshots\n")
	content.WriteString("# WARNING - Submenu options will be overwritten automatically,\n")
	content.WriteString("# but menuentry attributes will be maintained.\n")
	content.WriteString("#\n")
	content.WriteString("# To enable snapshot booting, add this line to your refind.conf:\n")
	content.WriteString("#   include refind-btrfs-snapshots.conf\n")
	content.WriteString("#\n")

	if isNewFile {
		// For new files, provide a template entry for the user to customize
		content.WriteString(g.generateTemplateEntry(sourceEntries, snapshots, rootFS))
	} else {
		// For existing files, preserve user customizations and update submenus
		content.WriteString("\n")
		content.WriteString(g.generateFromExistingEntries(existingEntries, snapshots, rootFS))
	}

	newContent := content.String()

	// Only return diff if content actually changed
	if newContent == originalContent {
		log.Debug().Str("path", configPath).Msg("File content matches, no changes required")
		return nil, nil
	}

	return &diff.FileDiff{
		Path:     configPath,
		Original: originalContent,
		Modified: newContent,
		IsNew:    originalContent == "",
	}, nil
}

// parseRefindLinuxConf parses a refind_linux.conf file
// parseExistingManagedConfig parses an existing managed config to extract menuentry customizations
func (g *Generator) parseExistingManagedConfig(content string) map[string]*MenuEntry {
	entries := make(map[string]*MenuEntry)

	// Parse the content manually since parseConfigFile expects a file path
	var currentEntry *MenuEntry
	var inMenuEntry bool
	var inSubmenu bool

	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Handle menuentry start
		if strings.HasPrefix(line, "menuentry ") {
			if currentEntry != nil {
				entries[currentEntry.Title] = currentEntry
			}

			title := extractQuotedValue(line, "menuentry ")
			currentEntry = &MenuEntry{
				Title:     title,
				Submenues: []*SubmenuEntry{},
			}
			inMenuEntry = true
			inSubmenu = false
			continue
		}

		// Handle submenuentry start
		if strings.HasPrefix(line, "submenuentry ") && inMenuEntry {
			inSubmenu = true
			continue
		}

		// Handle closing braces
		if line == "}" {
			if inSubmenu {
				inSubmenu = false
			} else if inMenuEntry {
				if currentEntry != nil {
					entries[currentEntry.Title] = currentEntry
					currentEntry = nil
				}
				inMenuEntry = false
			}
			continue
		}

		// Handle configuration directives for menuentry only (not submenu)
		if inMenuEntry && !inSubmenu && currentEntry != nil {
			g.parser.parseMenuDirective(currentEntry, line)
		}
	}

	// Add the last entry if it exists
	if currentEntry != nil {
		entries[currentEntry.Title] = currentEntry
	}

	return entries
}

// groupEntriesByBase groups menu entries by their base name (removing timestamp patterns)
// and by their loader to create one menuentry per functional boot configuration
func (g *Generator) groupEntriesByBase(entries []*MenuEntry) map[string][]*MenuEntry {
	groups := make(map[string][]*MenuEntry)

	for _, entry := range entries {
		// Create a group key based on the loader to group functionally similar entries
		groupKey := g.generateGroupKey(entry)
		groups[groupKey] = append(groups[groupKey], entry)
	}

	return groups
}

// generateGroupKey creates a key for grouping entries that should be consolidated
func (g *Generator) generateGroupKey(entry *MenuEntry) string {
	// Group by loader first (this groups entries using the same kernel)
	if entry.Loader != "" {
		loaderName := filepath.Base(entry.Loader)
		// Remove file extensions
		if ext := filepath.Ext(loaderName); ext != "" {
			loaderName = strings.TrimSuffix(loaderName, ext)
		}
		return loaderName
	}

	// For refind_linux.conf entries without loaders, try to infer from the directory
	// or group all entries from the same filesystem together
	if entry.SourceFile != "" && strings.HasSuffix(entry.SourceFile, "refind_linux.conf") {
		// Use the directory path as a grouping key for refind_linux.conf entries
		// This groups entries from the same kernel/filesystem together
		dir := filepath.Dir(entry.SourceFile)
		return "refind_linux:" + dir
	}

	// Fallback to base name extraction for entries without loader
	return g.extractBaseName(entry.Title)
}

// extractBaseName extracts the base name from a title, removing timestamp patterns
func (g *Generator) extractBaseName(title string) string {
	// Remove common timestamp patterns like "(YYYY-MM-DD_HH-MM-SS)"
	baseName := extractTimestampPattern.ReplaceAllString(title, "")

	// Clean up any trailing whitespace
	return strings.TrimSpace(baseName)
}

// generateMenuTitle generates an appropriate menu title from group key and template entry
func (g *Generator) generateMenuTitle(groupKey string, templateEntry *MenuEntry) string {
	// Convert group key (which is usually a loader name) to friendly names
	switch groupKey {
	case "vmlinuz-linux", "vmlinuz":
		return "Arch Linux"
	case "vmlinuz-lts":
		return "Arch Linux LTS"
	case "bzImage":
		return "Linux"
	default:
		// If it's already a clean title (from extractBaseName), use it
		if !strings.Contains(groupKey, "/") && groupKey != "" {
			// Capitalize first letter if it's a simple string
			if len(groupKey) > 0 {
				return strings.ToUpper(groupKey[:1]) + groupKey[1:]
			}
		}
	}

	// Fallback to original title
	return templateEntry.Title
}

// mergeCustomizations merges user customizations from existing entry into template
func (g *Generator) mergeCustomizations(template, existing *MenuEntry) *MenuEntry {
	// Create a copy of the template
	merged := *template

	// Preserve user customizations for menuentry attributes
	if existing.Icon != "" {
		merged.Icon = existing.Icon
	}
	if existing.Volume != "" {
		merged.Volume = existing.Volume
	}
	if existing.Loader != "" {
		merged.Loader = existing.Loader
	}
	if len(existing.Initrd) > 0 {
		merged.Initrd = existing.Initrd
	}
	if existing.Options != "" {
		merged.Options = existing.Options
		merged.BootOptions = parseBootOptions(existing.Options)
	}

	// Note: We don't preserve submenues - those will be regenerated
	merged.Submenues = []*SubmenuEntry{}

	return &merged
}

// generateTemplateEntry creates a template entry for new files.
// When boot sets are available (from kernel.Scanner), generates one template
// per detected kernel with accurate paths. Falls back to hardcoded Arch defaults.
func (g *Generator) generateTemplateEntry(sourceEntries []*MenuEntry, snapshots []*btrfs.Snapshot, rootFS *btrfs.Filesystem) string {
	var content strings.Builder

	content.WriteString("\n")
	content.WriteString("# TEMPLATE ENTRY - Customize this example and remove the 'disabled' line to enable\n")
	content.WriteString("# You can create multiple menuentry blocks for different boot configurations\n")
	content.WriteString("\n")

	// Try to infer some values from the source entries
	var sampleOptions string
	if len(sourceEntries) > 0 {
		sampleOptions = sourceEntries[0].Options
	}
	if sampleOptions == "" && rootFS != nil {
		// Provide a basic example if no source options available
		if rootFS.UUID != "" {
			sampleOptions = fmt.Sprintf("quiet rw rootflags=subvol=@ root=UUID=%s", rootFS.UUID)
		} else {
			sampleOptions = "quiet rw rootflags=subvol=@"
		}
	}

	// Generate template entries from detected boot sets if available
	if len(g.bootSets) > 0 {
		for _, bs := range g.bootSets {
			if bs.Kernel == nil {
				continue // Skip boot sets without a kernel
			}

			displayName := bs.DisplayName()
			content.WriteString(fmt.Sprintf("menuentry \"%s\" {\n", displayName))
			content.WriteString("    disabled\n")
			content.WriteString("    icon     /EFI/refind/icons/os_arch.png\n")
			content.WriteString(fmt.Sprintf("    loader   %s\n", bs.Kernel.Path))

			// Add microcode initrd entries first
			for _, mc := range bs.Microcode {
				content.WriteString(fmt.Sprintf("    initrd   %s\n", mc.Path))
			}
			// Add primary initramfs
			if bs.Initramfs != nil {
				content.WriteString(fmt.Sprintf("    initrd   %s\n", bs.Initramfs.Path))
			}

			if sampleOptions != "" {
				content.WriteString(fmt.Sprintf("    options  %s\n", sampleOptions))
			}
			content.WriteString("    \n")
			content.WriteString("    # Snapshot submenus will be automatically generated below:\n")

			// Add example submenus
			for i, snapshot := range snapshots {
				if i >= 2 { // Only show first 2 as examples
					break
				}
				snapshotTitle := fmt.Sprintf("%s (%s)", displayName, g.getSnapshotDisplayName(snapshot))
				content.WriteString(fmt.Sprintf("    submenuentry \"%s\" {\n", snapshotTitle))
				if sampleOptions != "" {
					snapshotOptions := g.updateOptionsForSnapshot(sampleOptions, snapshot)
					content.WriteString(fmt.Sprintf("        options %s\n", snapshotOptions))
				}
				content.WriteString("    }\n")
			}

			content.WriteString("}\n")
			content.WriteString("\n")
		}
	} else {
		// Fallback to hardcoded Arch Linux defaults when no boot sets detected
		content.WriteString("menuentry \"Arch Linux\" {\n")
		content.WriteString("    disabled\n")
		content.WriteString("    icon     /EFI/refind/icons/os_arch.png\n")
		content.WriteString("    loader   /boot/vmlinuz-linux\n")
		content.WriteString("    initrd   /boot/initramfs-linux.img\n")
		if sampleOptions != "" {
			content.WriteString(fmt.Sprintf("    options  %s\n", sampleOptions))
		}
		content.WriteString("    \n")
		content.WriteString("    # Snapshot submenus will be automatically generated below:\n")

		// Add example submenus
		for i, snapshot := range snapshots {
			if i >= 2 { // Only show first 2 as examples
				break
			}
			snapshotTitle := fmt.Sprintf("Arch Linux (%s)", g.getSnapshotDisplayName(snapshot))
			content.WriteString(fmt.Sprintf("    submenuentry \"%s\" {\n", snapshotTitle))
			if sampleOptions != "" {
				snapshotOptions := g.updateOptionsForSnapshot(sampleOptions, snapshot)
				content.WriteString(fmt.Sprintf("        options %s\n", snapshotOptions))
			}
			content.WriteString("    }\n")
		}

		content.WriteString("}\n")
		content.WriteString("\n")
	}

	content.WriteString("# INSTRUCTIONS:\n")
	content.WriteString("# 1. Remove or comment out the 'disabled' line above to enable this entry\n")
	content.WriteString("# 2. Customize the title, icon, loader, and initrd paths for your system\n")
	content.WriteString("# 3. Adjust the options line to match your boot requirements\n")
	content.WriteString("# 4. Save the file and regenerate to see your customized menu with snapshots\n")
	content.WriteString("# 5. You can create multiple menuentry blocks for different configurations\n")

	return content.String()
}

// generateFromExistingEntries generates content from existing customized entries
func (g *Generator) generateFromExistingEntries(existingEntries map[string]*MenuEntry, snapshots []*btrfs.Snapshot, rootFS *btrfs.Filesystem) string {
	var content strings.Builder

	if len(existingEntries) == 0 {
		content.WriteString("# No customized menu entries found.\n")
		content.WriteString("# Please add menuentry blocks to this file or regenerate to create templates.\n")
		return content.String()
	}

	// Sort titles for deterministic output order
	titles := make([]string, 0, len(existingEntries))
	for title := range existingEntries {
		titles = append(titles, title)
	}
	slices.Sort(titles)

	first := true
	for _, title := range titles {
		entry := existingEntries[title]
		if !first {
			content.WriteString("\n")
		}
		first = false

		entryContent := g.generateSingleMenuEntry(title, entry, snapshots, rootFS)
		content.WriteString(entryContent)
	}

	return content.String()
}

// getBootPlanForSnapshot looks up the first boot plan for a snapshot.
// Returns nil if no boot plans are available (falls back to ESP-mode behavior).
func (g *Generator) getBootPlanForSnapshot(snapshot *btrfs.Snapshot) *kernel.BootPlan {
	for _, plan := range g.bootPlans {
		if plan.Snapshot.Path == snapshot.Path {
			return plan
		}
	}
	return nil
}

// generateSingleMenuEntry generates a single menuentry with snapshots as submenus
func (g *Generator) generateSingleMenuEntry(title string, templateEntry *MenuEntry, snapshots []*btrfs.Snapshot, rootFS *btrfs.Filesystem) string {
	var content strings.Builder

	// Main menuentry
	content.WriteString(fmt.Sprintf("menuentry \"%s\" {\n", title))

	// Add all the preserved/template attributes
	if templateEntry.Icon != "" {
		content.WriteString(fmt.Sprintf("    icon %s\n", templateEntry.Icon))
	}
	if templateEntry.Volume != "" {
		content.WriteString(fmt.Sprintf("    volume %s\n", templateEntry.Volume))
	}
	if templateEntry.Loader != "" {
		content.WriteString(fmt.Sprintf("    loader %s\n", templateEntry.Loader))
	}
	for _, initrd := range templateEntry.Initrd {
		content.WriteString(fmt.Sprintf("    initrd %s\n", initrd))
	}
	if templateEntry.Options != "" {
		content.WriteString(fmt.Sprintf("    options %s\n", templateEntry.Options))
	}

	// Add submenu entries for snapshots
	for _, snapshot := range snapshots {
		snapshotTitle := fmt.Sprintf("%s (%s)", title, g.getSnapshotDisplayName(snapshot))
		content.WriteString(fmt.Sprintf("    submenuentry \"%s\" {\n", snapshotTitle))

		plan := g.getBootPlanForSnapshot(snapshot)
		if plan != nil && plan.Mode == kernel.BootModeBtrfs {
			// Btrfs-mode: kernel is inside the snapshot, override volume/loader/initrd
			if plan.BtrfsVolume != "" {
				content.WriteString(fmt.Sprintf("        volume  %s\n", plan.BtrfsVolume))
			}
			content.WriteString(fmt.Sprintf("        loader  %s\n", plan.SnapshotKernel))
			for _, initrd := range plan.SnapshotInitrds {
				content.WriteString(fmt.Sprintf("        initrd  %s\n", initrd))
			}
		}

		// Update options to point to the snapshot subvolume (needed for both modes)
		snapshotOptions := g.updateOptionsForSnapshot(templateEntry.Options, snapshot)
		if snapshotOptions != "" {
			content.WriteString(fmt.Sprintf("        options %s\n", snapshotOptions))
		}

		content.WriteString("    }\n")
	}

	content.WriteString("}\n")

	return content.String()
}
