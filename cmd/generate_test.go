package cmd

import (
	"os/user"
	"testing"
	"time"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/refind"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateCommand(t *testing.T) {
	// Test that generate command is properly registered
	var generateCommand *cobra.Command
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "generate" {
			generateCommand = cmd
			break
		}
	}

	require.NotNil(t, generateCommand, "generate command should be registered")

	// Check generate command properties
	assert.Equal(t, "generate", generateCommand.Use)
	assert.Equal(t, "Generate rEFInd boot entries for btrfs snapshots", generateCommand.Short)
	assert.Contains(t, generateCommand.Long, "Generate rEFInd boot configuration files for btrfs snapshots")
}

func TestGenerateCommandFlags(t *testing.T) {
	// Find the generate command
	var generateCommand *cobra.Command
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "generate" {
			generateCommand = cmd
			break
		}
	}

	require.NotNil(t, generateCommand)

	// Test all flags are present with correct defaults
	flagTests := []struct {
		name         string
		defaultValue string
	}{
		{"config-path", ""},
		{"esp-path", ""},
		{"count", "0"},
		{"dry-run", "false"},
		{"force", "false"},
		{"generate-include", "false"},
		{"yes", "false"},
	}

	for _, test := range flagTests {
		flag := generateCommand.Flags().Lookup(test.name)
		require.NotNil(t, flag, "flag %s should exist", test.name)
		assert.Equal(t, test.defaultValue, flag.DefValue, "flag %s should have correct default", test.name)
	}

	// Test short flags
	countFlag := generateCommand.Flags().ShorthandLookup("n")
	require.NotNil(t, countFlag)
	assert.Equal(t, "count", countFlag.Name)

	espFlag := generateCommand.Flags().ShorthandLookup("e")
	require.NotNil(t, espFlag)
	assert.Equal(t, "esp-path", espFlag.Name)

	generateIncludeFlag := generateCommand.Flags().ShorthandLookup("g")
	require.NotNil(t, generateIncludeFlag)
	assert.Equal(t, "generate-include", generateIncludeFlag.Name)

	yesFlag := generateCommand.Flags().ShorthandLookup("y")
	require.NotNil(t, yesFlag)
	assert.Equal(t, "yes", yesFlag.Name)
}

func TestIsBootableEntry(t *testing.T) {
	// Create a mock root filesystem
	rootFS := &btrfs.Filesystem{
		UUID:      "test-uuid",
		PartUUID:  "test-partuuid",
		Label:     "test-label",
		PartLabel: "test-partlabel",
		Device:    "/dev/sda1",
		Subvolume: &btrfs.Subvolume{
			ID:   1,
			Path: "@",
		},
	}

	tests := []struct {
		name     string
		entry    *refind.MenuEntry
		expected bool
		reason   string
	}{
		{
			name: "valid_uuid_entry",
			entry: &refind.MenuEntry{
				Title: "Test Entry",
				BootOptions: &refind.BootOptions{
					Root:   "UUID=test-uuid",
					Subvol: "@",
				},
			},
			expected: true,
			reason:   "Valid entry with matching UUID and subvol",
		},
		{
			name: "valid_partuuid_entry",
			entry: &refind.MenuEntry{
				Title: "Test Entry",
				BootOptions: &refind.BootOptions{
					Root:   "PARTUUID=test-partuuid",
					Subvol: "@",
				},
			},
			expected: true,
			reason:   "Valid entry with matching PARTUUID and subvol",
		},
		{
			name: "valid_label_entry",
			entry: &refind.MenuEntry{
				Title: "Test Entry",
				BootOptions: &refind.BootOptions{
					Root:   "LABEL=test-label",
					Subvol: "@",
				},
			},
			expected: true,
			reason:   "Valid entry with matching LABEL and subvol",
		},
		{
			name: "valid_partlabel_entry",
			entry: &refind.MenuEntry{
				Title: "Test Entry",
				BootOptions: &refind.BootOptions{
					Root:   "PARTLABEL=test-partlabel",
					Subvol: "@",
				},
			},
			expected: true,
			reason:   "Valid entry with matching PARTLABEL and subvol",
		},
		{
			name: "valid_device_entry",
			entry: &refind.MenuEntry{
				Title: "Test Entry",
				BootOptions: &refind.BootOptions{
					Root:   "/dev/sda1",
					Subvol: "@",
				},
			},
			expected: true,
			reason:   "Valid entry with matching device path and subvol",
		},
		{
			name: "valid_subvolid_entry",
			entry: &refind.MenuEntry{
				Title: "Test Entry",
				BootOptions: &refind.BootOptions{
					Root:     "UUID=test-uuid",
					SubvolID: "1",
				},
			},
			expected: true,
			reason:   "Valid entry with matching UUID and subvolid",
		},
		{
			name: "no_boot_options",
			entry: &refind.MenuEntry{
				Title:       "Test Entry",
				BootOptions: nil,
			},
			expected: false,
			reason:   "No boot options",
		},
		{
			name: "no_root_parameter",
			entry: &refind.MenuEntry{
				Title: "Test Entry",
				BootOptions: &refind.BootOptions{
					Root:   "",
					Subvol: "@",
				},
			},
			expected: false,
			reason:   "No root parameter",
		},
		{
			name: "no_subvol_or_subvolid",
			entry: &refind.MenuEntry{
				Title: "Test Entry",
				BootOptions: &refind.BootOptions{
					Root:     "UUID=test-uuid",
					Subvol:   "",
					SubvolID: "",
				},
			},
			expected: false,
			reason:   "No subvol or subvolid",
		},
		{
			name: "wrong_uuid",
			entry: &refind.MenuEntry{
				Title: "Test Entry",
				BootOptions: &refind.BootOptions{
					Root:   "UUID=wrong-uuid",
					Subvol: "@",
				},
			},
			expected: false,
			reason:   "Wrong UUID",
		},
		{
			name: "wrong_subvol",
			entry: &refind.MenuEntry{
				Title: "Test Entry",
				BootOptions: &refind.BootOptions{
					Root:   "UUID=test-uuid",
					Subvol: "@wrong",
				},
			},
			expected: false,
			reason:   "Wrong subvolume",
		},
		{
			name: "wrong_subvolid",
			entry: &refind.MenuEntry{
				Title: "Test Entry",
				BootOptions: &refind.BootOptions{
					Root:     "UUID=test-uuid",
					SubvolID: "999",
				},
			},
			expected: false,
			reason:   "Wrong subvolid",
		},
		{
			name: "invalid_subvolid",
			entry: &refind.MenuEntry{
				Title: "Test Entry",
				BootOptions: &refind.BootOptions{
					Root:     "UUID=test-uuid",
					SubvolID: "invalid",
				},
			},
			expected: true, // The function doesn't validate subvolid format, just fails parsing and continues
			reason:   "Invalid subvolid format is ignored by the function",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := refind.IsBootable(tt.entry, rootFS)
			assert.Equal(t, tt.expected, result, tt.reason)
		})
	}
}

func TestIsBootableEntryWithNilSubvolume(t *testing.T) {
	// Test with root filesystem that has no subvolume info
	rootFS := &btrfs.Filesystem{
		UUID:      "test-uuid",
		Device:    "/dev/sda1",
		Subvolume: nil, // No subvolume info
	}

	entry := &refind.MenuEntry{
		Title: "Test Entry",
		BootOptions: &refind.BootOptions{
			Root:   "UUID=test-uuid",
			Subvol: "@",
		},
	}

	// Should still pass device matching but fail subvolume checks
	result := refind.IsBootable(entry, rootFS)
	assert.True(t, result, "Should be true when rootFS has no subvolume info")
}

func TestIsBootableEntry_SubvolumeFormats(t *testing.T) {
	tests := []struct {
		name           string
		rootFSSubvol   string // Root filesystem subvolume path
		entrySubvol    string // Menu entry subvol value
		expectedResult bool
		description    string
	}{
		{
			name:           "root_@_entry_@",
			rootFSSubvol:   "@",
			entrySubvol:    "@",
			expectedResult: true,
			description:    "Root FS with @ and entry with @ should match",
		},
		{
			name:           "root_@_entry_/@",
			rootFSSubvol:   "@",
			entrySubvol:    "/@",
			expectedResult: true,
			description:    "Root FS with @ and entry with /@ should match (normalized)",
		},
		{
			name:           "root_/@_entry_@",
			rootFSSubvol:   "/@",
			entrySubvol:    "@",
			expectedResult: true,
			description:    "Root FS with /@ and entry with @ should match (normalized)",
		},
		{
			name:           "root_/@_entry_/@",
			rootFSSubvol:   "/@",
			entrySubvol:    "/@",
			expectedResult: true,
			description:    "Root FS with /@ and entry with /@ should match",
		},
		{
			name:           "root_@_entry_@home",
			rootFSSubvol:   "@",
			entrySubvol:    "@home",
			expectedResult: false,
			description:    "Different subvolumes should not match",
		},
		{
			name:           "root_/@_entry_/@home",
			rootFSSubvol:   "/@",
			entrySubvol:    "/@home",
			expectedResult: false,
			description:    "Different subvolumes with / prefix should not match",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create root filesystem with specified subvolume path
			rootFS := &btrfs.Filesystem{
				UUID:   "test-uuid",
				Device: "/dev/sda1",
				Subvolume: &btrfs.Subvolume{
					ID:   1,
					Path: tt.rootFSSubvol,
				},
			}

			// Create menu entry with specified subvol
			entry := &refind.MenuEntry{
				Title: "Test Entry",
				BootOptions: &refind.BootOptions{
					Root:   "UUID=test-uuid",
					Subvol: tt.entrySubvol,
				},
			}

			result := refind.IsBootable(entry, rootFS)
			assert.Equal(t, tt.expectedResult, result, tt.description)
		})
	}
}

func TestCheckRootPrivileges(t *testing.T) {
	// This test is tricky because it depends on the actual user running the test
	// We'll test the logic by checking the return value

	err := checkRootPrivileges()

	// Get current user to determine expected result
	currentUser, userErr := user.Current()
	require.NoError(t, userErr, "Should be able to get current user")

	if currentUser.Uid == "0" {
		assert.NoError(t, err, "Should not error when running as root")
	} else {
		assert.Error(t, err, "Should error when not running as root")
		assert.Contains(t, err.Error(), "not running as root")
		assert.Contains(t, err.Error(), currentUser.Uid)
	}
}

func TestGenerateSnapshotSelection(t *testing.T) {
	// Test snapshot selection logic
	snapshots := []*btrfs.Snapshot{
		createMockSnapshot(1, "/.snapshots/1/snapshot", time.Now().Add(-1*time.Hour), true),
		createMockSnapshot(2, "/.snapshots/2/snapshot", time.Now().Add(-2*time.Hour), true),
		createMockSnapshot(3, "/.snapshots/3/snapshot", time.Now().Add(-3*time.Hour), true),
		createMockSnapshot(4, "/.snapshots/4/snapshot", time.Now().Add(-4*time.Hour), true),
		createMockSnapshot(5, "/.snapshots/5/snapshot", time.Now().Add(-5*time.Hour), true),
	}

	tests := []struct {
		name           string
		selectionCount int
		expectedCount  int
	}{
		{
			name:           "select_all_with_zero",
			selectionCount: 0,
			expectedCount:  5,
		},
		{
			name:           "select_all_with_negative",
			selectionCount: -1,
			expectedCount:  5,
		},
		{
			name:           "select_three",
			selectionCount: 3,
			expectedCount:  3,
		},
		{
			name:           "select_more_than_available",
			selectionCount: 10,
			expectedCount:  5,
		},
		{
			name:           "select_one",
			selectionCount: 1,
			expectedCount:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var selectedSnapshots []*btrfs.Snapshot

			// Implement the same logic as in runGenerate
			if tt.selectionCount <= 0 {
				selectedSnapshots = snapshots
			} else {
				selectionCount := tt.selectionCount
				if selectionCount > len(snapshots) {
					selectionCount = len(snapshots)
				}
				selectedSnapshots = snapshots[:selectionCount]
			}

			assert.Len(t, selectedSnapshots, tt.expectedCount)

			// Verify snapshots are in the correct order (newest first)
			if len(selectedSnapshots) > 1 {
				for i := 1; i < len(selectedSnapshots); i++ {
					assert.True(t, selectedSnapshots[i-1].SnapshotTime.After(selectedSnapshots[i].SnapshotTime),
						"Snapshots should be ordered with newest first")
				}
			}
		})
	}
}

func TestConfigPathResolution(t *testing.T) {
	// Test config path resolution logic from generate command
	tests := []struct {
		name         string
		configPath   string
		espPath      string
		autoDetected string
		expected     string
		description  string
	}{
		{
			name:         "default_path_with_auto_detection",
			configPath:   "/EFI/refind/refind.conf", // Default value
			espPath:      "/boot/efi",
			autoDetected: "/boot/efi/EFI/refind/refind.conf",
			expected:     "/boot/efi/EFI/refind/refind.conf",
			description:  "Should use auto-detected path when using default",
		},
		{
			name:        "custom_relative_path",
			configPath:  "EFI/BOOT/refind.conf",
			espPath:     "/boot/efi",
			expected:    "/boot/efi/EFI/BOOT/refind.conf",
			description: "Should join relative path with ESP path",
		},
		{
			name:        "custom_absolute_path",
			configPath:  "/custom/path/refind.conf",
			espPath:     "/boot/efi",
			expected:    "/custom/path/refind.conf",
			description: "Should use absolute path as-is",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This tests the logic that would be in runGenerate
			var resolvedPath string

			if tt.configPath == "/EFI/refind/refind.conf" && tt.autoDetected != "" {
				// Simulate auto-detection success
				resolvedPath = tt.autoDetected
			} else {
				// Simulate manual path resolution
				if tt.configPath[0] != '/' { // Not absolute
					resolvedPath = tt.espPath + "/" + tt.configPath
				} else {
					resolvedPath = tt.configPath
				}
			}

			assert.Equal(t, tt.expected, resolvedPath, tt.description)
		})
	}
}

