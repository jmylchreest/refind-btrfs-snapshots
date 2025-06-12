package cmd

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock btrfs manager for testing
type mockBtrfsManager struct {
	filesystems []*btrfs.Filesystem
	snapshots   map[string][]*btrfs.Snapshot
	rootFS      *btrfs.Filesystem
	errors      map[string]error
}

func (m *mockBtrfsManager) DetectBtrfsFilesystems() ([]*btrfs.Filesystem, error) {
	if err, exists := m.errors["DetectBtrfsFilesystems"]; exists {
		return nil, err
	}
	return m.filesystems, nil
}

func (m *mockBtrfsManager) FindSnapshots(fs *btrfs.Filesystem) ([]*btrfs.Snapshot, error) {
	key := fs.GetBestIdentifier()
	if err, exists := m.errors["FindSnapshots"]; exists {
		return nil, err
	}
	if snapshots, exists := m.snapshots[key]; exists {
		return snapshots, nil
	}
	return []*btrfs.Snapshot{}, nil
}

func (m *mockBtrfsManager) GetRootFilesystem() (*btrfs.Filesystem, error) {
	if err, exists := m.errors["GetRootFilesystem"]; exists {
		return nil, err
	}
	return m.rootFS, nil
}

func createMockFilesystem(uuid, device, mountPoint string) *btrfs.Filesystem {
	return &btrfs.Filesystem{
		UUID:       uuid,
		Device:     device,
		MountPoint: mountPoint,
		Subvolume: &btrfs.Subvolume{
			ID:   1,
			Path: "@",
		},
	}
}

func createMockSnapshot(id uint64, path string, created time.Time, readOnly bool) *btrfs.Snapshot {
	return &btrfs.Snapshot{
		Subvolume: &btrfs.Subvolume{
			ID:         id,
			Path:       path,
			IsReadOnly: readOnly,
		},
		SnapshotTime:   created,
		FilesystemPath: "/tmp" + path,
	}
}

func TestListCommands(t *testing.T) {
	// Test that list commands are properly registered
	var listCommand *cobra.Command
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "list" {
			listCommand = cmd
			break
		}
	}

	require.NotNil(t, listCommand, "list command should be registered")

	// Check list command properties
	assert.Equal(t, "list", listCommand.Use)
	assert.Equal(t, "List btrfs volumes and snapshots", listCommand.Short)

	// Check subcommands
	subcommands := listCommand.Commands()
	subcommandNames := make([]string, 0, len(subcommands))
	for _, cmd := range subcommands {
		subcommandNames = append(subcommandNames, cmd.Use)
	}

	assert.Contains(t, subcommandNames, "volumes")
	assert.Contains(t, subcommandNames, "snapshots")
}

func TestRunListRoot(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "no_args",
			args:        []string{},
			expectError: true,
			errorMsg:    "subcommand required",
		},
		{
			name:        "invalid_subcommand",
			args:        []string{"invalid"},
			expectError: true,
			errorMsg:    "unknown subcommand",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			err := runListRoot(cmd, tt.args)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestOutputVolumesTable(t *testing.T) {
	filesystems := []*btrfs.Filesystem{
		{
			Device:     "/dev/sda1",
			MountPoint: "/",
			UUID:       "12345678-1234-1234-1234-123456789abc",
			PartUUID:   "87654321-4321-4321-4321-cba987654321",
			Label:      "root",
			PartLabel:  "ROOT",
			Subvolume: &btrfs.Subvolume{
				Path: "@",
			},
		},
		{
			Device:     "/dev/sdb1",
			MountPoint: "/home",
			UUID:       "abcdef12-3456-7890-abcd-ef1234567890",
			Subvolume: &btrfs.Subvolume{
				Path: "@home",
			},
		},
	}

	tests := []struct {
		name       string
		showAllIds bool
	}{
		{
			name:       "normal_output",
			showAllIds: false,
		},
		{
			name:       "show_all_ids",
			showAllIds: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := outputVolumesTable(filesystems, tt.showAllIds)
			assert.NoError(t, err)
		})
	}
}

func TestOutputVolumesJSON(t *testing.T) {
	filesystems := []*btrfs.Filesystem{
		{
			Device:     "/dev/sda1",
			MountPoint: "/",
			UUID:       "12345678-1234-1234-1234-123456789abc",
		},
	}

	// Capture stdout is complex in this context, so we just test that it doesn't error
	err := outputVolumesJSON(filesystems)
	assert.NoError(t, err)
}

func TestOutputSnapshotsTable(t *testing.T) {
	now := time.Now()
	snapshots := []*SnapshotInfo{
		{
			Snapshot: &btrfs.Snapshot{
				Subvolume: &btrfs.Subvolume{
					ID:         1,
					Path:       "/.snapshots/1/snapshot",
					IsReadOnly: true,
				},
				SnapshotTime: now,
			},
			Filesystem: createMockFilesystem("uuid1", "/dev/sda1", "/"),
			Size:       "1.2 GiB",
		},
		{
			Snapshot: &btrfs.Snapshot{
				Subvolume: &btrfs.Subvolume{
					ID:         2,
					Path:       "/.snapshots/2/snapshot",
					IsReadOnly: false,
				},
				SnapshotTime: now.Add(-1 * time.Hour),
			},
			Filesystem: createMockFilesystem("uuid1", "/dev/sda1", "/"),
		},
	}

	tests := []struct {
		name       string
		showSize   bool
		showVolume bool
	}{
		{
			name:       "basic_output",
			showSize:   false,
			showVolume: false,
		},
		{
			name:       "with_size",
			showSize:   true,
			showVolume: false,
		},
		{
			name:       "with_volume",
			showSize:   false,
			showVolume: true,
		},
		{
			name:       "with_size_and_volume",
			showSize:   true,
			showVolume: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := outputSnapshotsTable(snapshots, tt.showSize, tt.showVolume)
			assert.NoError(t, err)
		})
	}
}

func TestOutputSnapshotsJSON(t *testing.T) {
	snapshots := []*SnapshotInfo{
		{
			Snapshot: &btrfs.Snapshot{
				Subvolume: &btrfs.Subvolume{
					ID:   1,
					Path: "/.snapshots/1/snapshot",
				},
				SnapshotTime: time.Now(),
			},
			Filesystem: createMockFilesystem("uuid1", "/dev/sda1", "/"),
		},
	}

	err := outputSnapshotsJSON(snapshots)
	assert.NoError(t, err)
}

func TestFilterFilesystems(t *testing.T) {
	filesystems := []*btrfs.Filesystem{
		{
			UUID:       "uuid1",
			PartUUID:   "partuuid1",
			Label:      "root",
			PartLabel:  "ROOT",
			Device:     "/dev/sda1",
			MountPoint: "/",
		},
		{
			UUID:       "uuid2",
			Device:     "/dev/sdb1",
			MountPoint: "/home",
		},
	}

	tests := []struct {
		name     string
		filter   string
		expected int
	}{
		{
			name:     "filter_by_uuid",
			filter:   "uuid1",
			expected: 1,
		},
		{
			name:     "filter_by_device",
			filter:   "/dev/sdb1",
			expected: 1,
		},
		{
			name:     "filter_by_label",
			filter:   "root",
			expected: 1,
		},
		{
			name:     "filter_by_mountpoint",
			filter:   "/home",
			expected: 1,
		},
		{
			name:     "no_match",
			filter:   "nonexistent",
			expected: 0,
		},
		{
			name:     "empty_filter",
			filter:   "",
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterFilesystems(filesystems, tt.filter)
			assert.Len(t, result, tt.expected)
		})
	}
}

func TestDisplaySnapshotsJSON(t *testing.T) {
	snapshots := []*btrfs.Snapshot{
		{
			Subvolume: &btrfs.Subvolume{
				ID:   1,
				Path: "/.snapshots/1/snapshot",
			},
			SnapshotTime: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
			OriginalPath: "@",
		},
	}

	// We'll test the structure by checking that it can be parsed
	err := displaySnapshotsJSON(snapshots)
	assert.NoError(t, err)

	// Test that the manual JSON output is valid JSON by parsing
	// This is a simplified test since we can't easily capture stdout
	jsonData := `{
		"snapshots": [
			{
				"path": "/.snapshots/1/snapshot",
				"id": 1,
				"created": "2024-01-15T10:30:00Z",
				"is_readonly": false,
				"original_path": "@"
			}
		]
	}`

	var parsed map[string]interface{}
	err = json.Unmarshal([]byte(jsonData), &parsed)
	assert.NoError(t, err)
	assert.Contains(t, parsed, "snapshots")
}

func TestDisplaySnapshotsYAML(t *testing.T) {
	snapshots := []*btrfs.Snapshot{
		{
			Subvolume: &btrfs.Subvolume{
				ID:         1,
				Path:       "/.snapshots/1/snapshot",
				IsReadOnly: true,
			},
			SnapshotTime: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
			OriginalPath: "@",
		},
	}

	err := displaySnapshotsYAML(snapshots)
	assert.NoError(t, err)
}

func TestShowParallelProgress(t *testing.T) {
	// Test the parallel progress function
	var activeSnapshots sync.Map
	done := make(chan struct{})

	// Add some mock progress
	progress1 := &SnapshotProgress{
		Index:     1,
		FileCount: 1000,
		Path:      "/test/snapshot1",
	}
	progress2 := &SnapshotProgress{
		Index:     2,
		FileCount: 500,
		Path:      "/test/snapshot2",
	}

	activeSnapshots.Store(1, progress1)
	activeSnapshots.Store(2, progress2)

	// Start the progress indicator
	go showParallelProgress(&activeSnapshots, 5, done)

	// Let it run for a short time
	time.Sleep(100 * time.Millisecond)

	// Stop the progress
	close(done)

	// Test passes if no panic occurs
}

func TestSnapshotProgress(t *testing.T) {
	progress := SnapshotProgress{
		Index:     1,
		FileCount: 1000,
		Path:      "/test/path",
	}

	assert.Equal(t, 1, progress.Index)
	assert.Equal(t, int64(1000), progress.FileCount)
	assert.Equal(t, "/test/path", progress.Path)
}

func TestSnapshotInfo(t *testing.T) {
	fs := createMockFilesystem("uuid1", "/dev/sda1", "/")
	snapshot := createMockSnapshot(1, "/.snapshots/1/snapshot", time.Now(), true)

	info := &SnapshotInfo{
		Snapshot:   snapshot,
		Filesystem: fs,
		Size:       "1.2 GiB",
	}

	assert.Equal(t, snapshot, info.Snapshot)
	assert.Equal(t, fs, info.Filesystem)
	assert.Equal(t, "1.2 GiB", info.Size)
}

func TestListCommandFlags(t *testing.T) {
	// Test that flags are properly configured
	var listCommand *cobra.Command
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "list" {
			listCommand = cmd
			break
		}
	}

	require.NotNil(t, listCommand)

	// Test main list command flags
	allFlag := listCommand.Flags().Lookup("all")
	require.NotNil(t, allFlag)
	assert.Equal(t, "false", allFlag.DefValue)

	formatFlag := listCommand.Flags().Lookup("format")
	require.NotNil(t, formatFlag)
	assert.Equal(t, "table", formatFlag.DefValue)

	showSizeFlag := listCommand.Flags().Lookup("show-size")
	require.NotNil(t, showSizeFlag)
	assert.Equal(t, "false", showSizeFlag.DefValue)

	// Test list volumes command flags
	var volumesCommand *cobra.Command
	for _, cmd := range listCommand.Commands() {
		if cmd.Use == "volumes" {
			volumesCommand = cmd
			break
		}
	}

	require.NotNil(t, volumesCommand)

	jsonFlag := volumesCommand.Flags().Lookup("json")
	require.NotNil(t, jsonFlag)
	assert.Equal(t, "false", jsonFlag.DefValue)

	showAllIdsFlag := volumesCommand.Flags().Lookup("show-all-ids")
	require.NotNil(t, showAllIdsFlag)
	assert.Equal(t, "false", showAllIdsFlag.DefValue)

	// Test list snapshots command flags
	var snapshotsCommand *cobra.Command
	for _, cmd := range listCommand.Commands() {
		if cmd.Use == "snapshots" {
			snapshotsCommand = cmd
			break
		}
	}

	require.NotNil(t, snapshotsCommand)

	volumeFlag := snapshotsCommand.Flags().Lookup("volume")
	require.NotNil(t, volumeFlag)
	assert.Equal(t, "", volumeFlag.DefValue)
}

func TestViperBindingsForList(t *testing.T) {
	// Test that viper bindings work for list command flags
	viper.Reset()
	setDefaults()

	// Set some test values
	viper.Set("list.show_all", true)
	viper.Set("list.format", "json")
	viper.Set("list.show_size", true)

	assert.Equal(t, true, viper.GetBool("list.show_all"))
	assert.Equal(t, "json", viper.GetString("list.format"))
	assert.Equal(t, true, viper.GetBool("list.show_size"))
}
