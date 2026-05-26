package refind

import (
	"bufio"
	"strings"
)

// parseExistingManagedConfig parses an existing managed config to extract menuentry customizations
func (g *Generator) parseExistingManagedConfig(content string) map[string]*MenuEntry {
	entries := make(map[string]*MenuEntry)

	var currentEntry *MenuEntry
	var inMenuEntry bool
	var inSubmenu bool

	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

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

		if strings.HasPrefix(line, "submenuentry ") && inMenuEntry {
			inSubmenu = true
			continue
		}

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

		if inMenuEntry && !inSubmenu && currentEntry != nil {
			g.parser.parseMenuDirective(currentEntry, line)
		}
	}

	if currentEntry != nil {
		entries[currentEntry.Title] = currentEntry
	}

	return entries
}
