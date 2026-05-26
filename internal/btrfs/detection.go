package btrfs

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/esp"
)

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

		if fstype != "btrfs" {
			continue
		}

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

	realDevice, err := filepath.EvalSymlinks(device)
	if err != nil {
		realDevice = device
	}

	identifiers.UUID = m.findIdentifierInDir("/dev/disk/by-uuid", realDevice)
	identifiers.PartUUID = m.findIdentifierInDir("/dev/disk/by-partuuid", realDevice)
	identifiers.Label = m.findIdentifierInDir("/dev/disk/by-label", realDevice)
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

		if linkedDevice == targetDevice {
			return entry.Name()
		}
	}

	return ""
}
