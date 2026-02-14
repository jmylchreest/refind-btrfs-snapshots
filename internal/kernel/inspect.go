package kernel

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
)

// bzImage header constants from the Linux x86 boot protocol.
// See: https://www.kernel.org/doc/html/latest/arch/x86/boot.html
const (
	// hdrSMagicOffset is the offset where "HdrS" (0x53726448) should appear.
	hdrSMagicOffset = 0x202

	// bootProtocolVersionOffset is the offset of the 2-byte protocol version.
	bootProtocolVersionOffset = 0x206

	// kernelVersionOffset is the offset of the 2-byte pointer to the version string.
	// The actual string is at 0x200 + this value.
	kernelVersionOffset = 0x20E

	// hdrSMagic is the expected magic bytes "HdrS".
	hdrSMagic = 0x53726448

	// minHeaderSize is the minimum file size needed to read the boot protocol header.
	minHeaderSize = 0x210
)

// Initramfs compression magic bytes.
var compressionMagics = []struct {
	magic  []byte
	format string
}{
	// Order: longer magics first to avoid prefix ambiguity
	{[]byte{0xfd, 0x37, 0x7a, 0x58, 0x5a, 0x00}, "xz"},
	{[]byte{0x28, 0xb5, 0x2f, 0xfd}, "zstd"},
	{[]byte{0x02, 0x21, 0x4c, 0x18}, "lz4"},
	{[]byte{0x1f, 0x8b}, "gzip"},
	{[]byte{0x1f, 0x9e}, "gzip"},             // old gzip
	{[]byte{0x42, 0x5a}, "bzip2"},            // "BZ"
	{[]byte{0x5d, 0x00}, "lzma"},             // LZMA
	{[]byte{0x30, 0x37, 0x30, 0x37}, "cpio"}, // ASCII cpio (uncompressed, often microcode prefix)
}

// InspectKernel reads the bzImage header of a Linux kernel to extract version
// information. Returns nil and an error if the file is not a valid bzImage.
//
// This is pure Go with no external dependencies — it reads ~1KB of binary header.
func InspectKernel(path string) (*InspectedMetadata, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open kernel: %w", err)
	}
	defer f.Close()

	// Check file size
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat kernel: %w", err)
	}
	if info.Size() < minHeaderSize {
		return nil, fmt.Errorf("file too small (%d bytes) to contain a Linux boot header", info.Size())
	}

	// Read the HdrS magic at offset 0x202
	var magic uint32
	if _, err := f.Seek(hdrSMagicOffset, 0); err != nil {
		return nil, fmt.Errorf("seek to HdrS: %w", err)
	}
	if err := binary.Read(f, binary.LittleEndian, &magic); err != nil {
		return nil, fmt.Errorf("read HdrS magic: %w", err)
	}
	if magic != hdrSMagic {
		return nil, fmt.Errorf("not a Linux bzImage (expected HdrS magic 0x%08x at offset 0x202, got 0x%08x)", hdrSMagic, magic)
	}

	// Read boot protocol version at offset 0x206
	var protoVersion uint16
	if _, err := f.Seek(bootProtocolVersionOffset, 0); err != nil {
		return nil, fmt.Errorf("seek to protocol version: %w", err)
	}
	if err := binary.Read(f, binary.LittleEndian, &protoVersion); err != nil {
		return nil, fmt.Errorf("read protocol version: %w", err)
	}

	meta := &InspectedMetadata{
		BootProtocol: fmt.Sprintf("%d.%02d", protoVersion>>8, protoVersion&0xff),
	}

	// Read kernel_version pointer at offset 0x20E
	var versionPtr uint16
	if _, err := f.Seek(kernelVersionOffset, 0); err != nil {
		return nil, fmt.Errorf("seek to kernel_version pointer: %w", err)
	}
	if err := binary.Read(f, binary.LittleEndian, &versionPtr); err != nil {
		return nil, fmt.Errorf("read kernel_version pointer: %w", err)
	}

	// If the pointer is zero, version string is not available
	if versionPtr == 0 {
		return meta, nil
	}

	// Read the null-terminated version string at 0x200 + versionPtr
	stringOffset := int64(0x200) + int64(versionPtr)
	if stringOffset >= info.Size() {
		// Version pointer is out of bounds, but we still have protocol info
		return meta, nil
	}

	if _, err := f.Seek(stringOffset, 0); err != nil {
		return meta, nil
	}

	// Read up to 256 bytes for the version string (more than enough)
	buf := make([]byte, 256)
	n, err := f.Read(buf)
	if err != nil || n == 0 {
		return meta, nil
	}

	// Find null terminator
	fullVersion := buf[:n]
	if idx := bytes.IndexByte(fullVersion, 0); idx >= 0 {
		fullVersion = fullVersion[:idx]
	}

	meta.VersionFull = string(fullVersion)

	// Extract the short version: everything before the first space
	// e.g., "6.19.0-2-cachyos (user@host) #1 ..." → "6.19.0-2-cachyos"
	if spaceIdx := strings.IndexByte(meta.VersionFull, ' '); spaceIdx > 0 {
		meta.Version = meta.VersionFull[:spaceIdx]
	} else {
		meta.Version = meta.VersionFull
	}

	return meta, nil
}

// InspectInitramfs reads the first bytes of an initramfs image to detect
// its compression format. If the file starts with an uncompressed CPIO archive
// (common for early microcode loading), it scans ahead to find the actual
// compressed payload.
func InspectInitramfs(path string) (*InspectedMetadata, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open initramfs: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat initramfs: %w", err)
	}
	if info.Size() < 2 {
		return nil, fmt.Errorf("file too small (%d bytes) to identify compression format", info.Size())
	}

	// Read enough bytes to detect format.
	// If the file starts with a CPIO header, we need to scan further.
	scanSize := int64(65536) // 64KB should be enough to find the compressed payload
	if info.Size() < scanSize {
		scanSize = info.Size()
	}

	buf := make([]byte, scanSize)
	n, err := f.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("read initramfs header: %w", err)
	}
	buf = buf[:n]

	// Check the first bytes for direct compression format
	if format := matchCompression(buf); format != "" && format != "cpio" {
		return &InspectedMetadata{CompressFormat: format}, nil
	}

	// If it starts with CPIO (uncompressed), scan ahead for compressed payload.
	// This is the common case for initramfs with early microcode loading:
	// [uncompressed CPIO with microcode] [compressed main initramfs]
	if len(buf) >= 4 && string(buf[:4]) == "0707" {
		for offset := 1; offset < len(buf)-6; offset++ {
			if format := matchCompression(buf[offset:]); format != "" && format != "cpio" {
				return &InspectedMetadata{CompressFormat: format}, nil
			}
		}
		// Only found CPIO, no compressed payload in our scan window
		return &InspectedMetadata{CompressFormat: "cpio"}, nil
	}

	return &InspectedMetadata{CompressFormat: "unknown"}, nil
}

// matchCompression checks if the given bytes start with a known compression magic.
func matchCompression(data []byte) string {
	for _, cm := range compressionMagics {
		if bytes.HasPrefix(data, cm.magic) {
			return cm.format
		}
	}
	return ""
}
