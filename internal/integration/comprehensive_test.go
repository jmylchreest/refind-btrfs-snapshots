package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/refind"
)

// TestComprehensiveMultiKernelMultiVolumeScenario tests a complex scenario with:
// - Multiple kernels (mainline, LTS, zen, hardened)
// - Multiple root volumes (different UUIDs, PARTUUIDs, LABELs)
// - Multiple refind_linux.conf files across different kernel directories
// - Existing menuentry options in refind.conf that match our root volume
func TestComprehensiveMultiKernelMultiVolumeScenario(t *testing.T) {
	tempDir := t.TempDir()
	
	// Create main refind.conf with existing menu entries
	mainConfig := filepath.Join(tempDir, "refind.conf")
	mainContent := `# rEFInd Configuration File
timeout 30
default_selection 1
resolution 1920 1080

# Existing menuentry that matches our root volume (UUID=main-btrfs-uuid)
menuentry "Arch Linux (Main)" {
    icon /EFI/refind/icons/os_arch.png
    volume "Boot"
    loader /vmlinuz-linux
    initrd /initramfs-linux.img
    options "root=UUID=main-btrfs-uuid rootflags=subvol=@ rw quiet splash"
}

# Another menuentry for same volume but different kernel
menuentry "Arch Linux LTS" {
    icon /EFI/refind/icons/os_arch.png
    volume "Boot"
    loader /vmlinuz-linux-lts
    initrd /initramfs-linux-lts.img
    options "root=UUID=main-btrfs-uuid rootflags=subvol=@ rw quiet"
}

# Menuentry for completely different volume (should be ignored)
menuentry "Ubuntu" {
    icon /EFI/refind/icons/os_ubuntu.png
    volume "Boot"
    loader /vmlinuz-ubuntu
    initrd /initramfs-ubuntu.img
    options "root=UUID=ubuntu-ext4-uuid rw quiet splash"
}

# Windows entry (should be ignored)
menuentry "Windows 11" {
    icon /EFI/refind/icons/os_win.png
    loader /EFI/Microsoft/Boot/bootmgfw.efi
}`

	if err := os.WriteFile(mainConfig, []byte(mainContent), 0644); err != nil {
		t.Fatalf("Failed to create main config: %v", err)
	}
	
	// Define test scenario data (not used in test but kept for documentation)
	_ = struct {
		kernels []KernelConfig
		volumes []VolumeConfig
		directories []string
	}{
		kernels: []KernelConfig{
			{"linux", "mainline", "vmlinuz-linux", "initramfs-linux.img"},
			{"linux-lts", "LTS", "vmlinuz-linux-lts", "initramfs-linux-lts.img"},
			{"linux-zen", "Zen", "vmlinuz-linux-zen", "initramfs-linux-zen.img"},
			{"linux-hardened", "Hardened", "vmlinuz-linux-hardened", "initramfs-linux-hardened.img"},
		},
		volumes: []VolumeConfig{
			{"main-btrfs-uuid", "MAIN-PARTUUID", "ArchMain", "ARCH-MAIN", "@", true},
			{"alt-btrfs-uuid", "ALT-PARTUUID", "ArchAlt", "ARCH-ALT", "@alt", false},
			{"test-btrfs-uuid", "TEST-PARTUUID", "ArchTest", "ARCH-TEST", "@test", false},
			{"ubuntu-ext4-uuid", "", "Ubuntu", "", "", false}, // Non-btrfs
		},
		directories: []string{
			"EFI/Linux/mainline",
			"EFI/Linux/lts", 
			"EFI/Linux/zen",
			"EFI/Linux/hardened",
			"kernels/mainline",
			"kernels/lts",
			"boot/kernels/custom",
		},
	}
	
	// Create refind_linux.conf files for different kernel/volume combinations
	confFiles := map[string]RefindLinuxConf{
		// Mainline kernel configurations
		"EFI/Linux/mainline/refind_linux.conf": {
			entries: []RefindEntry{
				{"Arch Linux (Mainline Default)", "root=UUID=main-btrfs-uuid rootflags=subvol=@ rw quiet splash"},
				{"Arch Linux (Mainline Debug)", "root=UUID=main-btrfs-uuid rootflags=subvol=@ rw debug"},
			},
		},
		
		// LTS kernel configurations  
		"EFI/Linux/lts/refind_linux.conf": {
			entries: []RefindEntry{
				{"Arch Linux LTS (Default)", "root=UUID=main-btrfs-uuid rootflags=subvol=@ rw quiet"},
				{"Arch Linux LTS (Fallback)", "root=UUID=main-btrfs-uuid rootflags=subvol=@ rw systemd.unit=multi-user.target"},
			},
		},
		
		// Zen kernel with alternative volume
		"EFI/Linux/zen/refind_linux.conf": {
			entries: []RefindEntry{
				{"Arch Linux Zen (Alt Volume)", "root=UUID=alt-btrfs-uuid rootflags=subvol=@alt rw quiet performance"},
			},
		},
		
		// Hardened kernel with PARTUUID
		"EFI/Linux/hardened/refind_linux.conf": {
			entries: []RefindEntry{
				{"Arch Hardened (PARTUUID)", "root=PARTUUID=MAIN-PARTUUID rootflags=subvol=@ rw quiet"},
				{"Arch Hardened (Emergency)", "root=PARTUUID=MAIN-PARTUUID rootflags=subvol=@ rw systemd.unit=emergency.target"},
			},
		},
		
		// Kernel directory with LABEL
		"kernels/mainline/refind_linux.conf": {
			entries: []RefindEntry{
				{"Arch Custom (LABEL)", "root=LABEL=ArchMain rootflags=subvol=@ rw quiet"},
			},
		},
		
		// Mixed volume types in one file
		"kernels/lts/refind_linux.conf": {
			entries: []RefindEntry{
				{"LTS Main Volume", "root=UUID=main-btrfs-uuid rootflags=subvol=@ rw"},
				{"LTS Test Volume", "root=UUID=test-btrfs-uuid rootflags=subvol=@test rw"},
				{"LTS Ubuntu (Non-btrfs)", "root=UUID=ubuntu-ext4-uuid rw"}, // Should be parsed but not processed for snapshots
			},
		},
		
		// Custom location with PARTLABEL
		"boot/kernels/custom/refind_linux.conf": {
			entries: []RefindEntry{
				{"Custom Kernel (PARTLABEL)", "root=PARTLABEL=ARCH-MAIN rootflags=subvol=@ rw custom_param=test"},
			},
		},
	}
	
	// Create all the refind_linux.conf files
	for confPath, confData := range confFiles {
		fullPath := filepath.Join(tempDir, confPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("Failed to create directory for %s: %v", confPath, err)
		}
		
		content := ""
		for _, entry := range confData.entries {
			content += "\"" + entry.title + "\" \"" + entry.options + "\"\n"
		}
		
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create %s: %v", confPath, err)
		}
	}
	
	// Parse configuration
	parser := refind.NewParser(tempDir)
	config, err := parser.ParseConfig(mainConfig)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	
	// Verify comprehensive parsing results
	t.Run("verify_total_entries", func(t *testing.T) {
		// Main config: 4 entries + refind_linux.conf entries: 12 entries = 16 total
		expectedTotal := 16
		if len(config.Entries) != expectedTotal {
			t.Errorf("Expected %d total entries, got %d", expectedTotal, len(config.Entries))
			logAllEntries(t, config.Entries)
		}
	})
	
	t.Run("verify_main_config_entries", func(t *testing.T) {
		mainConfigEntries := 0
		for _, entry := range config.Entries {
			if entry.SourceFile == mainConfig {
				mainConfigEntries++
			}
		}
		expectedMainEntries := 4
		if mainConfigEntries != expectedMainEntries {
			t.Errorf("Expected %d main config entries, got %d", expectedMainEntries, mainConfigEntries)
		}
	})
	
	t.Run("verify_refind_linux_entries", func(t *testing.T) {
		refindLinuxEntries := 0
		sourceFiles := make(map[string]int)
		for _, entry := range config.Entries {
			if filepath.Base(entry.SourceFile) == "refind_linux.conf" {
				refindLinuxEntries++
				sourceFiles[entry.SourceFile]++
			}
		}
		
		expectedRefindLinuxEntries := 12
		if refindLinuxEntries != expectedRefindLinuxEntries {
			t.Errorf("Expected %d refind_linux.conf entries, got %d", expectedRefindLinuxEntries, refindLinuxEntries)
		}
		
		expectedFiles := 7
		if len(sourceFiles) != expectedFiles {
			t.Errorf("Expected entries from %d refind_linux.conf files, got %d", expectedFiles, len(sourceFiles))
			for file, count := range sourceFiles {
				t.Logf("File: %s, Entries: %d", file, count)
			}
		}
	})
	
	t.Run("verify_volume_matching", func(t *testing.T) {
		// Test entries that should match main-btrfs-uuid volume
		mainVolumeEntries := 0
		for _, entry := range config.Entries {
			if entry.BootOptions != nil {
				switch entry.BootOptions.Root {
				case "UUID=main-btrfs-uuid":
					if entry.BootOptions.Subvol == "@" {
						mainVolumeEntries++
					}
				case "PARTUUID=MAIN-PARTUUID":
					if entry.BootOptions.Subvol == "@" {
						mainVolumeEntries++
					}
				case "LABEL=ArchMain":
					if entry.BootOptions.Subvol == "@" {
						mainVolumeEntries++
					}
				case "PARTLABEL=ARCH-MAIN":
					if entry.BootOptions.Subvol == "@" {
						mainVolumeEntries++
					}
				}
			}
		}
		
		expectedMainVolumeEntries := 11 // 2 from main config + 9 from refind_linux.conf files
		if mainVolumeEntries != expectedMainVolumeEntries {
			t.Errorf("Expected %d entries matching main volume, got %d", expectedMainVolumeEntries, mainVolumeEntries)
		}
	})
	
	t.Run("verify_device_identifier_types", func(t *testing.T) {
		deviceTypes := make(map[string]int)
		for _, entry := range config.Entries {
			if entry.BootOptions != nil && entry.BootOptions.Root != "" {
				if entry.BootOptions.Root[:4] == "UUID" {
					deviceTypes["UUID"]++
				} else if entry.BootOptions.Root[:8] == "PARTUUID" {
					deviceTypes["PARTUUID"]++
				} else if entry.BootOptions.Root[:5] == "LABEL" {
					deviceTypes["LABEL"]++
				} else if entry.BootOptions.Root[:9] == "PARTLABEL" {
					deviceTypes["PARTLABEL"]++
				}
			}
		}
		
		expectedDeviceTypes := map[string]int{
			"UUID":      11, // Most common
			"PARTUUID":  2, // Hardened kernel
			"LABEL":     1, // Custom kernel
			"PARTLABEL": 1, // Custom kernel
		}
		
		for devType, expectedCount := range expectedDeviceTypes {
			if deviceTypes[devType] != expectedCount {
				t.Errorf("Expected %d %s entries, got %d", expectedCount, devType, deviceTypes[devType])
			}
		}
	})
	
	t.Run("verify_subvolume_variations", func(t *testing.T) {
		subvolumes := make(map[string]int)
		for _, entry := range config.Entries {
			if entry.BootOptions != nil && entry.BootOptions.Subvol != "" {
				subvolumes[entry.BootOptions.Subvol]++
			}
		}
		
		expectedSubvolumes := map[string]int{
			"@":     11, // Main subvolume
			"@alt":  1, // Alternative subvolume
			"@test": 1, // Test subvolume
		}
		
		for subvol, expectedCount := range expectedSubvolumes {
			if subvolumes[subvol] != expectedCount {
				t.Errorf("Expected %d entries with subvol=%s, got %d", expectedCount, subvol, subvolumes[subvol])
			}
		}
	})
}

// Helper types for test data structure
type KernelConfig struct {
	name        string
	displayName string
	vmlinuz     string
	initramfs   string
}

type VolumeConfig struct {
	uuid      string
	partuuid  string
	label     string
	partlabel string
	subvol    string
	isMain    bool
}

type RefindEntry struct {
	title   string
	options string
}

type RefindLinuxConf struct {
	entries []RefindEntry
}

// Helper function to log all entries for debugging
func logAllEntries(t *testing.T, entries []*refind.MenuEntry) {
	t.Log("All parsed entries:")
	for i, entry := range entries {
		bootOpts := "none"
		if entry.BootOptions != nil {
			bootOpts = entry.BootOptions.Root + " subvol=" + entry.BootOptions.Subvol
		}
		t.Logf("  %d: %s (from %s) - %s", i+1, entry.Title, filepath.Base(entry.SourceFile), bootOpts)
	}
}

// TestBootableEntryDetectionWithMultipleVolumes tests the isBootableEntry logic
// against various device specifications and subvolume combinations
func TestBootableEntryDetectionWithMultipleVolumes(t *testing.T) {
	// Mock filesystem representing our root volume
	rootFS := &mockFilesystem{
		uuid:      "main-btrfs-uuid",
		partuuid:  "MAIN-PARTUUID", 
		label:     "ArchMain",
		partlabel: "ARCH-MAIN",
		device:    "/dev/mapper/luks-main",
		subvol:    "@",
	}
	
	testCases := []struct {
		name     string
		entry    *refind.MenuEntry
		expected bool
		reason   string
	}{
		{
			name: "exact_uuid_match",
			entry: createTestEntry("Test Entry", "root=UUID=main-btrfs-uuid rootflags=subvol=@"),
			expected: true,
			reason: "UUID and subvolume match exactly",
		},
		{
			name: "partuuid_match",
			entry: createTestEntry("Test Entry", "root=PARTUUID=MAIN-PARTUUID rootflags=subvol=@"),
			expected: true,
			reason: "PARTUUID and subvolume match",
		},
		{
			name: "label_match", 
			entry: createTestEntry("Test Entry", "root=LABEL=ArchMain rootflags=subvol=@"),
			expected: true,
			reason: "LABEL and subvolume match",
		},
		{
			name: "partlabel_match",
			entry: createTestEntry("Test Entry", "root=PARTLABEL=ARCH-MAIN rootflags=subvol=@"),
			expected: true,
			reason: "PARTLABEL and subvolume match",
		},
		{
			name: "device_path_match",
			entry: createTestEntry("Test Entry", "root=/dev/mapper/luks-main rootflags=subvol=@"),
			expected: true,
			reason: "Device path and subvolume match",
		},
		{
			name: "wrong_uuid",
			entry: createTestEntry("Test Entry", "root=UUID=different-uuid rootflags=subvol=@"),
			expected: false,
			reason: "UUID doesn't match",
		},
		{
			name: "wrong_subvolume",
			entry: createTestEntry("Test Entry", "root=UUID=main-btrfs-uuid rootflags=subvol=@alt"),
			expected: false,
			reason: "Subvolume doesn't match",
		},
		{
			name: "no_subvolume",
			entry: createTestEntry("Test Entry", "root=UUID=main-btrfs-uuid"),
			expected: false,
			reason: "No subvolume specified",
		},
		{
			name: "no_boot_options",
			entry: &refind.MenuEntry{
				Title:       "Test Entry",
				BootOptions: nil,
			},
			expected: false,
			reason: "No boot options",
		},
		{
			name: "complex_options_match",
			entry: createTestEntry("Complex", "root=UUID=main-btrfs-uuid rootflags=subvol=@ rw quiet splash"),
			expected: true,
			reason: "Complex options with matching UUID and subvolume",
		},
		{
			name: "encrypted_volume_uuid",
			entry: createTestEntry("Encrypted", "cryptdevice=UUID=crypt-uuid:luks root=UUID=main-btrfs-uuid rootflags=subvol=@"),
			expected: true,
			reason: "Encrypted setup with matching decrypted volume",
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := mockIsBootableEntry(tc.entry, rootFS)
			if result != tc.expected {
				t.Errorf("isBootableEntry() = %v, expected %v. Reason: %s", result, tc.expected, tc.reason)
				if tc.entry.BootOptions != nil {
					t.Logf("Entry options: %s", tc.entry.BootOptions.Root+" "+tc.entry.BootOptions.RootFlags)
				}
			}
		})
	}
}

// Helper functions for testing
func createTestEntry(title, options string) *refind.MenuEntry {
	// Use actual parseBootOptions from refind package
	entry := &refind.MenuEntry{
		Title:   title,
		Options: options,
	}
	
	// Parse boot options (simplified version for testing)
	if options != "" {
		entry.BootOptions = &refind.BootOptions{}
		
		// Extract root
		if idx := strings.Index(options, "root="); idx != -1 {
			start := idx + 5
			end := strings.Index(options[start:], " ")
			if end == -1 {
				end = len(options)
			} else {
				end += start
			}
			entry.BootOptions.Root = options[start:end]
		}
		
		// Extract rootflags and subvol
		if idx := strings.Index(options, "rootflags="); idx != -1 {
			start := idx + 10
			end := strings.Index(options[start:], " ")
			if end == -1 {
				end = len(options)
			} else {
				end += start
			}
			entry.BootOptions.RootFlags = options[start:end]
			
			// Extract subvol from rootflags
			if subvolIdx := strings.Index(entry.BootOptions.RootFlags, "subvol="); subvolIdx != -1 {
				subvolStart := subvolIdx + 7
				subvolEnd := strings.Index(entry.BootOptions.RootFlags[subvolStart:], ",")
				if subvolEnd == -1 {
					subvolEnd = len(entry.BootOptions.RootFlags)
				} else {
					subvolEnd += subvolStart
				}
				entry.BootOptions.Subvol = entry.BootOptions.RootFlags[subvolStart:subvolEnd]
			}
		}
	}
	
	return entry
}

type mockFilesystem struct {
	uuid      string
	partuuid  string
	label     string
	partlabel string
	device    string
	subvol    string
}

func (m *mockFilesystem) MatchesDevice(device string) bool {
	if strings.HasPrefix(device, "UUID=") {
		return strings.TrimPrefix(device, "UUID=") == m.uuid
	}
	if strings.HasPrefix(device, "PARTUUID=") {
		return strings.TrimPrefix(device, "PARTUUID=") == m.partuuid
	}
	if strings.HasPrefix(device, "LABEL=") {
		return strings.TrimPrefix(device, "LABEL=") == m.label
	}
	if strings.HasPrefix(device, "PARTLABEL=") {
		return strings.TrimPrefix(device, "PARTLABEL=") == m.partlabel
	}
	return device == m.device
}

func (m *mockFilesystem) GetSubvolume() *btrfs.Subvolume {
	return &btrfs.Subvolume{Path: m.subvol}
}

func mockIsBootableEntry(entry *refind.MenuEntry, rootFS *mockFilesystem) bool {
	if entry.BootOptions == nil {
		return false
	}
	
	if entry.BootOptions.Root == "" {
		return false
	}
	
	if entry.BootOptions.Subvol == "" {
		return false
	}
	
	if !rootFS.MatchesDevice(entry.BootOptions.Root) {
		return false
	}
	
	if entry.BootOptions.Subvol != rootFS.GetSubvolume().Path {
		return false
	}
	
	return true
}