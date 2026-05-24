package refind

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/params"
	"github.com/rs/zerolog/log"
)

// Parser handles rEFInd config file parsing
type Parser struct {
	espPath       string
	kernelScanner *kernel.Scanner
}

// NewParser creates a new rEFInd config parser
func NewParser(espPath string) *Parser {
	return &Parser{
		espPath: espPath,
	}
}

// NewParserWithScanner creates a new rEFInd config parser with a kernel scanner
// for pattern-based boot image detection. If scanner is nil, falls back to
// legacy hardcoded detection.
func NewParserWithScanner(espPath string, scanner *kernel.Scanner) *Parser {
	return &Parser{
		espPath:       espPath,
		kernelScanner: scanner,
	}
}

// FindRefindConfigPath searches for rEFInd config in standard locations
func (p *Parser) FindRefindConfigPath() (string, error) {
	searchPaths := []string{
		filepath.Join(p.espPath, "EFI", "refind", "refind.conf"),
		filepath.Join(p.espPath, "EFI", "BOOT", "refind.conf"),
		filepath.Join(p.espPath, "refind.conf"),
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

	err := filepath.WalkDir(p.espPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Name() == "refind_linux.conf" {
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
	configDir := filepath.Dir(mainConfigPath)
	return filepath.Join(configDir, "refind-btrfs-snapshots.conf")
}

// ParseConfig parses the main rEFInd configuration file and refind_linux.conf files
func (p *Parser) ParseConfig(configPath string) (*Config, error) {
	log.Debug().Str("path", configPath).Msg("Parsing rEFInd config")

	config := &Config{Path: configPath}

	entries, includes, globals, err := p.parseConfigFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse main config: %w", err)
	}

	config.Entries = append(config.Entries, entries...)
	config.IncludePaths = includes
	config.GlobalConfig = globals

	log.Info().Str("path", configPath).Int("entries", len(entries)).Msg("Parsed main rEFInd config file")

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

	// refind_linux.conf entries take priority over the main config.
	linuxConfigs, err := p.FindRefindLinuxConfigs()
	if err == nil {
		for _, linuxConfigPath := range linuxConfigs {
			linuxEntries, err := p.parseRefindLinuxConf(linuxConfigPath)
			if err != nil {
				log.Warn().Err(err).Str("path", linuxConfigPath).Msg("Failed to parse refind_linux.conf")
				continue
			}

			log.Info().Str("path", linuxConfigPath).Int("entries", len(linuxEntries)).Msg("Parsed refind_linux.conf file")
			config.Entries = append(linuxEntries, config.Entries...)
		}
	}

	log.Info().
		Str("config_path", configPath).
		Int("total_entries", len(config.Entries)).
		Msg("Completed parsing all rEFInd configuration files")
	return config, nil
}

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

		if line == "" || strings.HasPrefix(line, "#") {
			if !inMenuEntry {
				globals = append(globals, scanner.Text())
			}
			continue
		}

		if strings.HasPrefix(line, "include ") {
			includePath := strings.TrimSpace(strings.TrimPrefix(line, "include "))
			includes = append(includes, includePath)
			globals = append(globals, scanner.Text())
			continue
		}

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

		if strings.HasPrefix(line, "submenuentry ") && inMenuEntry {
			title := extractQuotedValue(line, "submenuentry ")
			currentSubmenu = &SubmenuEntry{Title: title}
			inSubmenu = true
			continue
		}

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

	if currentEntry != nil {
		entries = append(entries, currentEntry)
	}

	return entries, includes, globals, scanner.Err()
}

func (p *Parser) parseMenuDirective(entry *MenuEntry, line string) {
	parts := strings.SplitN(line, " ", 2)
	if len(parts) < 1 {
		return
	}

	directive := strings.TrimSpace(parts[0])
	var value string
	if len(parts) >= 2 {
		value = strings.TrimSpace(parts[1])
	}

	switch directive {
	case "icon":
		entry.Icon = value
	case "volume":
		entry.Volume = value
	case "loader":
		entry.Loader = value
	case "initrd":
		entry.Initrd = append(entry.Initrd, value)
	case "options":
		entry.Options = value
		entry.BootOptions = parseBootOptions(value)
	case "disabled":
		// User-toggled disable; we preserve the line as-is during regeneration.
	}
}

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
		submenu.Initrd = append(submenu.Initrd, value)
	case "options":
		submenu.Options = value
		submenu.BootOptions = parseBootOptions(value)
	case "add_options":
		submenu.AddOptions = value
	}
}

func (p *Parser) parseRefindLinuxConf(path string) ([]*MenuEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open refind_linux.conf: %w", err)
	}
	defer file.Close()

	var entries []*MenuEntry
	lineNumber := 0

	// Scan for boot images once per file (not per line)
	dir := filepath.Dir(path)
	var dirLoader string
	var dirInitrd string
	if p.kernelScanner != nil {
		if images, err := p.kernelScanner.ScanDir(dir); err == nil {
			for _, img := range images {
				switch img.Role {
				case kernel.RoleKernel:
					if dirLoader == "" {
						dirLoader = img.Path
					}
				case kernel.RoleInitramfs:
					if dirInitrd == "" {
						dirInitrd = img.Path
					}
				}
			}
		}
	} else {
		dirLoader = p.findKernelInDir(dir)
		dirInitrd = p.findInitrdInDir(dir)
	}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Format: "Boot Title" "root=UUID=... rootflags=subvol=@ ..."
		parts := p.parseQuotedLine(line)
		if len(parts) < 2 {
			log.Warn().Str("path", path).Int("line", lineNumber).Str("content", line).Msg("Invalid refind_linux.conf line format")
			continue
		}

		entry := &MenuEntry{
			Title:       parts[0],
			Options:     parts[1],
			BootOptions: parseBootOptions(parts[1]),
			SourceFile:  path,
			LineNumber:  lineNumber,
			Loader:      dirLoader,
		}
		if dirInitrd != "" {
			entry.Initrd = []string{dirInitrd}
		}

		entries = append(entries, entry)
		log.Debug().
			Str("path", path).
			Str("title", entry.Title).
			Str("loader", entry.Loader).
			Strs("initrd", entry.Initrd).
			Msg("Parsed refind_linux.conf entry")
	}

	return entries, scanner.Err()
}

// parseQuotedLine parses a line with quoted strings, handling escapes.
// Uses an index-based loop so the index can be advanced by the unquoted-
// string branch (range loops ignore mutations of the loop variable).
func (p *Parser) parseQuotedLine(line string) []string {
	var parts []string
	var current strings.Builder
	inQuotes := false
	escaped := false

	for i := 0; i < len(line); i++ {
		char := rune(line[i])

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
				parts = append(parts, current.String())
				current.Reset()
				inQuotes = false
			} else {
				inQuotes = true
			}
			continue
		}

		if inQuotes {
			current.WriteRune(char)
		} else if char == ' ' || char == '\t' {
			continue
		} else {
			current.WriteRune(char)
			for i+1 < len(line) {
				next := rune(line[i+1])
				if next == ' ' || next == '\t' || next == '"' {
					break
				}
				current.WriteRune(next)
				i++
			}
			parts = append(parts, current.String())
			current.Reset()
		}
	}

	if inQuotes && current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts
}

func (p *Parser) findKernelInDir(dir string) string {
	commonKernels := []string{"vmlinuz", "vmlinuz-linux", "vmlinuz.efi", "bzImage"}

	for _, kernel := range commonKernels {
		kernelPath := filepath.Join(dir, kernel)
		if _, err := os.Stat(kernelPath); err == nil {
			if rel, err := filepath.Rel(p.espPath, kernelPath); err == nil {
				return "/" + strings.ReplaceAll(rel, "\\", "/")
			}
		}
	}

	return ""
}

func (p *Parser) findInitrdInDir(dir string) string {
	commonInitrds := []string{"initramfs-linux.img", "initrd.img", "initrd", "initramfs.img"}

	for _, initrd := range commonInitrds {
		initrdPath := filepath.Join(dir, initrd)
		if _, err := os.Stat(initrdPath); err == nil {
			if rel, err := filepath.Rel(p.espPath, initrdPath); err == nil {
				return "/" + strings.ReplaceAll(rel, "\\", "/")
			}
		}
	}

	return ""
}

// parseBootOptions parses boot options string into structured data
func parseBootOptions(options string) *BootOptions {
	bootOpts := &BootOptions{}
	parser := params.NewBootOptionsParser()

	if root := parser.SpaceParser.Extract(options, "root"); root != "" {
		bootOpts.Root = root
	}
	if rootflags := parser.ExtractRootFlags(options); rootflags != "" {
		bootOpts.RootFlags = rootflags
		bootOpts.Subvol = parser.ExtractSubvol(rootflags)
		bootOpts.SubvolID = parser.ExtractSubvolID(rootflags)
	}
	if initrd := parser.SpaceParser.Extract(options, "initrd"); initrd != "" {
		bootOpts.InitrdPath = initrd
	}

	return bootOpts
}

func extractQuotedValue(line, prefix string) string {
	line = strings.TrimPrefix(line, prefix)
	line = strings.TrimSpace(line)

	if strings.HasSuffix(line, " {") {
		line = strings.TrimSuffix(line, " {")
		line = strings.TrimSpace(line)
	}

	if strings.HasPrefix(line, "\"") && strings.HasSuffix(line, "\"") {
		line = strings.Trim(line, "\"")
	}

	return line
}
