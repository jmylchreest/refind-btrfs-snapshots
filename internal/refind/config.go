package refind

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/diff"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/params"
	"github.com/rs/zerolog/log"
)

// Config represents a rEFInd configuration
type Config struct {
	Path         string       `json:"path"`
	Entries      []*MenuEntry `json:"entries"`
	IncludePaths []string     `json:"include_paths"`
	GlobalConfig []string     `json:"global_config"`
}

// MenuEntry represents a rEFInd menu entry
type MenuEntry struct {
	Title       string          `json:"title"`
	Icon        string          `json:"icon"`
	Volume      string          `json:"volume"`
	Loader      string          `json:"loader"`
	Initrd      string          `json:"initrd"`
	Options     string          `json:"options"`
	Submenues   []*SubmenuEntry `json:"submenues,omitempty"`
	SourceFile  string          `json:"source_file"`
	LineNumber  int             `json:"line_number"`
	BootOptions *BootOptions    `json:"boot_options,omitempty"`
}

// SubmenuEntry represents a submenu entry
type SubmenuEntry struct {
	Title       string       `json:"title"`
	Loader      string       `json:"loader,omitempty"`
	Initrd      string       `json:"initrd,omitempty"`
	Options     string       `json:"options,omitempty"`
	AddOptions  string       `json:"add_options,omitempty"`
	BootOptions *BootOptions `json:"boot_options,omitempty"`
}

// BootOptions represents parsed boot options
type BootOptions struct {
	Root       string `json:"root"`
	RootFlags  string `json:"rootflags"`
	Subvol     string `json:"subvol"`
	SubvolID   string `json:"subvolid"`
	InitrdPath string `json:"initrd_path"`
}

// Parser handles rEFInd config file parsing
type Parser struct {
	espPath string
}

// NewParser creates a new rEFInd config parser
func NewParser(espPath string) *Parser {
	return &Parser{
		espPath: espPath,
	}
}

// FindRefindConfigPath searches for rEFInd config in standard locations
func (p *Parser) FindRefindConfigPath() (string, error) {
	// Search order based on rEFInd documentation
	searchPaths := []string{
		// 1. Common rEFInd locations
		filepath.Join(p.espPath, "EFI", "refind", "refind.conf"),
		filepath.Join(p.espPath, "EFI", "BOOT", "refind.conf"),
		filepath.Join(p.espPath, "refind.conf"),

		// 2. Alternative locations
		filepath.Join(p.espPath, "EFI", "Microsoft", "Boot", "refind.conf"),
	}

	for _, path := range searchPaths {
		if _, err := os.Stat(path); err == nil {
			log.Debug().Str("path", path).Msg("Found rEFInd config")
			return path, nil
		}
	}

	return "", fmt.Errorf("no rEFInd config found in standard locations")
}

// FindRefindLinuxConfigs searches for refind_linux.conf files anywhere on the ESP
func (p *Parser) FindRefindLinuxConfigs() ([]string, error) {
	var configs []string

	// Walk the entire ESP to find all refind_linux.conf files
	err := filepath.Walk(p.espPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Continue on errors
		}

		if info.Name() == "refind_linux.conf" {
			configs = append(configs, path)
			log.Debug().Str("path", path).Msg("Found refind_linux.conf")
		}

		return nil
	})

	if err != nil {
		log.Debug().Err(err).Str("esp_path", p.espPath).Msg("Error searching ESP for refind_linux.conf files")
	}

	return configs, nil
}

// GetManagedConfigPath returns the path for our managed config file next to the main config
func (p *Parser) GetManagedConfigPath(mainConfigPath string) string {
	// Place our config in the same directory as the main config
	configDir := filepath.Dir(mainConfigPath)
	return filepath.Join(configDir, "refind-btrfs-snapshots.conf")
}

// ParseConfig parses the main rEFInd configuration file and refind_linux.conf files
func (p *Parser) ParseConfig(configPath string) (*Config, error) {
	log.Debug().Str("path", configPath).Msg("Parsing rEFInd config")

	config := &Config{
		Path: configPath,
	}

	// Read the main config file
	entries, includes, globals, err := p.parseConfigFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse main config: %w", err)
	}

	config.Entries = append(config.Entries, entries...)
	config.IncludePaths = includes
	config.GlobalConfig = globals
	
	log.Info().Str("path", configPath).Int("entries", len(entries)).Msg("Parsed main rEFInd config file")

	// Parse included files
	for _, includePath := range includes {
		fullPath := includePath
		if !filepath.IsAbs(includePath) {
			fullPath = filepath.Join(filepath.Dir(configPath), includePath)
		}

		includeEntries, _, _, err := p.parseConfigFile(fullPath)
		if err != nil {
			log.Warn().Err(err).Str("path", fullPath).Msg("Failed to parse included config")
			continue
		}
		
		log.Info().Str("path", fullPath).Int("entries", len(includeEntries)).Msg("Parsed included config file")
		config.Entries = append(config.Entries, includeEntries...)
	}

	// Parse refind_linux.conf files (these should take priority)
	linuxConfigs, err := p.FindRefindLinuxConfigs()
	if err == nil {
		for _, linuxConfigPath := range linuxConfigs {
			linuxEntries, err := p.parseRefindLinuxConf(linuxConfigPath)
			if err != nil {
				log.Warn().Err(err).Str("path", linuxConfigPath).Msg("Failed to parse refind_linux.conf")
				continue
			}
			
			log.Info().Str("path", linuxConfigPath).Int("entries", len(linuxEntries)).Msg("Parsed refind_linux.conf file")
			// Add refind_linux.conf entries at the beginning (higher priority)
			config.Entries = append(linuxEntries, config.Entries...)
		}
	}

	log.Info().
		Str("config_path", configPath).
		Int("total_entries", len(config.Entries)).
		Msg("Completed parsing all rEFInd configuration files")
	return config, nil
}

// parseConfigFile parses a single config file
func (p *Parser) parseConfigFile(path string) ([]*MenuEntry, []string, []string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	var entries []*MenuEntry
	var includes []string
	var globals []string
	var currentEntry *MenuEntry
	var inMenuEntry bool
	var inSubmenu bool
	var currentSubmenu *SubmenuEntry
	lineNumber := 0

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			if !inMenuEntry {
				globals = append(globals, scanner.Text())
			}
			continue
		}

		// Handle include directives
		if strings.HasPrefix(line, "include ") {
			includePath := strings.TrimSpace(strings.TrimPrefix(line, "include "))
			includes = append(includes, includePath)
			globals = append(globals, scanner.Text())
			continue
		}

		// Handle menuentry start
		if strings.HasPrefix(line, "menuentry ") {
			if currentEntry != nil {
				entries = append(entries, currentEntry)
			}

			title := extractQuotedValue(line, "menuentry ")
			currentEntry = &MenuEntry{
				Title:      title,
				SourceFile: path,
				LineNumber: lineNumber,
				Submenues:  []*SubmenuEntry{},
			}
			inMenuEntry = true
			inSubmenu = false
			continue
		}

		// Handle submenuentry start
		if strings.HasPrefix(line, "submenuentry ") && inMenuEntry {
			title := extractQuotedValue(line, "submenuentry ")
			currentSubmenu = &SubmenuEntry{
				Title: title,
			}
			inSubmenu = true
			continue
		}

		// Handle closing braces
		if line == "}" {
			if inSubmenu {
				if currentSubmenu != nil {
					currentEntry.Submenues = append(currentEntry.Submenues, currentSubmenu)
					currentSubmenu = nil
				}
				inSubmenu = false
			} else if inMenuEntry {
				if currentEntry != nil {
					entries = append(entries, currentEntry)
					currentEntry = nil
				}
				inMenuEntry = false
			}
			continue
		}

		// Handle configuration directives
		if inMenuEntry {
			if inSubmenu && currentSubmenu != nil {
				p.parseSubmenuDirective(currentSubmenu, line)
			} else if currentEntry != nil {
				p.parseMenuDirective(currentEntry, line)
			}
		} else {
			globals = append(globals, scanner.Text())
		}
	}

	// Add the last entry if it exists
	if currentEntry != nil {
		entries = append(entries, currentEntry)
	}

	return entries, includes, globals, scanner.Err()
}

// parseMenuDirective parses a directive within a menu entry
func (p *Parser) parseMenuDirective(entry *MenuEntry, line string) {
	parts := strings.SplitN(line, " ", 2)
	if len(parts) < 2 {
		return
	}

	directive := strings.TrimSpace(parts[0])
	value := strings.TrimSpace(parts[1])

	switch directive {
	case "icon":
		entry.Icon = value
	case "volume":
		entry.Volume = value
	case "loader":
		entry.Loader = value
	case "initrd":
		entry.Initrd = value
	case "options":
		entry.Options = value
		entry.BootOptions = parseBootOptions(value)
	}
}

// parseSubmenuDirective parses a directive within a submenu entry
func (p *Parser) parseSubmenuDirective(submenu *SubmenuEntry, line string) {
	parts := strings.SplitN(line, " ", 2)
	if len(parts) < 2 {
		return
	}

	directive := strings.TrimSpace(parts[0])
	value := strings.TrimSpace(parts[1])

	switch directive {
	case "loader":
		submenu.Loader = value
	case "initrd":
		submenu.Initrd = value
	case "options":
		submenu.Options = value
		submenu.BootOptions = parseBootOptions(value)
	case "add_options":
		submenu.AddOptions = value
	}
}

// parseBootOptions parses boot options string into structured data
func parseBootOptions(options string) *BootOptions {
	bootOpts := &BootOptions{}
	parser := params.NewBootOptionsParser()

	// Extract root parameter
	if root := parser.SpaceParser.Extract(options, "root"); root != "" {
		bootOpts.Root = root
	}

	// Extract rootflags parameter and parse subvol/subvolid
	if rootflags := parser.ExtractRootFlags(options); rootflags != "" {
		bootOpts.RootFlags = rootflags
		bootOpts.Subvol = parser.ExtractSubvol(rootflags)
		bootOpts.SubvolID = parser.ExtractSubvolID(rootflags)
	}

	// Extract initrd parameter
	if initrd := parser.SpaceParser.Extract(options, "initrd"); initrd != "" {
		bootOpts.InitrdPath = initrd
	}

	return bootOpts
}

// extractParameter extracts a parameter value from a string
func extractParameter(text, param string) string {
	pattern := regexp.MustCompile(fmt.Sprintf(`%s=([^\s]+)`, regexp.QuoteMeta(param)))
	matches := pattern.FindStringSubmatch(text)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// extractSubParameter extracts a sub-parameter from a comma-separated string
func extractSubParameter(text, param string) string {
	pattern := regexp.MustCompile(fmt.Sprintf(`%s=([^,\s]+)`, regexp.QuoteMeta(param)))
	matches := pattern.FindStringSubmatch(text)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// extractQuotedValue extracts a quoted value from a line
func extractQuotedValue(line, prefix string) string {
	line = strings.TrimPrefix(line, prefix)
	line = strings.TrimSpace(line)

	// Remove opening brace if present
	if strings.HasSuffix(line, " {") {
		line = strings.TrimSuffix(line, " {")
		line = strings.TrimSpace(line)
	}

	// Remove quotes if present
	if strings.HasPrefix(line, "\"") && strings.HasSuffix(line, "\"") {
		line = strings.Trim(line, "\"")
	}

	return line
}

// Generator handles rEFInd config generation
type Generator struct {
	parser  *Parser
	espPath string
}

// NewGenerator creates a new rEFInd config generator
func NewGenerator(espPath string) *Generator {
	return &Generator{
		parser:  NewParser(espPath),
		espPath: espPath,
	}
}

// generateConfigContentWithHeader generates the content for a snapshot config with optional header
func (g *Generator) generateConfigContentWithHeader(snapshots []*btrfs.Snapshot, sourceEntry *MenuEntry, rootFS *btrfs.Filesystem, includeHeader bool) (string, error) {
	if len(snapshots) == 0 {
		return "", fmt.Errorf("no snapshots provided")
	}

	var content strings.Builder

	// Header comment (optional)
	if includeHeader {
		content.WriteString("# Generated by refind-btrfs-snapshots\n")
		content.WriteString("# DO NOT EDIT MANUALLY - This file will be overwritten\n\n")
	}

	// Main entry (current booted system)
	content.WriteString(fmt.Sprintf("menuentry \"%s\" {\n", sourceEntry.Title))

	// Copy basic properties from source entry
	if sourceEntry.Icon != "" {
		content.WriteString(fmt.Sprintf("    icon %s\n", sourceEntry.Icon))
	}
	if sourceEntry.Volume != "" {
		content.WriteString(fmt.Sprintf("    volume %s\n", sourceEntry.Volume))
	}

	// Use original paths and options from source entry (current system)
	content.WriteString(fmt.Sprintf("    loader %s\n", sourceEntry.Loader))
	if sourceEntry.Initrd != "" {
		content.WriteString(fmt.Sprintf("    initrd %s\n", sourceEntry.Initrd))
	}
	if sourceEntry.Options != "" {
		content.WriteString(fmt.Sprintf("    options %s\n", sourceEntry.Options))
	}

	// Add submenu entries for source entry's original submenus (current system)
	for _, submenu := range sourceEntry.Submenues {
		content.WriteString(fmt.Sprintf("    submenuentry \"%s\" {\n", submenu.Title))

		if submenu.Loader != "" {
			content.WriteString(fmt.Sprintf("        loader %s\n", submenu.Loader))
		}
		if submenu.Initrd != "" {
			content.WriteString(fmt.Sprintf("        initrd %s\n", submenu.Initrd))
		}

		subOptions := ""
		if submenu.Options != "" {
			subOptions = submenu.Options
		} else if sourceEntry.Options != "" {
			subOptions = sourceEntry.Options
		}

		if submenu.AddOptions != "" {
			if subOptions != "" {
				subOptions += " " + submenu.AddOptions
			} else {
				subOptions = submenu.AddOptions
			}
		}

		if subOptions != "" {
			content.WriteString(fmt.Sprintf("        options %s\n", subOptions))
		}

		content.WriteString("    }\n")
	}

	// Add submenu entries for all snapshots (sorted by recency)
	for _, snapshot := range snapshots {
		snapshotTitle := fmt.Sprintf("%s (%s)", sourceEntry.Title, g.getSnapshotDisplayName(snapshot))
		content.WriteString(fmt.Sprintf("    submenuentry \"%s\" {\n", snapshotTitle))

		snapshotLoaderPath := g.updatePathForSnapshot(sourceEntry.Loader, snapshot)
		snapshotInitrdPath := g.updatePathForSnapshot(sourceEntry.Initrd, snapshot)
		snapshotOptions := g.updateOptionsForSnapshot(sourceEntry.Options, snapshot)

		content.WriteString(fmt.Sprintf("        loader %s\n", snapshotLoaderPath))
		if snapshotInitrdPath != "" {
			content.WriteString(fmt.Sprintf("        initrd %s\n", snapshotInitrdPath))
		}
		if snapshotOptions != "" {
			content.WriteString(fmt.Sprintf("        options %s\n", snapshotOptions))
		}

		content.WriteString("    }\n")
	}

	content.WriteString("}\n")

	return content.String(), nil
}

// updatePathForSnapshot updates a file path to point to the snapshot
func (g *Generator) updatePathForSnapshot(originalPath string, snapshot *btrfs.Snapshot) string {
	if originalPath == "" {
		return ""
	}

	// Replace the subvolume path in the original path
	// This assumes paths are in the format /@/path/to/file
	if strings.HasPrefix(originalPath, "/@/") {
		relativePath := strings.TrimPrefix(originalPath, "/@/")
		return fmt.Sprintf("/@%s/%s", snapshot.Path, relativePath)
	}

	// Handle other path formats as needed
	return originalPath
}

// updateOptionsForSnapshot updates boot options to point to the snapshot
func (g *Generator) updateOptionsForSnapshot(originalOptions string, snapshot *btrfs.Snapshot) string {
	if originalOptions == "" {
		return ""
	}

	parser := params.NewBootOptionsParser()
	options := originalOptions

	// Update rootflags subvol parameter
	options = parser.UpdateSubvol(options, snapshot.Path)

	// Update rootflags subvolid parameter  
	options = parser.UpdateSubvolID(options, fmt.Sprintf("%d", snapshot.ID))

	// Update initrd path if present
	if initrd := parser.SpaceParser.Extract(options, "initrd"); initrd != "" {
		newInitrdPath := g.updatePathForSnapshot(initrd, snapshot)
		// Convert forward slashes to backslashes for Windows-style paths in options
		newInitrdPath = strings.ReplaceAll(newInitrdPath, "/", "\\")
		options = parser.SpaceParser.Update(options, "initrd", newInitrdPath)
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

	// Fallback to snapshot time
	return snapshot.SnapshotTime.Format("2006-01-02_15-04-05")
}

// sanitizeFilename removes invalid characters from filename
func sanitizeFilename(filename string) string {
	// Replace invalid characters with underscores
	invalidChars := regexp.MustCompile(`[<>:"/\\|?*\s]`)
	return invalidChars.ReplaceAllString(filename, "_")
}

// UpdateRefindLinuxConfWithAllEntries generates a diff for updating refind_linux.conf with all matching entries
func (g *Generator) UpdateRefindLinuxConfWithAllEntries(snapshots []*btrfs.Snapshot, sourceEntries []*MenuEntry, rootFS *btrfs.Filesystem) (*diff.FileDiff, error) {
	if len(snapshots) == 0 || len(sourceEntries) == 0 {
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

// UpdateRefindLinuxConfDiff generates a diff for updating refind_linux.conf with snapshot entries
func (g *Generator) UpdateRefindLinuxConfDiff(snapshots []*btrfs.Snapshot, sourceEntry *MenuEntry, rootFS *btrfs.Filesystem) (*diff.FileDiff, error) {
	if len(snapshots) == 0 {
		return nil, nil
	}

	// Find the source refind_linux.conf file
	if sourceEntry.SourceFile == "" || !strings.HasSuffix(sourceEntry.SourceFile, "refind_linux.conf") {
		return nil, fmt.Errorf("source entry is not from refind_linux.conf")
	}

	linuxConfPath := sourceEntry.SourceFile

	// Read original file content
	originalContent, err := os.ReadFile(linuxConfPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read refind_linux.conf: %w", err)
	}

	// Generate new content with snapshot entries
	newContent, err := g.generateRefindLinuxConfContent(string(originalContent), snapshots, sourceEntry, rootFS)
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

	// Parse existing content and remove any previously generated entries
	scanner := bufio.NewScanner(strings.NewReader(originalContent))
	for scanner.Scan() {
		line := scanner.Text()

		// Check if we're entering a generated section
		if strings.Contains(line, "# Snapshot entries generated by refind-btrfs-snapshots") {
			inGeneratedSection = true
			continue // Skip the comment line
		}

		// If we're in a generated section, check if this is a generated entry
		if inGeneratedSection {
			// Check if this line looks like a generated snapshot entry
			// Format: "Some Title (YYYY-MM-DD_HH-MM-SS)" "options..."
			if strings.TrimSpace(line) != "" && g.isGeneratedSnapshotEntry(line) {
				continue // Skip generated entries
			} else if strings.TrimSpace(line) == "" {
				continue // Skip empty lines in generated section
			} else {
				// This is not a generated entry, we're out of the generated section
				inGeneratedSection = false
				lines = append(lines, line)
			}
		} else {
			lines = append(lines, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	// Add new snapshot entries for each source entry
	if len(sourceEntries) > 0 {
		// Add header comment (only add empty line if we have content before)
		if len(lines) > 0 && lines[len(lines)-1] != "" {
			lines = append(lines, "")
		}
		lines = append(lines, "# Snapshot entries generated by refind-btrfs-snapshots")

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
	}

	return strings.Join(lines, "\n"), nil
}

// isGeneratedSnapshotEntry checks if a line is a generated snapshot entry
func (g *Generator) isGeneratedSnapshotEntry(line string) bool {
	// Parse the line to extract the title
	parts := g.parser.parseQuotedLine(strings.TrimSpace(line))
	if len(parts) < 1 {
		return false
	}

	title := parts[0]

	// Check if title matches pattern: "Some Title (YYYY-MM-DD_HH-MM-SS)"
	// Use regex to match the timestamp pattern at the end
	timestampPattern := regexp.MustCompile(`^.+\s+\(\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}\)$`)
	return timestampPattern.MatchString(title)
}

// generateRefindLinuxConfContent adds snapshot entries to a refind_linux.conf file
func (g *Generator) generateRefindLinuxConfContent(originalContent string, snapshots []*btrfs.Snapshot, sourceEntry *MenuEntry, rootFS *btrfs.Filesystem) (string, error) {
	var lines []string
	var sourceLineFound bool

	// Parse existing content
	scanner := bufio.NewScanner(strings.NewReader(originalContent))
	for scanner.Scan() {
		line := scanner.Text()
		lines = append(lines, line)

		// Check if this is the source entry line we're extending
		if !sourceLineFound && strings.TrimSpace(line) != "" && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			// Parse this line to see if it matches our source entry
			parts := g.parser.parseQuotedLine(strings.TrimSpace(line))
			if len(parts) >= 2 && parts[0] == sourceEntry.Title && parts[1] == sourceEntry.Options {
				sourceLineFound = true
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	// If we found the source line, add snapshot entries after it
	if sourceLineFound {
		// Add header comment
		lines = append(lines, "")
		lines = append(lines, "# Snapshot entries generated by refind-btrfs-snapshots")

		// Add snapshot entries
		for _, snapshot := range snapshots {
			snapshotTitle := fmt.Sprintf("%s (%s)", sourceEntry.Title, g.getSnapshotDisplayName(snapshot))
			snapshotOptions := g.updateOptionsForSnapshot(sourceEntry.Options, snapshot)

			// Format as quoted line: "Title" "options"
			snapshotLine := fmt.Sprintf("\"%s\" \"%s\"", snapshotTitle, snapshotOptions)
			lines = append(lines, snapshotLine)
		}
	}

	return strings.Join(lines, "\n"), nil
}

// GenerateManagedConfigDiff generates a single managed config file for all entries and snapshots
func (g *Generator) GenerateManagedConfigDiff(sourceEntries []*MenuEntry, snapshots []*btrfs.Snapshot, rootFS *btrfs.Filesystem, configPath string) (*diff.FileDiff, error) {
	log.Debug().Int("entries", len(sourceEntries)).Int("snapshots", len(snapshots)).Msg("Generating managed config")

	if len(snapshots) == 0 {
		return nil, nil
	}

	var content strings.Builder

	// Header comment
	content.WriteString("# Generated by refind-btrfs-snapshots\n")
	content.WriteString("# DO NOT EDIT MANUALLY - This file will be overwritten\n")
	content.WriteString("#\n")
	content.WriteString("# To enable snapshot booting, add this line to your refind.conf:\n")
	content.WriteString("#   include refind-btrfs-snapshots.conf\n")
	content.WriteString("#\n\n")

	// Generate entries for each source entry
	for i, entry := range sourceEntries {
		if i > 0 {
			content.WriteString("\n")
		}

		entryContent, err := g.generateConfigContentWithHeader(snapshots, entry, rootFS, false)
		if err != nil {
			return nil, fmt.Errorf("failed to generate config content for entry %s: %w", entry.Title, err)
		}

		content.WriteString(entryContent)
	}

	// Check for existing content
	var originalContent string
	if existingContent, err := os.ReadFile(configPath); err == nil {
		originalContent = string(existingContent)
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
func (p *Parser) parseRefindLinuxConf(path string) ([]*MenuEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open refind_linux.conf: %w", err)
	}
	defer file.Close()

	var entries []*MenuEntry
	lineNumber := 0

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse quoted title and options
		// Format: "Boot Title" "root=UUID=... rootflags=subvol=@ ..."
		parts := p.parseQuotedLine(line)
		if len(parts) < 2 {
			log.Warn().Str("path", path).Int("line", lineNumber).Str("content", line).Msg("Invalid refind_linux.conf line format")
			continue
		}

		title := parts[0]
		options := parts[1]

		// Create menu entry from refind_linux.conf
		entry := &MenuEntry{
			Title:       title,
			Options:     options,
			BootOptions: parseBootOptions(options),
			SourceFile:  path,
			LineNumber:  lineNumber,
		}

		// Try to infer loader and initrd from directory structure
		dir := filepath.Dir(path)
		if loaderPath := p.findKernelInDir(dir); loaderPath != "" {
			entry.Loader = loaderPath
		}
		if initrdPath := p.findInitrdInDir(dir); initrdPath != "" {
			entry.Initrd = initrdPath
		}

		entries = append(entries, entry)
		log.Debug().
			Str("path", path).
			Str("title", title).
			Str("loader", entry.Loader).
			Str("initrd", entry.Initrd).
			Msg("Parsed refind_linux.conf entry")
	}

	return entries, scanner.Err()
}

// parseQuotedLine parses a line with quoted strings, handling escapes
func (p *Parser) parseQuotedLine(line string) []string {
	var parts []string
	var current strings.Builder
	inQuotes := false
	escaped := false

	for i, char := range line {
		if escaped {
			current.WriteRune(char)
			escaped = false
			continue
		}

		if char == '\\' {
			escaped = true
			continue
		}

		if char == '"' {
			if inQuotes {
				// End of quoted string
				parts = append(parts, current.String())
				current.Reset()
				inQuotes = false
			} else {
				// Start of quoted string
				inQuotes = true
			}
			continue
		}

		if inQuotes {
			current.WriteRune(char)
		} else if char == ' ' || char == '\t' {
			// Skip whitespace outside quotes
			continue
		} else {
			// Start of unquoted string
			current.WriteRune(char)
			// Read until next space or quote
			for j := i + 1; j < len(line); j++ {
				nextChar := rune(line[j])
				if nextChar == ' ' || nextChar == '\t' || nextChar == '"' {
					break
				}
				current.WriteRune(nextChar)
				i = j
			}
			parts = append(parts, current.String())
			current.Reset()
		}
	}

	// Handle case where line ends while in quotes
	if inQuotes && current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts
}

// findKernelInDir looks for common kernel files in a directory
func (p *Parser) findKernelInDir(dir string) string {
	commonKernels := []string{"vmlinuz", "vmlinuz-linux", "vmlinuz.efi", "bzImage"}

	for _, kernel := range commonKernels {
		kernelPath := filepath.Join(dir, kernel)
		if _, err := os.Stat(kernelPath); err == nil {
			// Convert to ESP-relative path
			if rel, err := filepath.Rel(p.espPath, kernelPath); err == nil {
				return "/" + strings.ReplaceAll(rel, "\\", "/")
			}
		}
	}

	return ""
}

// findInitrdInDir looks for common initrd files in a directory
func (p *Parser) findInitrdInDir(dir string) string {
	commonInitrds := []string{"initramfs-linux.img", "initrd.img", "initrd", "initramfs.img"}

	for _, initrd := range commonInitrds {
		initrdPath := filepath.Join(dir, initrd)
		if _, err := os.Stat(initrdPath); err == nil {
			// Convert to ESP-relative path
			if rel, err := filepath.Rel(p.espPath, initrdPath); err == nil {
				return "/" + strings.ReplaceAll(rel, "\\", "/")
			}
		}
	}

	return ""
}
