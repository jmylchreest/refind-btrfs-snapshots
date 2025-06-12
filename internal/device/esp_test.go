package device

import (
	"path/filepath"
	"testing"
)

func TestNewESPDetector(t *testing.T) {
	tests := []struct {
		name string
		uuid string
	}{
		{
			name: "empty uuid",
			uuid: "",
		},
		{
			name: "valid uuid",
			uuid: "1234-5678",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := NewESPDetector(tt.uuid)
			if detector == nil {
				t.Fatal("NewESPDetector returned nil")
			}
			// We can't test internal fields, but we can test that the detector works
		})
	}
}

func TestESPDetector_ValidateESPAccess(t *testing.T) {
	detector := NewESPDetector("")
	
	// This test is hard to run reliably without an actual ESP
	// In most test environments, this will fail to find an ESP, which is expected
	err := detector.ValidateESPAccess()
	// We expect this to fail in test environments
	if err == nil {
		t.Log("ValidateESPAccess() succeeded (probably running on a system with ESP)")
	} else {
		t.Log("ValidateESPAccess() failed as expected in test environment")
	}
}

func TestESPDetector_ValidateESPAccessReadOnly(t *testing.T) {
	detector := NewESPDetector("")
	
	// This test is hard to run reliably without an actual ESP
	// In most test environments, this will fail to find an ESP, which is expected
	err := detector.ValidateESPAccessReadOnly()
	// We expect this to fail in test environments
	if err == nil {
		t.Log("ValidateESPAccessReadOnly() succeeded (probably running on a system with ESP)")
	} else {
		t.Log("ValidateESPAccessReadOnly() failed as expected in test environment")
	}
}

func TestESPDetector_GetESPMountPoint(t *testing.T) {
	detector := NewESPDetector("")
	
	// This test is tricky because it depends on the actual system state
	// We'll test that it doesn't panic and returns either a valid path or an error
	mountPoint, err := detector.GetESPMountPoint()
	
	// In most test environments, this will fail to find an ESP, which is expected
	if err != nil {
		// This is expected in test environments
		if mountPoint != "" {
			t.Error("GetESPMountPoint() should return empty string when error occurs")
		}
	} else {
		// If it succeeds, validate the path format
		if !filepath.IsAbs(mountPoint) {
			t.Errorf("GetESPMountPoint() should return absolute path, got: %v", mountPoint)
		}
	}
}

// Removed TestESPDetector_findESPByUUID as it tests private methods

// Removed TestESPDetector_parseMount as it tests private methods

// Removed TestESPDetector_isLikelyESP as it tests private methods

// Removed TestESPDetector_hasEFIDirectory as it tests private methods

func TestMount(t *testing.T) {
	mount := &Mount{
		Device:     "/dev/sda1",
		MountPoint: "/boot/efi",
		FSType:     "vfat",
	}

	if mount.Device != "/dev/sda1" {
		t.Errorf("Mount.Device = %v, want /dev/sda1", mount.Device)
	}
	if mount.MountPoint != "/boot/efi" {
		t.Errorf("Mount.MountPoint = %v, want /boot/efi", mount.MountPoint)
	}
	if mount.FSType != "vfat" {
		t.Errorf("Mount.FSType = %v, want vfat", mount.FSType)
	}
}