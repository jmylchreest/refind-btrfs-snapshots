package fstab

import (
	"fmt"
	"strings"
)

// generateFstabContentWithModifications generates fstab content, only reformatting modified entries
func (m *Manager) generateFstabContentWithModifications(fstab *Fstab, modifiedEntries map[string]bool) (string, error) {
	var content strings.Builder

	entryMap := make(map[string]*Entry)
	for _, entry := range fstab.Entries {
		entryMap[entry.Original] = entry
	}

	for _, line := range fstab.Lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			content.WriteString(line + "\n")
			continue
		}

		if entry, exists := entryMap[line]; exists {
			if modifiedEntries[line] {
				updatedLine := m.updateLineWithNewOptions(line, entry.Options)
				content.WriteString(updatedLine + "\n")
			} else {
				content.WriteString(line + "\n")
			}
		} else {
			content.WriteString(line + "\n")
		}
	}

	return content.String(), nil
}

// updateLineWithNewOptions updates only the options field in an fstab line while preserving original formatting
func (m *Manager) updateLineWithNewOptions(originalLine, newOptions string) string {
	fields := strings.Fields(originalLine)
	if len(fields) < 4 {
		return originalLine
	}

	device := fields[0]
	mountpoint := fields[1]
	fstype := fields[2]

	deviceEnd := strings.Index(originalLine, device) + len(device)
	mountpointStart := strings.Index(originalLine[deviceEnd:], mountpoint) + deviceEnd
	mountpointEnd := mountpointStart + len(mountpoint)
	fstypeStart := strings.Index(originalLine[mountpointEnd:], fstype) + mountpointEnd
	fstypeEnd := fstypeStart + len(fstype)

	optionsStart := fstypeEnd
	for optionsStart < len(originalLine) && (originalLine[optionsStart] == ' ' || originalLine[optionsStart] == '\t') {
		optionsStart++
	}

	optionsEnd := optionsStart
	for optionsEnd < len(originalLine) && originalLine[optionsEnd] != ' ' && originalLine[optionsEnd] != '\t' {
		optionsEnd++
	}

	if optionsStart < len(originalLine) && optionsEnd <= len(originalLine) {
		return originalLine[:optionsStart] + newOptions + originalLine[optionsEnd:]
	}

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
