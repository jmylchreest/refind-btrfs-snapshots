package refind

import (
	"path/filepath"
	"regexp"
	"strings"
)

// extractTimestampPattern matches trailing "(YYYY-MM-DD_HH-MM-SS)" in titles.
var extractTimestampPattern = regexp.MustCompile(`\s*\(\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}\)$`)

// groupEntriesByBase groups menu entries by their base name (removing timestamp patterns)
// and by their loader to create one menuentry per functional boot configuration
func (g *Generator) groupEntriesByBase(entries []*MenuEntry) map[string][]*MenuEntry {
	groups := make(map[string][]*MenuEntry)

	for _, entry := range entries {
		groupKey := g.generateGroupKey(entry)
		groups[groupKey] = append(groups[groupKey], entry)
	}

	return groups
}

// generateGroupKey creates a key for grouping entries that should be consolidated
func (g *Generator) generateGroupKey(entry *MenuEntry) string {
	if entry.Loader != "" {
		loaderName := filepath.Base(entry.Loader)
		if ext := filepath.Ext(loaderName); ext != "" {
			loaderName = strings.TrimSuffix(loaderName, ext)
		}
		return loaderName
	}

	if entry.SourceFile != "" && strings.HasSuffix(entry.SourceFile, "refind_linux.conf") {
		dir := filepath.Dir(entry.SourceFile)
		return "refind_linux:" + dir
	}

	return g.extractBaseName(entry.Title)
}

// extractBaseName extracts the base name from a title, removing timestamp patterns
func (g *Generator) extractBaseName(title string) string {
	baseName := extractTimestampPattern.ReplaceAllString(title, "")
	return strings.TrimSpace(baseName)
}

// generateMenuTitle generates an appropriate menu title from group key and template entry
func (g *Generator) generateMenuTitle(groupKey string, templateEntry *MenuEntry) string {
	switch groupKey {
	case "vmlinuz-linux", "vmlinuz":
		return "Arch Linux"
	case "vmlinuz-lts":
		return "Arch Linux LTS"
	case "bzImage":
		return "Linux"
	default:
		if !strings.Contains(groupKey, "/") && groupKey != "" {
			if len(groupKey) > 0 {
				return strings.ToUpper(groupKey[:1]) + groupKey[1:]
			}
		}
	}

	return templateEntry.Title
}

// mergeCustomizations merges user customizations from existing entry into template
func (g *Generator) mergeCustomizations(template, existing *MenuEntry) *MenuEntry {
	merged := *template

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

	merged.Submenues = []*SubmenuEntry{}

	return &merged
}
