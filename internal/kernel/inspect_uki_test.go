package kernel

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// Documented contract (from inspect_uki.go):
//   - A UKI is a PE32+ binary with a .linux section (per the systemd-stub spec).
//   - InspectUKI returns metadata with Format="uki" and fills Version from .uname,
//     Cmdline from .cmdline, OSReleaseID/OSReleasePrettyName from .osrel.
//   - A PE without .linux (e.g., an EFI-stub vmlinuz) must be rejected.

func TestInspectUKI_ExtractsAllFields(t *testing.T) {
	osrel := `ID=arch
PRETTY_NAME="Arch Linux"
VERSION_ID=rolling`
	pe := buildPE(t, []peSection{
		{name: ".linux", data: []byte("not really a kernel")},
		{name: ".uname", data: []byte("6.19.0-1-arch\x00")},
		{name: ".cmdline", data: []byte("root=UUID=x rw quiet\x00")},
		{name: ".osrel", data: []byte(osrel + "\x00")},
	})
	path := writeTempFile(t, pe)

	meta, err := InspectUKI(path)
	if err != nil {
		t.Fatalf("InspectUKI: %v", err)
	}
	if meta.Format != "uki" {
		t.Errorf("Format = %q, want uki", meta.Format)
	}
	if meta.Version != "6.19.0-1-arch" {
		t.Errorf("Version = %q", meta.Version)
	}
	if meta.Cmdline != "root=UUID=x rw quiet" {
		t.Errorf("Cmdline = %q", meta.Cmdline)
	}
	if meta.OSReleaseID != "arch" {
		t.Errorf("OSReleaseID = %q", meta.OSReleaseID)
	}
	if meta.OSReleasePrettyName != "Arch Linux" {
		t.Errorf("OSReleasePrettyName = %q", meta.OSReleasePrettyName)
	}
}

func TestInspectUKI_RejectsPEWithoutLinuxSection(t *testing.T) {
	// A vmlinuz with EFI stub is a PE but has no .linux section — must reject
	// so callers fall through to the bzImage parser.
	pe := buildPE(t, []peSection{
		{name: ".text", data: []byte("kernel code goes here")},
		{name: ".data", data: []byte("data section")},
	})
	path := writeTempFile(t, pe)

	_, err := InspectUKI(path)
	if err == nil {
		t.Fatal("expected error for PE without .linux section")
	}
}

func TestInspectUKI_RejectsNonPE(t *testing.T) {
	path := writeTempFile(t, []byte("not a PE file at all, just bytes"))
	_, err := InspectUKI(path)
	if err == nil {
		t.Fatal("expected error for non-PE file")
	}
}

func TestInspectUKI_HandlesMissingOptionalSections(t *testing.T) {
	// .linux only — .uname/.cmdline/.osrel are all optional.
	pe := buildPE(t, []peSection{
		{name: ".linux", data: []byte("kernel")},
	})
	path := writeTempFile(t, pe)

	meta, err := InspectUKI(path)
	if err != nil {
		t.Fatalf("InspectUKI: %v", err)
	}
	if meta.Format != "uki" {
		t.Errorf("Format = %q", meta.Format)
	}
	if meta.Version != "" || meta.Cmdline != "" || meta.OSReleaseID != "" {
		t.Errorf("expected optional fields empty: %+v", meta)
	}
}

func TestParseOSRelease(t *testing.T) {
	in := `# top comment
ID=arch
NAME="Arch Linux"
PRETTY_NAME='Arch Linux (stable)'
VERSION_ID=rolling
no-equals
`
	got := parseOSRelease(in)
	want := map[string]string{
		"ID":           "arch",
		"NAME":         "Arch Linux",
		"PRETTY_NAME":  "Arch Linux (stable)",
		"VERSION_ID":   "rolling",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("parseOSRelease[%q] = %q, want %q", k, got[k], v)
		}
	}
	if _, ok := got["no-equals"]; ok {
		t.Errorf("malformed line should not appear in result")
	}
}

// --- Minimal PE32+ synthesizer ------------------------------------------

type peSection struct {
	name string
	data []byte
}

// buildPE constructs a minimal PE32+ binary with the given named sections.
// Just enough to satisfy debug/pe.NewFile: a 64-byte DOS header, "PE\0\0",
// a 20-byte COFF file header, a 240-byte PE32+ optional header (all zero
// except Magic=0x20B), then 40-byte section headers and their data.
func buildPE(t *testing.T, sections []peSection) []byte {
	t.Helper()
	const (
		dosHeaderLen      = 64
		peSigLen          = 4
		coffHeaderLen     = 20
		optHeaderLen      = 240 // PE32+ standard
		sectionHeaderLen  = 40
	)

	nSections := uint16(len(sections))
	headerLen := dosHeaderLen + peSigLen + coffHeaderLen + optHeaderLen + int(nSections)*sectionHeaderLen

	// Pre-compute section data offsets (no alignment required for our purposes).
	type prepared struct {
		offset uint32
		size   uint32
	}
	prep := make([]prepared, len(sections))
	off := uint32(headerLen)
	for i, s := range sections {
		prep[i] = prepared{offset: off, size: uint32(len(s.data))}
		off += uint32(len(s.data))
	}

	var buf bytes.Buffer
	// DOS header: MZ + zeros + e_lfanew at offset 0x3C.
	dos := make([]byte, dosHeaderLen)
	dos[0], dos[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(dos[0x3C:], uint32(dosHeaderLen))
	buf.Write(dos)

	// PE\0\0
	buf.WriteString("PE\x00\x00")

	// COFF file header (20 bytes).
	coff := make([]byte, coffHeaderLen)
	binary.LittleEndian.PutUint16(coff[0:2], 0x8664) // Machine: AMD64
	binary.LittleEndian.PutUint16(coff[2:4], nSections)
	binary.LittleEndian.PutUint16(coff[16:18], uint16(optHeaderLen))
	buf.Write(coff)

	// Optional header (240 bytes). debug/pe validates that
	// NumberOfRvaAndSizes * 8 == SizeOfOptionalHeader - 112 for PE32+.
	// 240 - 112 = 128 bytes of data directories → 16 entries.
	opt := make([]byte, optHeaderLen)
	binary.LittleEndian.PutUint16(opt[0:2], 0x20B) // Magic: PE32+
	binary.LittleEndian.PutUint32(opt[108:112], 16) // NumberOfRvaAndSizes
	buf.Write(opt)

	// Section headers.
	for i, s := range sections {
		hdr := make([]byte, sectionHeaderLen)
		copy(hdr[0:8], s.name) // null-padded; debug/pe trims trailing NULs
		binary.LittleEndian.PutUint32(hdr[8:12], prep[i].size)   // VirtualSize
		binary.LittleEndian.PutUint32(hdr[16:20], prep[i].size)  // SizeOfRawData
		binary.LittleEndian.PutUint32(hdr[20:24], prep[i].offset) // PointerToRawData
		buf.Write(hdr)
	}

	// Section data.
	for _, s := range sections {
		buf.Write(s.data)
	}

	return buf.Bytes()
}

// TestInspectUKI_RealFixture cross-checks the synthesizer against a real
// ukify-built UKI. Set KERNEL_SPY_UKI to a UKI path (e.g. from
// contrib/make-test-fixtures.sh) to enable; the test is skipped otherwise
// so CI without ukify still passes.
func TestInspectUKI_RealFixture(t *testing.T) {
	path := os.Getenv("KERNEL_SPY_UKI")
	if path == "" {
		t.Skip("set KERNEL_SPY_UKI to a real ukify-built UKI to enable")
	}
	if _, err := os.Stat(path); err != nil {
		t.Skipf("KERNEL_SPY_UKI=%q not readable: %v", path, err)
	}
	meta, err := InspectUKI(path)
	if err != nil {
		t.Fatalf("InspectUKI on real UKI: %v", err)
	}
	if meta.Format != "uki" {
		t.Errorf("Format = %q, want uki", meta.Format)
	}
	// The fixture script always supplies these three, so they must be present
	// to confirm we parse a real UKI the same way as the synthetic one.
	if meta.Version == "" {
		t.Errorf("Version empty — expected .uname from real UKI")
	}
	if meta.Cmdline == "" {
		t.Errorf("Cmdline empty — expected .cmdline from real UKI")
	}
	if meta.OSReleasePrettyName == "" {
		t.Errorf("OSReleasePrettyName empty — expected .osrel from real UKI")
	}
	t.Logf("real UKI parsed: version=%q cmdline=%q os=%q",
		meta.Version, meta.Cmdline, meta.OSReleasePrettyName)
}

// Sanity check: the synthesizer produces something debug/pe.Open accepts.
func TestSynthesizedPEParses(t *testing.T) {
	pe := buildPE(t, []peSection{
		{name: ".linux", data: []byte("x")},
		{name: ".uname", data: []byte("1.2.3\x00")},
	})
	dir := t.TempDir()
	path := filepath.Join(dir, "synth.efi")
	if err := os.WriteFile(path, pe, 0o644); err != nil {
		t.Fatal(err)
	}
	// If buildPE is broken, InspectUKI will fail with a pe.Open error rather
	// than the "missing .linux section" message; this guards against silent
	// regression of the synthesizer itself.
	meta, err := InspectUKI(path)
	if err != nil {
		t.Fatalf("synth PE failed to parse: %v", err)
	}
	if meta.Version != "1.2.3" {
		t.Errorf("Version = %q, want 1.2.3", meta.Version)
	}
}
