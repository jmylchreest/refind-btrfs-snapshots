package diff

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFileType(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/mnt/snapshots/42/etc/fstab", "fstab"},
		{"/boot/efi/EFI/refind/refind.conf", "refind_config"},
		{"/boot/efi/EFI/refind/refind_linux.conf", "refind_linux"},
		{"/boot/efi/EFI/refind/refind-btrfs-snapshots.conf", "refind_include"},
		{"/boot/efi/EFI/BOOT/custom.conf", "refind_config"},
		{"/boot/efi/EFI/Linux/unified.efi", "unknown"},
		{"/some/random/path.txt", "unknown"},
		{"", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected+"_"+tt.path, func(t *testing.T) {
			assert.Equal(t, tt.expected, FileType(tt.path))
		})
	}
}
