package kernel

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// Documented contract (from inspect_microcode.go):
//   - Intel data_code is 0xYYYYMMDD BCD.
//   - AMD data_code is 0xMMDDYYYY BCD (each digit pair is decimal).
//   - InspectMicrocode tries CPIO, then raw Intel, then raw AMD; returns
//     an error when none match (caller falls back to filename-only).
//   - MicrocodeLatestDate is the chronological max across blocks, formatted YYYY-MM-DD.
//   - MicrocodeProcessorSignatures lists per-block CPU identifiers.

// --- Helpers to synthesize containers ------------------------------------

// intelBlockBytes builds a 48-byte Intel update block header (no payload past header).
// data_code is BCD-encoded YYYYMMDD.
func intelBlockBytes(revision uint32, year, month, day uint32, signature uint32) []byte {
	hdr := make([]byte, intelHeaderLen)
	binary.LittleEndian.PutUint32(hdr[0:4], 1) // header_version
	binary.LittleEndian.PutUint32(hdr[4:8], revision)
	date := (toBCD2(year/100) << 24) | (toBCD2(year%100) << 16) | (toBCD2(month) << 8) | toBCD2(day)
	binary.LittleEndian.PutUint32(hdr[8:12], date)
	binary.LittleEndian.PutUint32(hdr[12:16], signature)
	// total_size = headerLen → walker moves exactly one header forward
	binary.LittleEndian.PutUint32(hdr[32:36], uint32(intelHeaderLen))
	return hdr
}

// amdContainerBytes builds a minimal AMD container with the given patches.
// data_code is BCD-encoded MMDDYYYY.
func amdContainerBytes(patches []amdTestPatch) []byte {
	var out []byte
	out = append(out, amdMagic...)                    // magic
	out = append(out, 0, 0, 0, 0)                      // equiv table type
	out = append(out, 0, 0, 0, 0)                      // equiv table length = 0 (no entries, fine for parser)
	for _, p := range patches {
		patchType := make([]byte, 4)
		binary.LittleEndian.PutUint32(patchType, 1)
		// Build a 32-byte patch header (matches struct microcode_header_amd
		// up to and including processor_rev_id).
		hdr := make([]byte, 32)
		date := (toBCD2(p.month) << 24) | (toBCD2(p.day) << 16) | (toBCD2(p.year/100) << 8) | toBCD2(p.year%100)
		binary.LittleEndian.PutUint32(hdr[0:4], date)
		binary.LittleEndian.PutUint32(hdr[4:8], p.patchID)
		binary.LittleEndian.PutUint16(hdr[24:26], p.processorRevID)
		patchSize := make([]byte, 4)
		binary.LittleEndian.PutUint32(patchSize, uint32(len(hdr)))
		out = append(out, patchType...)
		out = append(out, patchSize...)
		out = append(out, hdr...)
	}
	return out
}

type amdTestPatch struct {
	year, month, day uint32
	patchID          uint32
	processorRevID   uint16
}

// toBCD2 encodes a two-digit decimal number as one byte of BCD
// (e.g., 24 → 0x24). Stored as uint32 for shift-friendly use.
func toBCD2(n uint32) uint32 {
	return ((n / 10) << 4) | (n % 10)
}

// cpioNewcEntry builds a single CPIO newc entry for a file at `name` with `data`.
func cpioNewcEntry(name string, data []byte) []byte {
	nameWithNUL := append([]byte(name), 0)
	var out []byte
	out = append(out, []byte(cpioNewcMagic)...)
	// 13 hex-encoded 8-byte fields after the 6-byte magic = 104 bytes,
	// for a total header of 110 bytes. Per struct cpio_newc_header:
	//   field 0: c_ino       field 7: c_devmajor
	//   field 1: c_mode      field 8: c_devminor
	//   field 2: c_uid       field 9: c_rdevmajor
	//   field 3: c_gid       field 10: c_rdevminor
	//   field 4: c_nlink     field 11: c_namesize
	//   field 5: c_mtime     field 12: c_check
	//   field 6: c_filesize
	for i := 0; i < 13; i++ {
		switch i {
		case 6:
			out = append(out, hex8(uint32(len(data)))...)
		case 11:
			out = append(out, hex8(uint32(len(nameWithNUL)))...)
		default:
			out = append(out, []byte("00000000")...)
		}
	}
	out = append(out, nameWithNUL...)
	for len(out)%4 != 0 {
		out = append(out, 0)
	}
	out = append(out, data...)
	for len(out)%4 != 0 {
		out = append(out, 0)
	}
	return out
}

func cpioTrailer() []byte {
	return cpioNewcEntry("TRAILER!!!", nil)
}

func hex8(v uint32) []byte {
	const digits = "0123456789abcdef"
	out := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		out[i] = digits[v&0xF]
		v >>= 4
	}
	return out
}

// --- Tests ---------------------------------------------------------------

func TestInspectMicrocode_RawIntel(t *testing.T) {
	// Two Intel blocks: 2023-05-15 (rev 0x100) and 2024-09-13 (rev 0x101).
	// Latest must be 2024-09-13.
	blocks := []byte{}
	blocks = append(blocks, intelBlockBytes(0x100, 2023, 5, 15, 0x806EA)...)
	blocks = append(blocks, intelBlockBytes(0x101, 2024, 9, 13, 0x806EA)...)
	path := writeTempFile(t, blocks)

	meta, err := InspectMicrocode(path)
	if err != nil {
		t.Fatalf("InspectMicrocode: %v", err)
	}
	if meta.MicrocodeVendor != "Intel" {
		t.Errorf("Vendor = %q, want Intel", meta.MicrocodeVendor)
	}
	if meta.MicrocodeBlockCount != 2 {
		t.Errorf("BlockCount = %d, want 2", meta.MicrocodeBlockCount)
	}
	if meta.MicrocodeLatestDate != "2024-09-13" {
		t.Errorf("LatestDate = %q, want 2024-09-13", meta.MicrocodeLatestDate)
	}
	if len(meta.MicrocodeRevisions) != 2 || meta.MicrocodeRevisions[0] != 0x100 || meta.MicrocodeRevisions[1] != 0x101 {
		t.Errorf("Revisions = %v", meta.MicrocodeRevisions)
	}
}

func TestInspectMicrocode_RawAMD(t *testing.T) {
	// Three AMD patches, chronologically: 2011-10-24, 2008-04-30, 2013-01-21.
	// Latest (raw u32 max would lie: 0x10242011 > 0x01212013) — but chronological
	// max must be 2013-01-21.
	container := amdContainerBytes([]amdTestPatch{
		{year: 2011, month: 10, day: 24, patchID: 0x010000da, processorRevID: 0x1080},
		{year: 2008, month: 4, day: 30, patchID: 0x01000083, processorRevID: 0x1022},
		{year: 2013, month: 1, day: 21, patchID: 0x05000029, processorRevID: 0x5010},
	})
	path := writeTempFile(t, container)

	meta, err := InspectMicrocode(path)
	if err != nil {
		t.Fatalf("InspectMicrocode: %v", err)
	}
	if meta.MicrocodeVendor != "AMD" {
		t.Errorf("Vendor = %q, want AMD", meta.MicrocodeVendor)
	}
	if meta.MicrocodeBlockCount != 3 {
		t.Errorf("BlockCount = %d, want 3", meta.MicrocodeBlockCount)
	}
	if meta.MicrocodeLatestDate != "2013-01-21" {
		t.Errorf("LatestDate = %q, want 2013-01-21 (chronological max despite raw u32 ordering)", meta.MicrocodeLatestDate)
	}
	if len(meta.MicrocodeProcessorSignatures) != 3 {
		t.Fatalf("ProcessorSignatures len = %d, want 3", len(meta.MicrocodeProcessorSignatures))
	}
	// processor_rev_id values must be the ones we encoded (at offset 24, not 20).
	wantSigs := []uint32{0x1080, 0x1022, 0x5010}
	for i, want := range wantSigs {
		if meta.MicrocodeProcessorSignatures[i] != want {
			t.Errorf("ProcessorSignatures[%d] = 0x%04x, want 0x%04x", i, meta.MicrocodeProcessorSignatures[i], want)
		}
	}
}

func TestInspectMicrocode_CPIOWrappedIntel(t *testing.T) {
	intelBin := intelBlockBytes(0x42, 2024, 3, 15, 0x806EA)
	var cpio []byte
	cpio = append(cpio, cpioNewcEntry("kernel/x86/microcode/GenuineIntel.bin", intelBin)...)
	cpio = append(cpio, cpioTrailer()...)
	path := writeTempFile(t, cpio)

	meta, err := InspectMicrocode(path)
	if err != nil {
		t.Fatalf("InspectMicrocode: %v", err)
	}
	if meta.MicrocodeVendor != "Intel" {
		t.Errorf("Vendor = %q", meta.MicrocodeVendor)
	}
	if meta.MicrocodeLatestDate != "2024-03-15" {
		t.Errorf("LatestDate = %q, want 2024-03-15", meta.MicrocodeLatestDate)
	}
}

func TestInspectMicrocode_CPIOWrappedAMD(t *testing.T) {
	amdBin := amdContainerBytes([]amdTestPatch{
		{year: 2024, month: 6, day: 1, patchID: 0xABC, processorRevID: 0xA20F},
	})
	var cpio []byte
	cpio = append(cpio, cpioNewcEntry("kernel/x86/microcode/AuthenticAMD.bin", amdBin)...)
	cpio = append(cpio, cpioTrailer()...)
	path := writeTempFile(t, cpio)

	meta, err := InspectMicrocode(path)
	if err != nil {
		t.Fatalf("InspectMicrocode: %v", err)
	}
	if meta.MicrocodeVendor != "AMD" {
		t.Errorf("Vendor = %q", meta.MicrocodeVendor)
	}
	if meta.MicrocodeLatestDate != "2024-06-01" {
		t.Errorf("LatestDate = %q, want 2024-06-01", meta.MicrocodeLatestDate)
	}
	if len(meta.MicrocodeProcessorSignatures) != 1 || meta.MicrocodeProcessorSignatures[0] != 0xA20F {
		t.Errorf("ProcessorSignatures = %v, want [0xA20F]", meta.MicrocodeProcessorSignatures)
	}
}

func TestInspectMicrocode_GarbageReturnsError(t *testing.T) {
	path := writeTempFile(t, []byte("not a microcode container at all"))
	_, err := InspectMicrocode(path)
	if err == nil {
		t.Fatal("expected error for garbage file")
	}
}

func TestInspectMicrocode_TooSmall(t *testing.T) {
	path := writeTempFile(t, []byte{1, 2})
	_, err := InspectMicrocode(path)
	if err == nil {
		t.Fatal("expected error for tiny file")
	}
}

func TestInspectMicrocode_Nonexistent(t *testing.T) {
	_, err := InspectMicrocode("/nonexistent/microcode.img")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

// --- Date / BCD helpers ---

func TestFormatIntelDate(t *testing.T) {
	// 0xYYYYMMDD format
	cases := []struct {
		in   uint32
		want string
	}{
		{0x20240913, "2024-09-13"},
		{0x19990101, "1999-01-01"},
		{0x00000000, ""},
		{0x20240000, ""}, // month=0
		{0x20241300, ""}, // month=13
		{0x20240932, ""}, // day=32
		{0x18890101, ""}, // year < 1990
	}
	for _, c := range cases {
		got := formatIntelDate(c.in)
		if got != c.want {
			t.Errorf("formatIntelDate(0x%08x) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatAMDDate(t *testing.T) {
	// 0xMMDDYYYY format
	cases := []struct {
		in   uint32
		want string
	}{
		{0x04302008, "2008-04-30"},
		{0x01212013, "2013-01-21"},
		{0x10242011, "2011-10-24"},
		{0x00000000, ""},
		{0x13012024, ""}, // month=13
		{0x01322024, ""}, // day=32
	}
	for _, c := range cases {
		got := formatAMDDate(c.in)
		if got != c.want {
			t.Errorf("formatAMDDate(0x%08x) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBCDToDecimal(t *testing.T) {
	cases := []struct {
		in   uint32
		want uint32
	}{
		{0x2024, 2024},
		{0x99, 99},
		{0x12, 12},
		{0x0, 0},
		{0xA, 0}, // non-BCD nibble → 0
		{0x1A, 0},
	}
	for _, c := range cases {
		got := bcdToDecimal(c.in)
		if got != c.want {
			t.Errorf("bcdToDecimal(0x%x) = %d, want %d", c.in, got, c.want)
		}
	}
}

// --- Test helper ---

func writeTempFile(t *testing.T, data []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ucode.img")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
