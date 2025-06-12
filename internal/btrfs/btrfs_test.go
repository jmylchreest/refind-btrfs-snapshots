package btrfs

import (
	"path/filepath"
	"testing"
	"time"
)

func TestNewManager(t *testing.T) {
	searchDirs := []string{"/.snapshots", "/snapshots"}
	maxDepth := 3

	manager := NewManager(searchDirs, maxDepth)

	if manager == nil {
		t.Fatal("NewManager returned nil")
	}

	if len(manager.searchDirs) != len(searchDirs) {
		t.Errorf("Expected %d search directories, got %d", len(searchDirs), len(manager.searchDirs))
	}

	for i, dir := range searchDirs {
		if manager.searchDirs[i] != dir {
			t.Errorf("Expected search dir %s, got %s", dir, manager.searchDirs[i])
		}
	}

	if manager.maxDepth != maxDepth {
		t.Errorf("Expected max depth %d, got %d", maxDepth, manager.maxDepth)
	}
}

func TestParseSubvolumeShow(t *testing.T) {
	manager := NewManager([]string{}, 0)

	testOutput := `@
UUID: 			5b8c8a5e-3f4d-4a8b-9c2d-1e6f7a8b9c0d
Parent UUID: 		-
Received UUID: 		-
Creation time: 		2023-10-15 14:30:22 +0000
Subvolume ID: 		256
Generation: 		1234
Gen at creation: 	1234
Parent ID: 		5
Top level ID: 		5
Path: 			@
Flags: 			-
Snapshot(s):`

	subvol, err := manager.parseSubvolumeShow(testOutput)
	if err != nil {
		t.Fatalf("parseSubvolumeShow failed: %v", err)
	}

	if subvol.ID != 256 {
		t.Errorf("Expected ID 256, got %d", subvol.ID)
	}

	if subvol.Path != "@" {
		t.Errorf("Expected path '@', got '%s'", subvol.Path)
	}

	if subvol.ParentID != 5 {
		t.Errorf("Expected parent ID 5, got %d", subvol.ParentID)
	}

	if subvol.Generation != 1234 {
		t.Errorf("Expected generation 1234, got %d", subvol.Generation)
	}

	if subvol.IsReadOnly {
		t.Error("Expected read-only to be false")
	}

	if subvol.IsSnapshot {
		t.Error("Expected is-snapshot to be false")
	}
}

func TestParseSubvolumeShowReadOnly(t *testing.T) {
	manager := NewManager([]string{}, 0)

	testOutput := `snapshot
UUID: 			5b8c8a5e-3f4d-4a8b-9c2d-1e6f7a8b9c0d
Parent UUID: 		-
Received UUID: 		-
Creation time: 		2023-10-15 14:30:22 +0000
Subvolume ID: 		512
Generation: 		5678
Gen at creation: 	5678
Parent ID: 		256
Top level ID: 		5
Path: 			snapshot
Flags: 			readonly,snapshot
Snapshot(s):`

	subvol, err := manager.parseSubvolumeShow(testOutput)
	if err != nil {
		t.Fatalf("parseSubvolumeShow failed: %v", err)
	}

	if !subvol.IsReadOnly {
		t.Error("Expected read-only to be true")
	}

	if !subvol.IsSnapshot {
		t.Error("Expected is-snapshot to be true")
	}
}

func TestGetSnapshotFstabPath(t *testing.T) {
	snapshot := &Snapshot{
		Subvolume: &Subvolume{
			Path: "@snapshots/test",
		},
		FilesystemPath: "/test/snapshot/path",
	}

	expected := filepath.Join("/test/snapshot/path", "etc", "fstab")
	result := GetSnapshotFstabPath(snapshot)

	if result != expected {
		t.Errorf("Expected fstab path %s, got %s", expected, result)
	}
}

func TestIsSnapshotOfRoot(t *testing.T) {
	manager := NewManager([]string{}, 0)

	// Create mock subvolumes
	rootSubvol := &Subvolume{
		ID:         256,
		Path:       "@",
		ParentID:   5,
		IsSnapshot: false,
	}

	// Valid snapshot - has root as parent
	snapshotSubvol := &Subvolume{
		ID:         512,
		Path:       "@snapshots/test",
		ParentID:   256,
		IsSnapshot: true,
	}

	// Invalid - not marked as snapshot
	nonSnapshotSubvol := &Subvolume{
		ID:         768,
		Path:       "@home",
		ParentID:   5,
		IsSnapshot: false,
	}

	// Invalid - wrong parent ID
	wrongParentSnapshot := &Subvolume{
		ID:         513,
		Path:       "@other/subvol",
		ParentID:   999,
		IsSnapshot: true,
	}

	// Test valid snapshot detection
	if !manager.isSnapshotOfRoot(snapshotSubvol, rootSubvol) {
		t.Error("Expected snapshot with correct parent ID to be detected as snapshot of root")
	}

	// Test non-snapshot rejection
	if manager.isSnapshotOfRoot(nonSnapshotSubvol, rootSubvol) {
		t.Error("Expected non-snapshot to not be detected as snapshot of root")
	}

	// Test wrong parent rejection
	if manager.isSnapshotOfRoot(wrongParentSnapshot, rootSubvol) {
		t.Error("Expected snapshot with wrong parent ID to not be detected as snapshot of root")
	}
}

func TestSnapshot(t *testing.T) {
	// Test Snapshot struct creation and basic properties
	now := time.Now()

	subvol := &Subvolume{
		ID:          512,
		Path:        "@snapshots/test",
		ParentID:    256,
		Generation:  1234,
		CreatedTime: now,
		IsSnapshot:  true,
		IsReadOnly:  true,
	}

	snapshot := &Snapshot{
		Subvolume:    subvol,
		OriginalPath: "@",
		SnapshotTime: now,
	}

	if snapshot.ID != 512 {
		t.Errorf("Expected snapshot ID 512, got %d", snapshot.ID)
	}

	if snapshot.Path != "@snapshots/test" {
		t.Errorf("Expected snapshot path '@snapshots/test', got '%s'", snapshot.Path)
	}

	if snapshot.OriginalPath != "@" {
		t.Errorf("Expected original path '@', got '%s'", snapshot.OriginalPath)
	}

	if !snapshot.IsSnapshot {
		t.Error("Expected snapshot to be marked as snapshot")
	}

	if !snapshot.IsReadOnly {
		t.Error("Expected snapshot to be read-only")
	}
}

func TestFilesystem(t *testing.T) {
	// Test Filesystem struct creation and basic properties
	subvol := &Subvolume{
		ID:   256,
		Path: "@",
	}

	fs := &Filesystem{
		UUID:       "test-uuid",
		Device:     "/dev/test",
		MountPoint: "/",
		Subvolume:  subvol,
		Snapshots:  []*Snapshot{},
	}

	if fs.UUID != "test-uuid" {
		t.Errorf("Expected UUID 'test-uuid', got '%s'", fs.UUID)
	}

	if fs.Device != "/dev/test" {
		t.Errorf("Expected device '/dev/test', got '%s'", fs.Device)
	}

	if fs.MountPoint != "/" {
		t.Errorf("Expected mount point '/', got '%s'", fs.MountPoint)
	}

	if fs.Subvolume.ID != 256 {
		t.Errorf("Expected subvolume ID 256, got %d", fs.Subvolume.ID)
	}

	if len(fs.Snapshots) != 0 {
		t.Errorf("Expected 0 snapshots, got %d", len(fs.Snapshots))
	}
}

// Benchmark tests
func BenchmarkParseSubvolumeShow(b *testing.B) {
	manager := NewManager([]string{}, 0)
	testOutput := `Name: 			@
UUID: 			5b8c8a5e-3f4d-4a8b-9c2d-1e6f7a8b9c0d
Parent UUID: 		-
Received UUID: 		-
Creation time: 		2023-10-15 14:30:22 +0000
Subvolume ID: 		256
Generation: 		1234
Gen at creation: 	1234
Parent ID: 		5
Top level ID: 		5
Flags: 			readonly,snapshot
Snapshot(s):`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := manager.parseSubvolumeShow(testOutput)
		if err != nil {
			b.Fatalf("parseSubvolumeShow failed: %v", err)
		}
	}
}
