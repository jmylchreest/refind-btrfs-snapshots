package fstab

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/rs/zerolog/log"
)

// isValidUUID checks whether s is a valid UUID (8-4-4-4-12 hex digits with hyphens).
func isValidUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
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
	fields := strings.Fields(line)

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
