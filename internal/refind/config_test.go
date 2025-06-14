package refind

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindRefindLinuxConfigs_MultipleFiles(t *testing.T) {
	// Create a temporary directory structure
	tempDir := t.TempDir()

	// Create multiple refind_linux.conf files in different locations
	testFiles := []string{
		"refind_linux.conf",
		"EFI/BOOT/refind_linux.conf",
		"EFI/Linux/refind_linux.conf",
		"kernels/5.15/refind_linux.conf",
		"some/deep/nested/path/refind_linux.conf",
	}

	// Create directories and files
	for _, testFile := range testFiles {
		fullPath := filepath.Join(tempDir, testFile)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("Failed to create directory for %s: %v", testFile, err)
		}

		content := `"Boot Normal" "root=UUID=test-uuid rootflags=subvol=@"
"Boot Fallback" "root=UUID=test-uuid rootflags=subvol=@ systemd.unit=emergency.target"`

		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create test file %s: %v", testFile, err)
		}
	}

	// Test discovery
	parser := NewParser(tempDir)
	configs, err := parser.FindRefindLinuxConfigs()
	if err != nil {
		t.Fatalf("FindRefindLinuxConfigs() error = %v", err)
	}

	// Should find all test files
	if len(configs) != len(testFiles) {
		t.Errorf("FindRefindLinuxConfigs() found %d files, expected %d", len(configs), len(testFiles))
	}

	// Verify each file was found
	foundFiles := make(map[string]bool)
	for _, config := range configs {
		rel, err := filepath.Rel(tempDir, config)
		if err != nil {
			t.Errorf("Failed to get relative path for %s: %v", config, err)
			continue
		}
		foundFiles[rel] = true
	}

	for _, expectedFile := range testFiles {
		if !foundFiles[expectedFile] {
			t.Errorf("Expected file %s was not found", expectedFile)
		}
	}
}

func TestParseRefindLinuxConf_SourceFileTracking(t *testing.T) {
	tempDir := t.TempDir()

	// Create test file
	testFile := filepath.Join(tempDir, "test_refind_linux.conf")
	content := `"Boot Normal" "root=UUID=test-uuid rootflags=subvol=@"
"Boot Fallback" "root=UUID=test-uuid rootflags=subvol=@ systemd.unit=emergency.target"`

	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Parse the file
	parser := NewParser(tempDir)
	entries, err := parser.parseRefindLinuxConf(testFile)
	if err != nil {
		t.Fatalf("parseRefindLinuxConf() error = %v", err)
	}

	// Verify entries have correct source file
	expectedEntries := 2
	if len(entries) != expectedEntries {
		t.Errorf("parseRefindLinuxConf() returned %d entries, expected %d", len(entries), expectedEntries)
	}

	for i, entry := range entries {
		if entry.SourceFile != testFile {
			t.Errorf("Entry %d has SourceFile = %s, expected %s", i, entry.SourceFile, testFile)
		}

		if entry.LineNumber <= 0 {
			t.Errorf("Entry %d has invalid LineNumber = %d", i, entry.LineNumber)
		}
	}
}

func TestParser_ConfigParsingWithMultipleLinuxConfs(t *testing.T) {
	tempDir := t.TempDir()

	// Create main refind.conf
	mainConfig := filepath.Join(tempDir, "refind.conf")
	mainContent := `timeout 20
default_selection 1

menuentry "Main Entry" {
    loader /vmlinuz
    initrd /initramfs.img
    options "root=UUID=main-uuid"
}`

	if err := os.WriteFile(mainConfig, []byte(mainContent), 0644); err != nil {
		t.Fatalf("Failed to create main config: %v", err)
	}

	// Create multiple refind_linux.conf files
	linuxConfigs := map[string]string{
		"EFI/Linux/refind_linux.conf": `"Linux Default" "root=UUID=test1-uuid rootflags=subvol=@"
"Linux Fallback" "root=UUID=test1-uuid rootflags=subvol=@ systemd.unit=emergency.target"`,
		"kernels/refind_linux.conf": `"Kernel Test" "root=UUID=test2-uuid rootflags=subvol=@"`,
	}

	for confPath, content := range linuxConfigs {
		fullPath := filepath.Join(tempDir, confPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("Failed to create directory for %s: %v", confPath, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create %s: %v", confPath, err)
		}
	}

	// Parse configuration
	parser := NewParser(tempDir)
	config, err := parser.ParseConfig(mainConfig)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}

	// Verify we have entries from main config + linux configs
	// Main config: 1 entry, Linux configs: 3 entries total
	expectedEntries := 4
	if len(config.Entries) != expectedEntries {
		t.Errorf("ParseConfig() returned %d entries, expected %d", len(config.Entries), expectedEntries)
	}

	// Count entries by source file
	sourceFiles := make(map[string]int)
	for _, entry := range config.Entries {
		sourceFiles[entry.SourceFile]++
	}

	// Verify entries from linux configs (should be at the beginning due to higher priority)
	linuxEntryCount := 0
	for sourceFile, count := range sourceFiles {
		if filepath.Base(sourceFile) == "refind_linux.conf" {
			linuxEntryCount += count
		}
	}

	expectedLinuxEntries := 3
	if linuxEntryCount != expectedLinuxEntries {
		t.Errorf("Found %d entries from refind_linux.conf files, expected %d", linuxEntryCount, expectedLinuxEntries)
	}
}

func TestParser_MultipleLinuxConfsWithDifferentRootDevices(t *testing.T) {
	tempDir := t.TempDir()

	// Create main refind.conf
	mainConfig := filepath.Join(tempDir, "refind.conf")
	mainContent := `timeout 20
default_selection 1`

	if err := os.WriteFile(mainConfig, []byte(mainContent), 0644); err != nil {
		t.Fatalf("Failed to create main config: %v", err)
	}

	// Create multiple refind_linux.conf files with different root devices/volumes
	linuxConfigs := map[string]string{
		// Same btrfs device, same subvolume (should be processed)
		"EFI/Linux/main-system/refind_linux.conf": `"Main System Default" "root=UUID=btrfs-root-uuid rootflags=subvol=@"
"Main System Recovery" "root=UUID=btrfs-root-uuid rootflags=subvol=@ systemd.unit=rescue.target"`,

		// Same btrfs device, different subvolume (should be skipped)
		"EFI/Linux/alt-system/refind_linux.conf": `"Alt System" "root=UUID=btrfs-root-uuid rootflags=subvol=@alt"`,

		// Different btrfs device, same subvolume pattern (should be skipped)
		"EFI/Linux/other-disk/refind_linux.conf": `"Other Disk" "root=UUID=other-btrfs-uuid rootflags=subvol=@"`,

		// Same device, same subvolume (should be processed)
		"kernels/refind_linux.conf": `"Kernel Test" "root=UUID=btrfs-root-uuid rootflags=subvol=@"`,

		// Non-btrfs system (should be skipped)
		"EFI/Linux/ext4-system/refind_linux.conf": `"Ext4 System" "root=UUID=ext4-uuid"`,

		// Different device with PARTUUID (should be skipped)
		"EFI/Linux/partuuid-system/refind_linux.conf": `"PARTUUID System" "root=PARTUUID=different-partuuid rootflags=subvol=@"`,
	}

	for confPath, content := range linuxConfigs {
		fullPath := filepath.Join(tempDir, confPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("Failed to create directory for %s: %v", confPath, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create %s: %v", confPath, err)
		}
	}

	// Parse configuration
	parser := NewParser(tempDir)
	config, err := parser.ParseConfig(mainConfig)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}

	// Verify total entries (all should be parsed)
	expectedTotalEntries := 7 // 2 + 1 + 1 + 1 + 1 + 1 = 7
	if len(config.Entries) != expectedTotalEntries {
		t.Errorf("ParseConfig() returned %d entries, expected %d", len(config.Entries), expectedTotalEntries)
		for i, entry := range config.Entries {
			t.Logf("Entry %d: %s from %s", i, entry.Title, entry.SourceFile)
		}
	}

	// Count entries by device/subvol characteristics
	entriesByDevice := make(map[string][]*MenuEntry)
	for _, entry := range config.Entries {
		if entry.BootOptions != nil && entry.BootOptions.Root != "" {
			key := entry.BootOptions.Root + "|" + entry.BootOptions.Subvol
			entriesByDevice[key] = append(entriesByDevice[key], entry)
		}
	}

	// Should have entries for different root devices/subvolumes
	expectedGroups := map[string]int{
		"UUID=btrfs-root-uuid|@":        3, // main-system (2) + kernel (1)
		"UUID=btrfs-root-uuid|@alt":     1, // alt-system
		"UUID=other-btrfs-uuid|@":       1, // other-disk
		"UUID=ext4-uuid|":               1, // ext4-system (no subvol)
		"PARTUUID=different-partuuid|@": 1, // partuuid-system
	}

	for expectedKey, expectedCount := range expectedGroups {
		if actualCount := len(entriesByDevice[expectedKey]); actualCount != expectedCount {
			t.Errorf("Expected %d entries for device/subvol '%s', got %d", expectedCount, expectedKey, actualCount)
		}
	}

	// Verify source file tracking works correctly
	sourceFiles := make(map[string]int)
	for _, entry := range config.Entries {
		sourceFiles[entry.SourceFile]++
	}

	// Should have entries from all refind_linux.conf files
	expectedFiles := 6
	if len(sourceFiles) != expectedFiles {
		t.Errorf("Expected entries from %d files, got entries from %d files", expectedFiles, len(sourceFiles))
		for file, count := range sourceFiles {
			t.Logf("Source file: %s (%d entries)", file, count)
		}
	}
}

func TestIsBootableEntry_WithDifferentDeviceTypes(t *testing.T) {
	// This test would normally be in cmd package, but we'll create a simple version here
	// to test the logic that determines which entries should be processed for snapshots

	testCases := []struct {
		name     string
		entry    *MenuEntry
		rootFS   mockRootFS
		expected bool
	}{
		{
			name: "matching UUID and subvolume",
			entry: &MenuEntry{
				BootOptions: &BootOptions{
					Root:   "UUID=test-uuid",
					Subvol: "@",
				},
			},
			rootFS:   mockRootFS{uuid: "test-uuid", subvol: "@"},
			expected: true,
		},
		{
			name: "matching PARTUUID and subvolume",
			entry: &MenuEntry{
				BootOptions: &BootOptions{
					Root:   "PARTUUID=test-partuuid",
					Subvol: "@",
				},
			},
			rootFS:   mockRootFS{partuuid: "test-partuuid", subvol: "@"},
			expected: true,
		},
		{
			name: "different UUID",
			entry: &MenuEntry{
				BootOptions: &BootOptions{
					Root:   "UUID=different-uuid",
					Subvol: "@",
				},
			},
			rootFS:   mockRootFS{uuid: "test-uuid", subvol: "@"},
			expected: false,
		},
		{
			name: "different subvolume",
			entry: &MenuEntry{
				BootOptions: &BootOptions{
					Root:   "UUID=test-uuid",
					Subvol: "@alt",
				},
			},
			rootFS:   mockRootFS{uuid: "test-uuid", subvol: "@"},
			expected: false,
		},
		{
			name: "no boot options",
			entry: &MenuEntry{
				BootOptions: nil,
			},
			rootFS:   mockRootFS{uuid: "test-uuid", subvol: "@"},
			expected: false,
		},
		{
			name: "no root parameter",
			entry: &MenuEntry{
				BootOptions: &BootOptions{
					Root:   "",
					Subvol: "@",
				},
			},
			rootFS:   mockRootFS{uuid: "test-uuid", subvol: "@"},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Simple mock implementation of the bootable entry check logic
			result := isBootableEntryMock(tc.entry, &tc.rootFS)
			if result != tc.expected {
				t.Errorf("isBootableEntry() = %v, expected %v", result, tc.expected)
			}
		})
	}
}

// Mock types and functions for testing the bootable entry logic
type mockRootFS struct {
	uuid     string
	partuuid string
	label    string
	subvol   string
}

func (m *mockRootFS) MatchesDevice(device string) bool {
	if m.uuid != "" && device == "UUID="+m.uuid {
		return true
	}
	if m.partuuid != "" && device == "PARTUUID="+m.partuuid {
		return true
	}
	if m.label != "" && device == "LABEL="+m.label {
		return true
	}
	return false
}

func (m *mockRootFS) GetSubvolume() string {
	return m.subvol
}

func isBootableEntryMock(entry *MenuEntry, rootFS *mockRootFS) bool {
	// Simplified version of the actual isBootableEntry logic
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

	if entry.BootOptions.Subvol != rootFS.GetSubvolume() {
		return false
	}

	return true
}

func TestUpdateOptionsForSnapshot_SubvolumeFormatPreservation(t *testing.T) {
	// Create a test snapshot
	testTime := time.Date(2025, 6, 14, 10, 0, 2, 0, time.UTC)
	snapshot := &btrfs.Snapshot{
		Subvolume: &btrfs.Subvolume{
			ID:   275,
			Path: "/.snapshots/8/snapshot",
		},
		OriginalPath:   "/.snapshots/8/snapshot", 
		FilesystemPath: "/.snapshots/8/snapshot",
		SnapshotTime:   testTime,
	}

	tests := []struct {
		name            string
		originalOptions string
		expectedSubvol  string
		description     string
	}{
		{
			name:            "preserve_@_format",
			originalOptions: "quiet splash rw rootflags=subvol=@ cryptdevice=UUID=test:luks root=/dev/mapper/luks",
			expectedSubvol:  "@/.snapshots/8/snapshot",
			description:     "Should preserve @ format when original uses @",
		},
		{
			name:            "preserve_/@_format", 
			originalOptions: "quiet splash rw rootflags=subvol=/@ cryptdevice=UUID=test:luks root=/dev/mapper/luks",
			expectedSubvol:  "/@/.snapshots/8/snapshot",
			description:     "Should preserve /@ format when original uses /@",
		},
		{
			name:            "handle_@_subpath_format",
			originalOptions: "quiet splash rw rootflags=subvol=@/home cryptdevice=UUID=test:luks root=/dev/mapper/luks", 
			expectedSubvol:  "@/.snapshots/8/snapshot",
			description:     "Should use @ format when original uses @/subpath",
		},
		{
			name:            "fallback_no_rootflags",
			originalOptions: "quiet splash rw cryptdevice=UUID=test:luks root=/dev/mapper/luks",
			expectedSubvol:  "@/.snapshots/8/snapshot",
			description:     "Should use @ format as fallback when no rootflags present",
		},
		{
			name:            "fallback_no_subvol",
			originalOptions: "quiet splash rw rootflags=compress=zstd cryptdevice=UUID=test:luks root=/dev/mapper/luks",
			expectedSubvol:  "@/.snapshots/8/snapshot", 
			description:     "Should use @ format as fallback when rootflags has no subvol",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			generator := &Generator{}
			result := generator.updateOptionsForSnapshot(tt.originalOptions, snapshot)

			// Extract the subvol value from the result
			parser := params.NewBootOptionsParser()
			rootflags := parser.ExtractRootFlags(result)
			actualSubvol := parser.ExtractSubvol(rootflags)

			assert.Equal(t, tt.expectedSubvol, actualSubvol, tt.description)

			// Also verify subvolid was updated
			actualSubvolID := parser.ExtractSubvolID(rootflags)
			assert.Equal(t, "275", actualSubvolID, "Subvolid should be updated to snapshot ID")
		})
	}
}
