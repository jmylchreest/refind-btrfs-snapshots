package btrfs

import (
	"bufio"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

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

	if subvol.Path == "" || subvol.ID == 0 {
		return nil, fmt.Errorf("failed to parse essential subvolume information")
	}

	return subvol, nil
}
