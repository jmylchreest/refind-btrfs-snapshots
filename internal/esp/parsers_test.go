package esp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMounts(t *testing.T) {
	t.Run("typical_mounts", func(t *testing.T) {
		input := `proc /proc proc rw,nosuid,nodev,noexec,relatime 0 0
sys /sys sysfs rw,nosuid,nodev,noexec,relatime 0 0
/dev/nvme0n1p1 /boot vfat rw,relatime,fmask=0022,dmask=0022,codepage=437,iocharset=ascii,shortname=mixed,utf8,errors=remount-ro 0 0
/dev/nvme0n1p2 / btrfs rw,relatime,ssd,space_cache=v2,subvolid=256,subvol=/@ 0 0
tmpfs /run tmpfs rw,nosuid,nodev,size=3289920k,nr_inodes=819200,mode=755 0 0
/dev/sda1 /mnt/extra ext4 rw,relatime 0 0
`
		mounts, err := parseMounts(strings.NewReader(input))
		require.NoError(t, err)
		require.Len(t, mounts, 3, "should include only /dev/ entries (3), skip pseudo-fs (proc, sys, tmpfs)")

		assert.Equal(t, "/dev/nvme0n1p1", mounts["nvme0n1p1"].Device)
		assert.Equal(t, "/boot", mounts["nvme0n1p1"].MountPoint)
		assert.Equal(t, "vfat", mounts["nvme0n1p1"].FSType)

		assert.Equal(t, "/", mounts["nvme0n1p2"].MountPoint)
		assert.Equal(t, "btrfs", mounts["nvme0n1p2"].FSType)

		assert.Equal(t, "/mnt/extra", mounts["sda1"].MountPoint)
	})

	t.Run("malformed_lines_skipped", func(t *testing.T) {
		input := `/dev/sda1
/dev/sda2 /short
/dev/sda3 /full vfat rw 0 0
not-a-device /mnt/fake ext4 rw 0 0
`
		mounts, err := parseMounts(strings.NewReader(input))
		require.NoError(t, err)
		require.Len(t, mounts, 1, "only the full valid line should produce a mount")
		assert.Equal(t, "/dev/sda3", mounts["sda3"].Device)
	})

	t.Run("empty_input", func(t *testing.T) {
		mounts, err := parseMounts(strings.NewReader(""))
		require.NoError(t, err)
		assert.Empty(t, mounts)
	})
}

func TestParsePartitions(t *testing.T) {
	t.Run("typical_partitions", func(t *testing.T) {
		input := `major minor  #blocks  name

   8        0  500107608 sda
   8        1     524288 sda1
   8        2  499582976 sda2
 259        0 1000204886 nvme0n1
 259        1     524288 nvme0n1p1
 259        2  999678976 nvme0n1p2
`
		parts, err := parsePartitions(strings.NewReader(input))
		require.NoError(t, err)
		require.Len(t, parts, 6)

		assert.Equal(t, "sda", parts[0].Name)
		assert.Equal(t, "500107608 blocks", parts[0].Size)
		assert.Equal(t, "nvme0n1p2", parts[5].Name)
		assert.Equal(t, "999678976 blocks", parts[5].Size)
	})

	t.Run("missing_header_yields_empty", func(t *testing.T) {
		// Without the "major" header line, the parser drains the whole input
		// looking for it and returns nothing — documenting this behavior so
		// any future change to be lenient is a deliberate choice.
		input := `   8        0  500107608 sda
`
		parts, err := parsePartitions(strings.NewReader(input))
		require.NoError(t, err)
		assert.Empty(t, parts)
	})

	t.Run("short_lines_skipped", func(t *testing.T) {
		input := `major minor  #blocks  name

   8        0
   8        1     524288 sda1
`
		parts, err := parsePartitions(strings.NewReader(input))
		require.NoError(t, err)
		require.Len(t, parts, 1)
		assert.Equal(t, "sda1", parts[0].Name)
	})
}

func TestIsESP(t *testing.T) {
	const efiGPTGUID = "c12a7328-f81f-11d2-ba4b-00a0c93ec93b"

	tests := []struct {
		name     string
		device   *BlockDevice
		expected bool
	}{
		{
			name:     "not_a_partition",
			device:   &BlockDevice{Type: "disk", FSTYPE: "vfat", Mountpoint: "/boot"},
			expected: false,
		},
		{
			name:     "gpt_efi_partition_guid_lowercase",
			device:   &BlockDevice{Type: "part", PARTTYPE: efiGPTGUID},
			expected: true,
		},
		{
			name:     "gpt_efi_partition_guid_uppercase",
			device:   &BlockDevice{Type: "part", PARTTYPE: strings.ToUpper(efiGPTGUID)},
			expected: true,
		},
		{
			name:     "mbr_efi_partition_with_0x_prefix",
			device:   &BlockDevice{Type: "part", PARTTYPE: "0xef"},
			expected: true,
		},
		{
			name:     "mbr_efi_partition_without_prefix",
			device:   &BlockDevice{Type: "part", PARTTYPE: "ef"},
			expected: true,
		},
		{
			name:     "vfat_on_boot_mount",
			device:   &BlockDevice{Type: "part", FSTYPE: "vfat", Mountpoint: "/boot"},
			expected: true,
		},
		{
			name:     "vfat_on_boot_efi",
			device:   &BlockDevice{Type: "part", FSTYPE: "vfat", Mountpoint: "/boot/efi"},
			expected: true,
		},
		{
			name:     "vfat_on_efi",
			device:   &BlockDevice{Type: "part", FSTYPE: "vfat", Mountpoint: "/efi"},
			expected: true,
		},
		{
			name:     "vfat_on_unrelated_mount",
			device:   &BlockDevice{Type: "part", FSTYPE: "vfat", Mountpoint: "/mnt/sdcard"},
			expected: false,
		},
		{
			name:     "non_vfat_partition",
			device:   &BlockDevice{Type: "part", FSTYPE: "ext4", Mountpoint: "/boot"},
			expected: false,
		},
		{
			name:     "unmounted_partition_no_hint",
			device:   &BlockDevice{Type: "part", FSTYPE: "vfat"},
			expected: false,
		},
	}

	d := NewESPDetector("")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, d.isESP(tt.device))
		})
	}
}

func TestIsESP_ForcedUUID(t *testing.T) {
	d := NewESPDetector("12AB-34CD")

	matching := &BlockDevice{Type: "part", UUID: "12AB-34CD", PARTTYPE: "anything"}
	assert.True(t, d.isESP(matching), "forced UUID matches → ESP")

	different := &BlockDevice{Type: "part", UUID: "different-uuid", PARTTYPE: "c12a7328-f81f-11d2-ba4b-00a0c93ec93b"}
	assert.False(t, d.isESP(different), "forced UUID set but doesn't match → not ESP even if PARTTYPE would otherwise qualify")
}
