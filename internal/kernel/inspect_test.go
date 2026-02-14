package kernel

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createFakeKernel builds a minimal bzImage with a valid header and version string.
func createFakeKernel(t *testing.T, dir string, version string, protocol uint16) string {
	t.Helper()
	path := filepath.Join(dir, "vmlinuz-test")

	// We need at least 0x210 bytes for the header, plus space for the version string.
	// Version string goes at offset 0x200 + versionPtr.
	// Place the version string starting at offset 0x300 (so versionPtr = 0x100).
	versionPtr := uint16(0x100) // string at 0x200 + 0x100 = 0x300
	fileSize := 0x300 + len(version) + 1

	buf := make([]byte, fileSize)

	// Write HdrS magic at offset 0x202
	binary.LittleEndian.PutUint32(buf[0x202:], hdrSMagic)

	// Write boot protocol version at offset 0x206
	binary.LittleEndian.PutUint16(buf[0x206:], protocol)

	// Write kernel_version pointer at offset 0x20E
	binary.LittleEndian.PutUint16(buf[0x20E:], versionPtr)

	// Write the version string at offset 0x300
	copy(buf[0x300:], version)
	buf[0x300+len(version)] = 0 // null terminator

	require.NoError(t, os.WriteFile(path, buf, 0644))
	return path
}

func TestInspectKernel_ValidBzImage(t *testing.T) {
	dir := t.TempDir()
	path := createFakeKernel(t, dir, "6.19.0-2-cachyos (user@host) #1 SMP", 0x020F)

	meta, err := InspectKernel(path)
	require.NoError(t, err)
	require.NotNil(t, meta)

	assert.Equal(t, "6.19.0-2-cachyos", meta.Version)
	assert.Equal(t, "6.19.0-2-cachyos (user@host) #1 SMP", meta.VersionFull)
	assert.Equal(t, "2.15", meta.BootProtocol)
}

func TestInspectKernel_VersionParsing(t *testing.T) {
	tests := []struct {
		name        string
		fullVersion string
		shortVer    string
	}{
		{"arch", "6.12.10-arch1-1 (linux@archlinux) #1 SMP PREEMPT", "6.12.10-arch1-1"},
		{"cachyos", "6.19.0-2-cachyos (linux-cachyos@cachyos) #1 SMP", "6.19.0-2-cachyos"},
		{"debian", "6.1.0-21-amd64 (debian-kernel@lists.debian.org) #1 SMP", "6.1.0-21-amd64"},
		{"no_space", "5.15.0", "5.15.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := createFakeKernel(t, dir, tt.fullVersion, 0x020F)

			meta, err := InspectKernel(path)
			require.NoError(t, err)
			assert.Equal(t, tt.shortVer, meta.Version)
			assert.Equal(t, tt.fullVersion, meta.VersionFull)
		})
	}
}

func TestInspectKernel_ProtocolVersions(t *testing.T) {
	tests := []struct {
		proto    uint16
		expected string
	}{
		{0x020F, "2.15"},
		{0x0200, "2.00"},
		{0x0204, "2.04"},
		{0x020C, "2.12"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			dir := t.TempDir()
			path := createFakeKernel(t, dir, "6.0.0", tt.proto)

			meta, err := InspectKernel(path)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, meta.BootProtocol)
		})
	}
}

func TestInspectKernel_NoHdrSMagic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not-a-kernel")
	// Write enough zeros but no HdrS magic
	require.NoError(t, os.WriteFile(path, make([]byte, 0x300), 0644))

	meta, err := InspectKernel(path)
	assert.Error(t, err)
	assert.Nil(t, meta)
	assert.Contains(t, err.Error(), "not a Linux bzImage")
}

func TestInspectKernel_VersionOffsetZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kernel-no-version")

	buf := make([]byte, 0x300)
	binary.LittleEndian.PutUint32(buf[0x202:], hdrSMagic)
	binary.LittleEndian.PutUint16(buf[0x206:], 0x020F)
	// kernel_version pointer = 0 (no version string)
	binary.LittleEndian.PutUint16(buf[0x20E:], 0)

	require.NoError(t, os.WriteFile(path, buf, 0644))

	meta, err := InspectKernel(path)
	require.NoError(t, err)
	require.NotNil(t, meta)
	assert.Equal(t, "", meta.Version)
	assert.Equal(t, "2.15", meta.BootProtocol)
}

func TestInspectKernel_TruncatedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tiny-file")
	require.NoError(t, os.WriteFile(path, make([]byte, 0x100), 0644))

	meta, err := InspectKernel(path)
	assert.Error(t, err)
	assert.Nil(t, meta)
	assert.Contains(t, err.Error(), "too small")
}

func TestInspectKernel_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty")
	require.NoError(t, os.WriteFile(path, []byte{}, 0644))

	meta, err := InspectKernel(path)
	assert.Error(t, err)
	assert.Nil(t, meta)
}

func TestInspectKernel_NonexistentFile(t *testing.T) {
	meta, err := InspectKernel("/nonexistent/vmlinuz")
	assert.Error(t, err)
	assert.Nil(t, meta)
}

// --- InspectInitramfs tests ---

func createInitramfsWithMagic(t *testing.T, dir, name string, magic []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	// Write magic bytes followed by some padding
	data := make([]byte, 1024)
	copy(data, magic)
	require.NoError(t, os.WriteFile(path, data, 0644))
	return path
}

func TestInspectInitramfs_Gzip(t *testing.T) {
	dir := t.TempDir()
	path := createInitramfsWithMagic(t, dir, "initramfs.img", []byte{0x1f, 0x8b})

	meta, err := InspectInitramfs(path)
	require.NoError(t, err)
	assert.Equal(t, "gzip", meta.CompressFormat)
}

func TestInspectInitramfs_Zstd(t *testing.T) {
	dir := t.TempDir()
	path := createInitramfsWithMagic(t, dir, "initramfs.img", []byte{0x28, 0xb5, 0x2f, 0xfd})

	meta, err := InspectInitramfs(path)
	require.NoError(t, err)
	assert.Equal(t, "zstd", meta.CompressFormat)
}

func TestInspectInitramfs_Xz(t *testing.T) {
	dir := t.TempDir()
	path := createInitramfsWithMagic(t, dir, "initramfs.img", []byte{0xfd, 0x37, 0x7a, 0x58, 0x5a, 0x00})

	meta, err := InspectInitramfs(path)
	require.NoError(t, err)
	assert.Equal(t, "xz", meta.CompressFormat)
}

func TestInspectInitramfs_Lz4(t *testing.T) {
	dir := t.TempDir()
	path := createInitramfsWithMagic(t, dir, "initramfs.img", []byte{0x02, 0x21, 0x4c, 0x18})

	meta, err := InspectInitramfs(path)
	require.NoError(t, err)
	assert.Equal(t, "lz4", meta.CompressFormat)
}

func TestInspectInitramfs_Bzip2(t *testing.T) {
	dir := t.TempDir()
	path := createInitramfsWithMagic(t, dir, "initramfs.img", []byte{0x42, 0x5a})

	meta, err := InspectInitramfs(path)
	require.NoError(t, err)
	assert.Equal(t, "bzip2", meta.CompressFormat)
}

func TestInspectInitramfs_CpioWithCompressedPayload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "initramfs.img")

	// Simulate: CPIO header followed by gzip payload at offset 100
	data := make([]byte, 4096)
	copy(data[0:], "070701") // ASCII CPIO magic
	// Place gzip magic at offset 100
	data[100] = 0x1f
	data[101] = 0x8b

	require.NoError(t, os.WriteFile(path, data, 0644))

	meta, err := InspectInitramfs(path)
	require.NoError(t, err)
	assert.Equal(t, "gzip", meta.CompressFormat)
}

func TestInspectInitramfs_CpioOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "initramfs.img")

	// CPIO header with no compressed payload
	data := make([]byte, 4096)
	copy(data[0:], "070701")

	require.NoError(t, os.WriteFile(path, data, 0644))

	meta, err := InspectInitramfs(path)
	require.NoError(t, err)
	assert.Equal(t, "cpio", meta.CompressFormat)
}

func TestInspectInitramfs_UnknownFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "initramfs.img")

	// Random bytes that don't match any magic
	data := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x00, 0x00, 0x00}
	data = append(data, make([]byte, 1000)...) // pad for minimum size

	require.NoError(t, os.WriteFile(path, data, 0644))

	meta, err := InspectInitramfs(path)
	require.NoError(t, err)
	assert.Equal(t, "unknown", meta.CompressFormat)
}

func TestInspectInitramfs_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.img")
	require.NoError(t, os.WriteFile(path, []byte{}, 0644))

	meta, err := InspectInitramfs(path)
	assert.Error(t, err)
	assert.Nil(t, meta)
	assert.Contains(t, err.Error(), "too small")
}

func TestInspectInitramfs_TooSmall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tiny.img")
	require.NoError(t, os.WriteFile(path, []byte{0x1f}, 0644))

	meta, err := InspectInitramfs(path)
	assert.Error(t, err)
	assert.Nil(t, meta)
}

func TestInspectInitramfs_NonexistentFile(t *testing.T) {
	meta, err := InspectInitramfs("/nonexistent/initramfs.img")
	assert.Error(t, err)
	assert.Nil(t, meta)
}
