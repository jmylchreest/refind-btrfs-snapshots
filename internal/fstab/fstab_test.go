package fstab

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/runner"
)

func TestNewManager(t *testing.T) {
	manager := NewManager()
	if manager == nil {
		t.Fatal("NewManager() returned nil")
	}
}

func TestManager_ParseFstab(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantEntries int
		wantLines   int
		wantErr     bool
	}{
		{
			name: "valid fstab",
			content: `# /etc/fstab: static file system information.
#
UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=@,defaults 0 1
UUID=12345678-1234-1234-1234-123456789abc /home btrfs subvol=@home,defaults 0 2
/dev/sda1 /boot/efi vfat defaults 0 2`,
			wantEntries: 3,
			wantLines:   5,
			wantErr:     false,
		},
		{
			name:        "empty file",
			content:     "",
			wantEntries: 0,
			wantLines:   0,
			wantErr:     false,
		},
		{
			name: "comments only",
			content: `# This is a comment
# Another comment`,
			wantEntries: 0,
			wantLines:   2,
			wantErr:     false,
		},
		{
			name: "mixed content",
			content: `# Comment
UUID=test / btrfs defaults 0 1

# Another comment
/dev/sda1 /boot ext4 defaults 0 2`,
			wantEntries: 2,
			wantLines:   5,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary file
			tmpFile := createTempFile(t, tt.content)
			defer os.Remove(tmpFile)

			manager := NewManager()
			fstab, err := manager.ParseFstab(tmpFile)

			if (err != nil) != tt.wantErr {
				t.Errorf("ParseFstab() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if err == nil {
				if len(fstab.Entries) != tt.wantEntries {
					t.Errorf("ParseFstab() entries = %v, want %v", len(fstab.Entries), tt.wantEntries)
				}
				if len(fstab.Lines) != tt.wantLines {
					t.Errorf("ParseFstab() lines = %v, want %v", len(fstab.Lines), tt.wantLines)
				}
			}
		})
	}
}

func TestManager_ParseFstab_NonExistentFile(t *testing.T) {
	manager := NewManager()
	_, err := manager.ParseFstab("/non/existent/file")
	if err == nil {
		t.Error("ParseFstab() should return error for non-existent file")
	}
}

func TestManager_parseFstabLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want *Entry
	}{
		{
			name: "basic entry",
			line: "UUID=test / btrfs defaults 0 1",
			want: &Entry{
				Device:     "UUID=test",
				Mountpoint: "/",
				FSType:     "btrfs",
				Options:    "defaults",
				Dump:       "0",
				Pass:       "1",
				Original:   "UUID=test / btrfs defaults 0 1",
			},
		},
		{
			name: "minimal entry",
			line: "/dev/sda1 /boot ext4 defaults",
			want: &Entry{
				Device:     "/dev/sda1",
				Mountpoint: "/boot",
				FSType:     "ext4",
				Options:    "defaults",
				Dump:       "0",
				Pass:       "0",
				Original:   "/dev/sda1 /boot ext4 defaults",
			},
		},
		{
			name: "invalid entry",
			line: "incomplete",
			want: nil,
		},
		{
			name: "entry with spaces",
			line: "  UUID=test  /  btrfs  defaults  0  1  ",
			want: &Entry{
				Device:     "UUID=test",
				Mountpoint: "/",
				FSType:     "btrfs",
				Options:    "defaults",
				Dump:       "0",
				Pass:       "1",
				Original:   "  UUID=test  /  btrfs  defaults  0  1  ",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewManager()
			got := manager.parseFstabLine(tt.line)

			if tt.want == nil && got != nil {
				t.Errorf("parseFstabLine() = %v, want nil", got)
				return
			}

			if tt.want != nil && got == nil {
				t.Errorf("parseFstabLine() = nil, want %v", tt.want)
				return
			}

			if tt.want != nil && got != nil {
				if !reflect.DeepEqual(got, tt.want) {
					t.Errorf("parseFstabLine() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestManager_UpdateSnapshotFstabDiff(t *testing.T) {
	// Create test snapshot and filesystem
	snapshot := &btrfs.Snapshot{
		Subvolume: &btrfs.Subvolume{
			ID:   256,
			Path: "/@snapshots/1/snapshot",
		},
		FilesystemPath: "/tmp/test-snapshot",
	}

	rootFS := &btrfs.Filesystem{
		UUID:   "12345678-1234-1234-1234-123456789abc",
		Device: "/dev/sda2",
	}

	// Create temporary fstab file
	fstabContent := `# /etc/fstab
UUID=12345678-1234-1234-1234-123456789abc / btrfs subvol=@,defaults 0 1
UUID=other-uuid /home btrfs subvol=@home,defaults 0 2`

	// Create temporary directory structure
	tmpDir := t.TempDir()
	snapshotDir := filepath.Join(tmpDir, "snapshot")
	etcDir := filepath.Join(snapshotDir, "etc")
	if err := os.MkdirAll(etcDir, 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	fstabPath := filepath.Join(etcDir, "fstab")
	if err := os.WriteFile(fstabPath, []byte(fstabContent), 0644); err != nil {
		t.Fatalf("Failed to create test fstab: %v", err)
	}

	// Update snapshot to use test path
	snapshot.FilesystemPath = snapshotDir

	manager := NewManager()
	fileDiff, err := manager.UpdateSnapshotFstabDiff(snapshot, rootFS)

	if err != nil {
		t.Errorf("UpdateSnapshotFstabDiff() error = %v", err)
		return
	}

	if fileDiff == nil {
		t.Error("UpdateSnapshotFstabDiff() returned nil diff, expected changes")
		return
	}

	// Verify the diff contains expected changes
	if !strings.Contains(fileDiff.Modified, "subvol=/@snapshots/1/snapshot") {
		t.Error("UpdateSnapshotFstabDiff() should update subvol option")
	}

	if !strings.Contains(fileDiff.Modified, "subvolid=256") {
		t.Error("UpdateSnapshotFstabDiff() should update subvolid option")
	}
}

func TestManager_UpdateSnapshotFstabDiff_NoChanges(t *testing.T) {
	snapshot := &btrfs.Snapshot{
		Subvolume: &btrfs.Subvolume{
			ID:   256,
			Path: "/@snapshots/1/snapshot",
		},
		FilesystemPath: "/tmp/test-snapshot",
	}

	rootFS := &btrfs.Filesystem{
		UUID:   "different-uuid",
		Device: "/dev/sdb2",
	}

	// Create fstab that doesn't match the root filesystem
	fstabContent := `# /etc/fstab
UUID=other-uuid / ext4 defaults 0 1`

	tmpDir := t.TempDir()
	snapshotDir := filepath.Join(tmpDir, "snapshot")
	etcDir := filepath.Join(snapshotDir, "etc")
	if err := os.MkdirAll(etcDir, 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	fstabPath := filepath.Join(etcDir, "fstab")
	if err := os.WriteFile(fstabPath, []byte(fstabContent), 0644); err != nil {
		t.Fatalf("Failed to create test fstab: %v", err)
	}

	snapshot.FilesystemPath = snapshotDir

	manager := NewManager()
	fileDiff, err := manager.UpdateSnapshotFstabDiff(snapshot, rootFS)

	if err != nil {
		t.Errorf("UpdateSnapshotFstabDiff() error = %v", err)
		return
	}

	if fileDiff != nil {
		t.Error("UpdateSnapshotFstabDiff() should return nil diff when no changes needed")
	}
}

func TestManager_UpdateSnapshotFstabDiff_NoFstab(t *testing.T) {
	snapshot := &btrfs.Snapshot{
		Subvolume: &btrfs.Subvolume{
			ID:   256,
			Path: "/@snapshots/1/snapshot",
		},
		FilesystemPath: "/non/existent/path",
	}

	rootFS := &btrfs.Filesystem{
		UUID: "test-uuid",
	}

	manager := NewManager()
	fileDiff, err := manager.UpdateSnapshotFstabDiff(snapshot, rootFS)

	if err != nil {
		t.Errorf("UpdateSnapshotFstabDiff() error = %v", err)
		return
	}

	if fileDiff != nil {
		t.Error("UpdateSnapshotFstabDiff() should return nil diff when fstab doesn't exist")
	}
}

func TestManager_isRootMount(t *testing.T) {
	rootFS := &btrfs.Filesystem{
		UUID:      "test-uuid",
		Device:    "/dev/sda2",
		Label:     "root",
		PartUUID:  "part-uuid",
		PartLabel: "root-part",
	}

	tests := []struct {
		name  string
		entry *Entry
		want  bool
	}{
		{
			name: "root mount with UUID",
			entry: &Entry{
				Device:     "UUID=test-uuid",
				Mountpoint: "/",
				FSType:     "btrfs",
			},
			want: true,
		},
		{
			name: "root mount with device path",
			entry: &Entry{
				Device:     "/dev/sda2",
				Mountpoint: "/",
				FSType:     "btrfs",
			},
			want: true,
		},
		{
			name: "root mount with LABEL",
			entry: &Entry{
				Device:     "LABEL=root",
				Mountpoint: "/",
				FSType:     "btrfs",
			},
			want: true,
		},
		{
			name: "non-root mountpoint",
			entry: &Entry{
				Device:     "UUID=test-uuid",
				Mountpoint: "/home",
				FSType:     "btrfs",
			},
			want: false,
		},
		{
			name: "non-btrfs filesystem",
			entry: &Entry{
				Device:     "UUID=test-uuid",
				Mountpoint: "/",
				FSType:     "ext4",
			},
			want: false,
		},
		{
			name: "non-matching device",
			entry: &Entry{
				Device:     "UUID=other-uuid",
				Mountpoint: "/",
				FSType:     "btrfs",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewManager()
			got := manager.isRootMount(tt.entry, rootFS)
			if got != tt.want {
				t.Errorf("isRootMount() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestManager_updateRootEntry(t *testing.T) {
	snapshot := &btrfs.Snapshot{
		Subvolume: &btrfs.Subvolume{
			ID:   256,
			Path: "/@snapshots/1/snapshot",
		},
	}

	rootFS := &btrfs.Filesystem{
		UUID: "test-uuid",
	}

	tests := []struct {
		name         string
		entry        *Entry
		wantModified bool
		wantOptions  string
	}{
		{
			name: "add subvol option",
			entry: &Entry{
				Options: "defaults",
			},
			wantModified: true,
			wantOptions:  "defaults,subvol=/@snapshots/1/snapshot,subvolid=256",
		},
		{
			name: "update existing subvol",
			entry: &Entry{
				Options: "defaults,subvol=@",
			},
			wantModified: true,
			wantOptions:  "defaults,subvol=/@snapshots/1/snapshot,subvolid=256",
		},
		{
			name: "update both options",
			entry: &Entry{
				Options: "subvol=@,subvolid=5",
			},
			wantModified: true,
			wantOptions:  "subvol=/@snapshots/1/snapshot,subvolid=256",
		},
		{
			name: "no changes needed",
			entry: &Entry{
				Options: "subvol=/@snapshots/1/snapshot,subvolid=256",
			},
			wantModified: false,
			wantOptions:  "subvol=/@snapshots/1/snapshot,subvolid=256",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewManager()
			originalOptions := tt.entry.Options
			got := manager.updateRootEntry(tt.entry, snapshot, rootFS)

			if got != tt.wantModified {
				t.Errorf("updateRootEntry() = %v, want %v", got, tt.wantModified)
			}

			if tt.entry.Options != tt.wantOptions {
				t.Errorf("updateRootEntry() options = %v, want %v", tt.entry.Options, tt.wantOptions)
			}

			// Restore original for next test
			tt.entry.Options = originalOptions
		})
	}
}

func TestManager_updateSubvolOption(t *testing.T) {
	tests := []struct {
		name      string
		options   string
		newSubvol string
		want      string
	}{
		{
			name:      "add to empty options",
			options:   "",
			newSubvol: "/@snapshots/1/snapshot",
			want:      "subvol=/@snapshots/1/snapshot",
		},
		{
			name:      "add to existing options",
			options:   "defaults",
			newSubvol: "/@snapshots/1/snapshot",
			want:      "defaults,subvol=/@snapshots/1/snapshot",
		},
		{
			name:      "replace existing subvol",
			options:   "defaults,subvol=@,compress=zstd",
			newSubvol: "/@snapshots/1/snapshot",
			want:      "defaults,subvol=/@snapshots/1/snapshot,compress=zstd",
		},
		{
			name:      "replace only subvol",
			options:   "subvol=@",
			newSubvol: "/@snapshots/1/snapshot",
			want:      "subvol=/@snapshots/1/snapshot",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewManager()
			got := manager.updateSubvolOption(tt.options, tt.newSubvol)
			if got != tt.want {
				t.Errorf("updateSubvolOption() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestManager_updateSubvolidOption(t *testing.T) {
	tests := []struct {
		name        string
		options     string
		newSubvolid uint64
		want        string
	}{
		{
			name:        "add to empty options",
			options:     "",
			newSubvolid: 256,
			want:        "subvolid=256",
		},
		{
			name:        "add to existing options",
			options:     "defaults",
			newSubvolid: 256,
			want:        "defaults,subvolid=256",
		},
		{
			name:        "replace existing subvolid",
			options:     "defaults,subvolid=5,compress=zstd",
			newSubvolid: 256,
			want:        "defaults,subvolid=256,compress=zstd",
		},
		{
			name:        "replace only subvolid",
			options:     "subvolid=5",
			newSubvolid: 256,
			want:        "subvolid=256",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewManager()
			got := manager.updateSubvolidOption(tt.options, tt.newSubvolid)
			if got != tt.want {
				t.Errorf("updateSubvolidOption() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestManager_deviceMatches(t *testing.T) {
	rootFS := &btrfs.Filesystem{
		UUID:      "test-uuid",
		PartUUID:  "part-uuid",
		Label:     "root",
		PartLabel: "root-part",
		Device:    "/dev/sda2",
	}

	tests := []struct {
		name   string
		device string
		want   bool
	}{
		{
			name:   "UUID match",
			device: "UUID=test-uuid",
			want:   true,
		},
		{
			name:   "PARTUUID match",
			device: "PARTUUID=part-uuid",
			want:   true,
		},
		{
			name:   "LABEL match",
			device: "LABEL=root",
			want:   true,
		},
		{
			name:   "PARTLABEL match",
			device: "PARTLABEL=root-part",
			want:   true,
		},
		{
			name:   "device path match",
			device: "/dev/sda2",
			want:   true,
		},
		{
			name:   "UUID no match",
			device: "UUID=other-uuid",
			want:   false,
		},
		{
			name:   "device path no match",
			device: "/dev/sdb1",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewManager()
			got := manager.deviceMatches(tt.device, rootFS)
			if got != tt.want {
				t.Errorf("deviceMatches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestManager_generateFstabContentWithModifications(t *testing.T) {
	fstab := &Fstab{
		Lines: []string{
			"# Comment",
			"UUID=test / btrfs defaults 0 1",
			"UUID=other /home btrfs defaults 0 2",
			"",
			"# Another comment",
		},
		Entries: []*Entry{
			{
				Device:     "UUID=test",
				Mountpoint: "/",
				FSType:     "btrfs",
				Options:    "defaults,subvol=/@snapshots/1/snapshot",
				Dump:       "0",
				Pass:       "1",
				Original:   "UUID=test / btrfs defaults 0 1",
			},
			{
				Device:     "UUID=other",
				Mountpoint: "/home",
				FSType:     "btrfs",
				Options:    "defaults",
				Dump:       "0",
				Pass:       "2",
				Original:   "UUID=other /home btrfs defaults 0 2",
			},
		},
	}

	modifiedEntries := map[string]bool{
		"UUID=test / btrfs defaults 0 1": true,
	}

	manager := NewManager()
	content, err := manager.generateFstabContentWithModifications(fstab, modifiedEntries)

	if err != nil {
		t.Errorf("generateFstabContentWithModifications() error = %v", err)
		return
	}

	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")

	// Check that comments are preserved
	if lines[0] != "# Comment" {
		t.Error("generateFstabContentWithModifications() should preserve comments")
	}

	// Check that modified entry is updated
	if !strings.Contains(lines[1], "defaults,subvol=/@snapshots/1/snapshot") {
		t.Error("generateFstabContentWithModifications() should update modified entries")
	}

	// Check that unmodified entry is preserved
	if lines[2] != "UUID=other /home btrfs defaults 0 2" {
		t.Error("generateFstabContentWithModifications() should preserve unmodified entries")
	}
}

func TestManager_updateLineWithNewOptions(t *testing.T) {
	tests := []struct {
		name         string
		originalLine string
		newOptions   string
		want         string
	}{
		{
			name:         "basic update",
			originalLine: "UUID=test / btrfs defaults 0 1",
			newOptions:   "defaults,subvol=/@snapshots/1/snapshot",
			want:         "UUID=test / btrfs defaults,subvol=/@snapshots/1/snapshot 0 1",
		},
		{
			name:         "update with tabs",
			originalLine: "UUID=test\t/\tbtrfs\tdefaults\t0\t1",
			newOptions:   "defaults,subvol=/@snapshots/1/snapshot",
			want:         "UUID=test\t/\tbtrfs\tdefaults,subvol=/@snapshots/1/snapshot\t0\t1",
		},
		{
			name:         "invalid line format",
			originalLine: "incomplete",
			newOptions:   "defaults",
			want:         "incomplete",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewManager()
			got := manager.updateLineWithNewOptions(tt.originalLine, tt.newOptions)
			if got != tt.want {
				t.Errorf("updateLineWithNewOptions() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetFieldOrDefault(t *testing.T) {
	fields := []string{"a", "b", "c"}

	tests := []struct {
		name         string
		index        int
		defaultValue string
		want         string
	}{
		{
			name:         "valid index",
			index:        1,
			defaultValue: "default",
			want:         "b",
		},
		{
			name:         "out of range index",
			index:        5,
			defaultValue: "default",
			want:         "default",
		},
		{
			name:         "negative index",
			index:        -1,
			defaultValue: "default",
			want:         "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getFieldOrDefault(fields, tt.index, tt.defaultValue)
			if got != tt.want {
				t.Errorf("getFieldOrDefault() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestManager_UpdateSnapshotFstab_DryRun(t *testing.T) {
	// Create test data
	snapshot := &btrfs.Snapshot{
		Subvolume: &btrfs.Subvolume{
			ID:   256,
			Path: "/@snapshots/1/snapshot",
		},
		FilesystemPath: "/tmp/test-snapshot",
	}

	rootFS := &btrfs.Filesystem{
		UUID: "test-uuid",
	}

	// Create temporary fstab
	fstabContent := `UUID=test-uuid / btrfs subvol=@,defaults 0 1`
	tmpDir := t.TempDir()
	snapshotDir := filepath.Join(tmpDir, "snapshot")
	etcDir := filepath.Join(snapshotDir, "etc")
	if err := os.MkdirAll(etcDir, 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	fstabPath := filepath.Join(etcDir, "fstab")
	if err := os.WriteFile(fstabPath, []byte(fstabContent), 0644); err != nil {
		t.Fatalf("Failed to create test fstab: %v", err)
	}

	snapshot.FilesystemPath = snapshotDir

	// Test dry run
	r := runner.New(true) // dry run
	manager := NewManager()
	err := manager.UpdateSnapshotFstab(snapshot, rootFS, r)

	if err != nil {
		t.Errorf("UpdateSnapshotFstab() dry run error = %v", err)
	}

	// Verify file wasn't actually modified
	content, err := os.ReadFile(fstabPath)
	if err != nil {
		t.Fatalf("Failed to read fstab after dry run: %v", err)
	}

	if string(content) != fstabContent {
		t.Error("UpdateSnapshotFstab() dry run should not modify file")
	}
}

// Helper function to create temporary files for testing
func createTempFile(t *testing.T, content string) string {
	tmpFile, err := os.CreateTemp("", "fstab_test_*")
	if err != nil {
		t.Fatalf("Failed to create temporary file: %v", err)
	}

	if _, err := tmpFile.WriteString(content); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		t.Fatalf("Failed to write to temporary file: %v", err)
	}

	tmpFile.Close()
	return tmpFile.Name()
}

// Test real file operations with mock runner
type mockRunner struct {
	dryRun  bool
	written map[string][]byte
}

func newMockRunner(dryRun bool) *mockRunner {
	return &mockRunner{
		dryRun:  dryRun,
		written: make(map[string][]byte),
	}
}

func (r *mockRunner) IsDryRun() bool {
	return r.dryRun
}

func (r *mockRunner) WriteFile(path string, data []byte, perm os.FileMode, description string) error {
	if r.dryRun {
		return nil
	}
	r.written[path] = data
	return nil
}

func (r *mockRunner) Command(name string, args []string, description string) error {
	return nil
}

func (r *mockRunner) MkdirAll(path string, perm os.FileMode, description string) error {
	return nil
}

func TestManager_UpdateSnapshotFstab_WithMockRunner(t *testing.T) {
	snapshot := &btrfs.Snapshot{
		Subvolume: &btrfs.Subvolume{
			ID:   256,
			Path: "/@snapshots/1/snapshot",
		},
		FilesystemPath: "/tmp/test-snapshot",
		SnapshotTime:   time.Now(),
	}

	rootFS := &btrfs.Filesystem{
		UUID: "test-uuid",
	}

	// Create temporary fstab
	fstabContent := `# Test fstab
UUID=test-uuid / btrfs subvol=@,defaults 0 1
UUID=other /home btrfs defaults 0 2`

	tmpDir := t.TempDir()
	snapshotDir := filepath.Join(tmpDir, "snapshot")
	etcDir := filepath.Join(snapshotDir, "etc")
	if err := os.MkdirAll(etcDir, 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	fstabPath := filepath.Join(etcDir, "fstab")
	if err := os.WriteFile(fstabPath, []byte(fstabContent), 0644); err != nil {
		t.Fatalf("Failed to create test fstab: %v", err)
	}

	snapshot.FilesystemPath = snapshotDir

	// Test with mock runner
	r := newMockRunner(false)
	manager := NewManager()
	err := manager.UpdateSnapshotFstab(snapshot, rootFS, r)

	if err != nil {
		t.Errorf("UpdateSnapshotFstab() error = %v", err)
		return
	}

	// Verify the file was written through the runner
	writtenData, exists := r.written[fstabPath]
	if !exists {
		t.Error("UpdateSnapshotFstab() should have written fstab file through runner")
		return
	}

	writtenContent := string(writtenData)
	if !strings.Contains(writtenContent, "subvol=/@snapshots/1/snapshot") {
		t.Error("UpdateSnapshotFstab() should update subvol option")
	}

	if !strings.Contains(writtenContent, "subvolid=256") {
		t.Error("UpdateSnapshotFstab() should update subvolid option")
	}

	// Verify comments are preserved
	if !strings.Contains(writtenContent, "# Test fstab") {
		t.Error("UpdateSnapshotFstab() should preserve comments")
	}

	// Verify unrelated entries are preserved
	if !strings.Contains(writtenContent, "UUID=other /home btrfs defaults 0 2") {
		t.Error("UpdateSnapshotFstab() should preserve unrelated entries")
	}
}

func TestManager_AnalyzeBootMount(t *testing.T) {
	tests := []struct {
		name                string
		fstab               *Fstab
		rootFS              *btrfs.Filesystem
		wantHasSeparateBoot bool
		wantBootOnSameBtrfs bool
		wantEntryNil        bool
	}{
		{
			name:                "nil fstab returns btrfs mode",
			fstab:               nil,
			rootFS:              nil,
			wantHasSeparateBoot: false,
			wantBootOnSameBtrfs: true,
			wantEntryNil:        true,
		},
		{
			name: "no /boot entry - part of root filesystem",
			fstab: &Fstab{
				Entries: []*Entry{
					{Device: "UUID=aaaa-bbbb", Mountpoint: "/", FSType: "btrfs", Options: "subvol=@,defaults"},
					{Device: "UUID=cccc-dddd", Mountpoint: "/home", FSType: "btrfs", Options: "subvol=@home,defaults"},
				},
			},
			rootFS:              &btrfs.Filesystem{UUID: "aaaa-bbbb"},
			wantHasSeparateBoot: false,
			wantBootOnSameBtrfs: true,
			wantEntryNil:        true,
		},
		{
			name: "/boot on vfat (ESP mode)",
			fstab: &Fstab{
				Entries: []*Entry{
					{Device: "UUID=aaaa-bbbb", Mountpoint: "/", FSType: "btrfs", Options: "subvol=@,defaults"},
					{Device: "/dev/sda1", Mountpoint: "/boot", FSType: "vfat", Options: "defaults"},
				},
			},
			rootFS:              &btrfs.Filesystem{UUID: "aaaa-bbbb"},
			wantHasSeparateBoot: true,
			wantBootOnSameBtrfs: false,
			wantEntryNil:        false,
		},
		{
			name: "/boot on ext4 (ESP mode)",
			fstab: &Fstab{
				Entries: []*Entry{
					{Device: "UUID=aaaa-bbbb", Mountpoint: "/", FSType: "btrfs", Options: "subvol=@,defaults"},
					{Device: "PARTUUID=1234-5678", Mountpoint: "/boot", FSType: "ext4", Options: "defaults"},
				},
			},
			rootFS:              nil,
			wantHasSeparateBoot: true,
			wantBootOnSameBtrfs: false,
			wantEntryNil:        false,
		},
		{
			name: "/boot on same btrfs filesystem",
			fstab: &Fstab{
				Entries: []*Entry{
					{Device: "UUID=aaaa-bbbb", Mountpoint: "/", FSType: "btrfs", Options: "subvol=@,defaults"},
					{Device: "UUID=aaaa-bbbb", Mountpoint: "/boot", FSType: "btrfs", Options: "subvol=@boot,defaults"},
				},
			},
			rootFS:              &btrfs.Filesystem{UUID: "aaaa-bbbb"},
			wantHasSeparateBoot: true,
			wantBootOnSameBtrfs: true,
			wantEntryNil:        false,
		},
		{
			name: "/boot on different btrfs filesystem",
			fstab: &Fstab{
				Entries: []*Entry{
					{Device: "UUID=aaaa-bbbb", Mountpoint: "/", FSType: "btrfs", Options: "subvol=@,defaults"},
					{Device: "UUID=xxxx-yyyy", Mountpoint: "/boot", FSType: "btrfs", Options: "subvol=@boot,defaults"},
				},
			},
			rootFS:              &btrfs.Filesystem{UUID: "aaaa-bbbb"},
			wantHasSeparateBoot: true,
			wantBootOnSameBtrfs: false,
			wantEntryNil:        false,
		},
		{
			name: "/boot on btrfs with nil rootFS (conservative - assume different)",
			fstab: &Fstab{
				Entries: []*Entry{
					{Device: "UUID=aaaa-bbbb", Mountpoint: "/", FSType: "btrfs", Options: "subvol=@,defaults"},
					{Device: "UUID=aaaa-bbbb", Mountpoint: "/boot", FSType: "btrfs", Options: "subvol=@boot,defaults"},
				},
			},
			rootFS:              nil,
			wantHasSeparateBoot: true,
			wantBootOnSameBtrfs: false,
			wantEntryNil:        false,
		},
		{
			name: "/boot/efi on vfat does not affect /boot detection",
			fstab: &Fstab{
				Entries: []*Entry{
					{Device: "UUID=aaaa-bbbb", Mountpoint: "/", FSType: "btrfs", Options: "subvol=@,defaults"},
					{Device: "/dev/sda1", Mountpoint: "/boot/efi", FSType: "vfat", Options: "defaults"},
				},
			},
			rootFS:              &btrfs.Filesystem{UUID: "aaaa-bbbb"},
			wantHasSeparateBoot: false,
			wantBootOnSameBtrfs: true,
			wantEntryNil:        true,
		},
		{
			name: "empty fstab entries",
			fstab: &Fstab{
				Entries: []*Entry{},
			},
			rootFS:              nil,
			wantHasSeparateBoot: false,
			wantBootOnSameBtrfs: true,
			wantEntryNil:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewManager()
			got := manager.AnalyzeBootMount(tt.fstab, tt.rootFS)

			if got.HasSeparateBootMount != tt.wantHasSeparateBoot {
				t.Errorf("AnalyzeBootMount() HasSeparateBootMount = %v, want %v", got.HasSeparateBootMount, tt.wantHasSeparateBoot)
			}
			if got.BootOnSameBtrfs != tt.wantBootOnSameBtrfs {
				t.Errorf("AnalyzeBootMount() BootOnSameBtrfs = %v, want %v", got.BootOnSameBtrfs, tt.wantBootOnSameBtrfs)
			}
			if tt.wantEntryNil && got.Entry != nil {
				t.Errorf("AnalyzeBootMount() Entry should be nil, got %+v", got.Entry)
			}
			if !tt.wantEntryNil && got.Entry == nil {
				t.Error("AnalyzeBootMount() Entry should not be nil")
			}
		})
	}
}

func TestIsValidUUID(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"valid_lowercase", "12345678-1234-1234-1234-123456789abc", true},
		{"valid_uppercase", "12345678-1234-1234-1234-123456789ABC", true},
		{"valid_mixed_case", "aAbBcCdD-eEfF-0011-2233-445566778899", true},
		{"empty", "", false},
		{"too_short", "12345678-1234-1234-1234", false},
		{"too_long", "12345678-1234-1234-1234-123456789abcd", false},
		{"missing_hyphen_pos8", "123456781234-1234-1234-123456789abc", false},
		{"missing_hyphen_pos13", "12345678-12341234-1234-123456789abc", false},
		{"missing_hyphen_pos18", "12345678-1234-12341234-123456789abc", false},
		{"missing_hyphen_pos23", "12345678-1234-1234-1234123456789abc", false},
		{"non_hex_char_g", "g2345678-1234-1234-1234-123456789abc", false},
		{"non_hex_char_z", "1234567z-1234-1234-1234-123456789abc", false},
		{"spaces", "12345678 1234 1234 1234 123456789abc", false},
		{"all_zeros", "00000000-0000-0000-0000-000000000000", true},
		{"all_f", "ffffffff-ffff-ffff-ffff-ffffffffffff", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isValidUUID(tt.input); got != tt.expected {
				t.Errorf("isValidUUID(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}
