package esp

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
)

// ESP represents an EFI System Partition
type ESP struct {
	Device     string `json:"device"`
	UUID       string `json:"uuid"`
	MountPoint string `json:"mountpoint"`
	Size       string `json:"size"`
	Label      string `json:"label"`
}

// ESPDetector handles ESP detection
type ESPDetector struct {
	forceUUID string
}

// NewESPDetector creates a new ESP detector
func NewESPDetector(forceUUID string) *ESPDetector {
	return &ESPDetector{
		forceUUID: forceUUID,
	}
}

// FindESP detects the EFI System Partition
func (d *ESPDetector) FindESP() (*ESP, error) {
	log.Debug().Msg("Detecting EFI System Partition")

	// Get block device information from /proc and /sys
	devices, err := d.getBlockDevices()
	if err != nil {
		return nil, fmt.Errorf("failed to get block devices: %w", err)
	}

	// Look for ESP using different methods
	for _, device := range devices {
		if d.isESP(device) {
			esp := &ESP{
				Device:     device.Name,
				UUID:       device.UUID,
				MountPoint: device.Mountpoint,
				Size:       device.Size,
				Label:      device.PARTLABEL,
			}

			log.Info().
				Str("device", esp.Device).
				Str("mountpoint", esp.MountPoint).
				Str("uuid", esp.UUID).
				Msg("Found EFI System Partition")

			return esp, nil
		}
	}

	return nil, fmt.Errorf("no EFI System Partition found")
}

// BlockDevice represents a block device from lsblk output
type BlockDevice struct {
	Name       string `json:"name"`
	Size       string `json:"size"`
	Type       string `json:"type"`
	Mountpoint string `json:"mountpoint"`
	UUID       string `json:"uuid"`
	FSTYPE     string `json:"fstype"`
	PARTUUID   string `json:"partuuid"`
	PARTLABEL  string `json:"partlabel"`
	PARTTYPE   string `json:"parttype"`
}

// getBlockDevices reads block device information from /proc and /sys
func (d *ESPDetector) getBlockDevices() ([]*BlockDevice, error) {
	var devices []*BlockDevice

	// Read mounted filesystems from /proc/mounts
	mounts, err := d.readMounts()
	if err != nil {
		return nil, fmt.Errorf("failed to read mounts: %w", err)
	}

	// Read partition information from /proc/partitions
	partitions, err := d.readPartitions()
	if err != nil {
		return nil, fmt.Errorf("failed to read partitions: %w", err)
	}

	// Combine mount and partition information
	for _, partition := range partitions {
		device := &BlockDevice{
			Name: "/dev/" + partition.Name,
			Size: partition.Size,
			Type: "part",
		}

		// Add mount information if available
		if mount, exists := mounts[partition.Name]; exists {
			device.Mountpoint = mount.MountPoint
			device.FSTYPE = mount.FSType
		}

		// Read additional information from /sys
		if err := d.enrichDeviceInfo(device, partition.Name); err != nil {
			log.Debug().Err(err).Str("device", partition.Name).Msg("Failed to enrich device info")
		}

		devices = append(devices, device)
	}

	return devices, nil
}

// Partition represents a partition from /proc/partitions
type Partition struct {
	Name string
	Size string
}

// Mount represents a mount point from /proc/mounts
type Mount struct {
	Device     string
	MountPoint string
	FSType     string
}

// readMounts reads mount information from /proc/mounts
func (d *ESPDetector) readMounts() (map[string]*Mount, error) {
	file, err := os.Open("/proc/mounts")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	mounts := make(map[string]*Mount)
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}

		device := fields[0]
		mountPoint := fields[1]
		fsType := fields[2]

		// Extract device name from path
		deviceName := filepath.Base(device)
		if strings.HasPrefix(device, "/dev/") {
			mounts[deviceName] = &Mount{
				Device:     device,
				MountPoint: mountPoint,
				FSType:     fsType,
			}
		}
	}

	return mounts, scanner.Err()
}

// readPartitions reads partition information from /proc/partitions
func (d *ESPDetector) readPartitions() ([]*Partition, error) {
	file, err := os.Open("/proc/partitions")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var partitions []*Partition
	scanner := bufio.NewScanner(file)

	// Skip header lines
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "major") {
			break
		}
	}

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			continue
		}

		// fields: major minor #blocks name
		sizeBlocks := fields[2]
		name := fields[3]

		// Convert blocks to human readable size (assuming 1024 byte blocks)
		size := sizeBlocks + " blocks"

		partitions = append(partitions, &Partition{
			Name: name,
			Size: size,
		})
	}

	return partitions, scanner.Err()
}

// enrichDeviceInfo adds additional information from /sys filesystem
func (d *ESPDetector) enrichDeviceInfo(device *BlockDevice, deviceName string) error {
	// Read UUID from /dev/disk/by-uuid/
	if uuid, err := d.findUUIDForDevice(deviceName); err == nil {
		device.UUID = uuid
	}

	// Read PARTUUID from /dev/disk/by-partuuid/
	if partuuid, err := d.findPartUUIDForDevice(deviceName); err == nil {
		device.PARTUUID = partuuid
	}

	// Try to find partition type information
	sysPath := d.findSysPath(deviceName)
	if sysPath != "" {
		// Read partition type from /sys if available
		partTypePath := filepath.Join(sysPath, "partition")
		if _, err := os.Stat(partTypePath); err == nil {
			device.Type = "part"
		}

		// Try to read partition type GUID/ID
		if partType, err := d.readFileContent(filepath.Join(sysPath, "typeuuid")); err == nil {
			device.PARTTYPE = strings.TrimSpace(partType)
		} else if _, err := d.readFileContent(filepath.Join(sysPath, "start")); err == nil {
			// For MBR partitions, confirm it's a partition
			device.Type = "part"
		}
	}

	return nil
}

// findSysPath finds the correct /sys path for a device
func (d *ESPDetector) findSysPath(deviceName string) string {
	// Try direct block device path
	sysPath := filepath.Join("/sys/block", deviceName)
	if _, err := os.Stat(sysPath); err == nil {
		return sysPath
	}

	// Try as partition under all block devices in /sys/block/
	entries, err := os.ReadDir("/sys/block")
	if err == nil {
		for _, entry := range entries {
			partPath := filepath.Join("/sys/block", entry.Name(), deviceName)
			if _, err := os.Stat(partPath); err == nil {
				return partPath
			}
		}
	}

	return ""
}

// findDeviceIdentifierInDir resolves a device's identifier by scanning symlinks
// in the given /dev/disk/by-* directory (e.g. "/dev/disk/by-uuid").
func (d *ESPDetector) findDeviceIdentifierInDir(dir, deviceName, label string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}

	devicePath := "/dev/" + deviceName
	for _, entry := range entries {
		linkPath := filepath.Join(dir, entry.Name())
		target, err := os.Readlink(linkPath)
		if err != nil {
			continue
		}

		// Resolve relative symlink
		if !filepath.IsAbs(target) {
			target = filepath.Join(dir, target)
		}
		target = filepath.Clean(target)

		if target == devicePath {
			return entry.Name(), nil
		}
	}

	return "", fmt.Errorf("%s not found for device %s", label, deviceName)
}

// findPartUUIDForDevice finds the PARTUUID for a device by checking /dev/disk/by-partuuid/
func (d *ESPDetector) findPartUUIDForDevice(deviceName string) (string, error) {
	return d.findDeviceIdentifierInDir("/dev/disk/by-partuuid", deviceName, "PARTUUID")
}

// readFileContent reads content from a file, returning empty string on error
func (d *ESPDetector) readFileContent(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

// findUUIDForDevice finds the UUID for a device by checking /dev/disk/by-uuid/
func (d *ESPDetector) findUUIDForDevice(deviceName string) (string, error) {
	return d.findDeviceIdentifierInDir("/dev/disk/by-uuid", deviceName, "UUID")
}

// isESP determines if a block device is an EFI System Partition
func (d *ESPDetector) isESP(device *BlockDevice) bool {
	// Skip if not a partition
	if device.Type != "part" {
		return false
	}

	// If a specific UUID is configured, use that exclusively
	if d.forceUUID != "" {
		return device.UUID == d.forceUUID
	}

	// Check for EFI System Partition GUID (GPT)
	efiSystemGUID := "c12a7328-f81f-11d2-ba4b-00a0c93ec93b"
	if strings.ToLower(device.PARTTYPE) == efiSystemGUID {
		return true
	}

	// Check for EFI system partition type ID (MBR)
	if device.PARTTYPE == "0xef" || device.PARTTYPE == "ef" {
		return true
	}

	// Fallback heuristics for ESP detection
	// Check if it's a FAT filesystem on common ESP mount points
	if device.FSTYPE == "vfat" {
		// Common ESP mount points
		commonESPMounts := []string{"/boot", "/boot/efi", "/efi", "/esp"}
		for _, mount := range commonESPMounts {
			if device.Mountpoint == mount {
				log.Debug().Str("device", device.Name).Str("mountpoint", device.Mountpoint).Msg("Detected ESP using mount point heuristic")
				return true
			}
		}

		// Check if it's mounted and contains EFI directory structure
		if device.Mountpoint != "" {
			efiDir := filepath.Join(device.Mountpoint, "EFI")
			if info, err := os.Stat(efiDir); err == nil && info.IsDir() {
				log.Debug().Str("device", device.Name).Str("mountpoint", device.Mountpoint).Msg("Detected ESP using EFI directory heuristic")
				return true
			}
		}
	}

	return false
}

// GetESPMountPoint returns the mount point of the ESP, with fallback detection
func (d *ESPDetector) GetESPMountPoint() (string, error) {
	esp, err := d.FindESP()
	if err != nil {
		return "", err
	}

	if esp.MountPoint == "" {
		return "", fmt.Errorf("ESP is not mounted")
	}

	return esp.MountPoint, nil
}

// ValidateESPAccess checks if the ESP is accessible
// Write permission issues will be handled during actual file operations
func (d *ESPDetector) ValidateESPAccess() error {
	esp, err := d.FindESP()
	if err != nil {
		return err
	}

	if esp.MountPoint == "" {
		return fmt.Errorf("ESP is not mounted")
	}

	return d.ValidateESPPath(esp.MountPoint)
}

// ValidateESPPath validates access to a specific ESP path without re-detection
func (d *ESPDetector) ValidateESPPath(espPath string) error {
	if espPath == "" {
		return fmt.Errorf("ESP path cannot be empty")
	}

	// Check if the mount point exists and is accessible
	info, err := os.Stat(espPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("ESP mount point %s does not exist", espPath)
		}
		return fmt.Errorf("ESP mount point %s is not accessible: %w", espPath, err)
	}

	if !info.IsDir() {
		return fmt.Errorf("ESP mount point %s is not a directory", espPath)
	}

	log.Debug().
		Str("mountpoint", espPath).
		Msg("ESP access validated")

	return nil
}
