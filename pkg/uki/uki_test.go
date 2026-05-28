package uki

import (
	"bytes"
	"encoding/binary"
	"errors"
	"path/filepath"
	"testing"
)

func TestParse_RejectsNonPE(t *testing.T) {
	_, err := Parse(bytes.NewReader([]byte("definitely not a PE binary")))
	if err == nil {
		t.Fatal("expected error for non-PE input, got nil")
	}
}

func TestParse_RejectsPEWithoutLinuxSection(t *testing.T) {
	// A vmlinuz with EFI stub is a PE but has no .linux section — must reject
	// so callers don't mistake it for a UKI.
	pe := buildPE(t, []peSection{
		{name: ".text", data: []byte("kernel code goes here")},
		{name: ".data", data: []byte("data")},
	})
	_, err := Parse(bytes.NewReader(pe))
	if !errors.Is(err, ErrNotUKI) {
		t.Fatalf("err = %v, want ErrNotUKI", err)
	}
}

func TestParse_ReturnsAllSectionsInOrder(t *testing.T) {
	pe := buildPE(t, []peSection{
		{name: ".linux", data: []byte("kernel")},
		{name: ".cmdline", data: []byte("root=UUID=x rw")},
		{name: ".uname", data: []byte("6.19.0")},
	})
	img, err := Parse(bytes.NewReader(pe))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	secs := img.Sections()
	if len(secs) != 3 {
		t.Fatalf("len(Sections) = %d, want 3", len(secs))
	}
	wantNames := []string{".linux", ".cmdline", ".uname"}
	for i, want := range wantNames {
		if secs[i].Name != want {
			t.Errorf("Sections()[%d].Name = %q, want %q", i, secs[i].Name, want)
		}
	}
}

func TestParse_PreservesSectionDataBytes(t *testing.T) {
	want := []byte("root=UUID=x rw quiet")
	pe := buildPE(t, []peSection{
		{name: ".linux", data: []byte("k")},
		{name: ".cmdline", data: want},
	})
	img, err := Parse(bytes.NewReader(pe))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := img.Section(".cmdline")
	if got == nil {
		t.Fatal("Section(.cmdline) = nil")
	}
	if !bytes.Equal(got.Data, want) {
		t.Errorf(".cmdline data = %q, want %q", got.Data, want)
	}
}

func TestSection_ReturnsNilForMissing(t *testing.T) {
	pe := buildPE(t, []peSection{{name: ".linux", data: []byte("k")}})
	img, err := Parse(bytes.NewReader(pe))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := img.Section(".missing"); got != nil {
		t.Errorf("Section(.missing) = %+v, want nil", got)
	}
}

func TestSection_FirstMatchOnly_ForRepeatedNames(t *testing.T) {
	// Multi-profile UKIs repeat .cmdline. Section() must return the first
	// (base) one; callers needing the rest iterate Sections().
	pe := buildPE(t, []peSection{
		{name: ".linux", data: []byte("k")},
		{name: ".cmdline", data: []byte("base")},
		{name: ".profile", data: []byte("ID=a\n")},
		{name: ".cmdline", data: []byte("override")},
	})
	img, err := Parse(bytes.NewReader(pe))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := img.Section(".cmdline"); got == nil || string(got.Data) != "base" {
		t.Errorf("Section(.cmdline) = %v, want first (base) match", got)
	}
	var cmdlines int
	for _, s := range img.Sections() {
		if s.Name == ".cmdline" {
			cmdlines++
		}
	}
	if cmdlines != 2 {
		t.Errorf("Sections() yielded %d .cmdline entries, want 2", cmdlines)
	}
}

func TestParseFile_RealFixture(t *testing.T) {
	// Integration check against a real ukify-built UKI. Lives under the
	// kernel package's testdata for now; will move when inspect_uki is
	// consolidated onto pkg/uki.
	path := filepath.Join("..", "..", "internal", "kernel", "testdata", "uki-single-profile.efi")
	img, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile(%s): %v", path, err)
	}
	for _, name := range []string{SectionLinux, SectionCmdline, SectionUname, SectionOSRel} {
		if img.Section(name) == nil {
			t.Errorf("real fixture missing required section %q", name)
		}
	}
}

// --- WriteTo round-trip ------------------------------------------------------

func TestWriteTo_RoundTripSynthesizedPE(t *testing.T) {
	orig := buildPE(t, []peSection{
		{name: ".linux", data: []byte("kernel-bytes")},
		{name: ".cmdline", data: []byte("root=UUID=x rw")},
		{name: ".uname", data: []byte("6.19.0")},
	})

	img, err := Parse(bytes.NewReader(orig))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	var buf bytes.Buffer
	n, err := img.WriteTo(&buf)
	if err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	if n != int64(buf.Len()) {
		t.Errorf("WriteTo returned %d, wrote %d", n, buf.Len())
	}

	img2, err := Parse(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Parse re-emitted: %v", err)
	}

	assertSectionsEqual(t, img.Sections(), img2.Sections())
}

func TestWriteTo_RoundTripRealFixture(t *testing.T) {
	path := filepath.Join("..", "..", "internal", "kernel", "testdata", "uki-single-profile.efi")
	img, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	var buf bytes.Buffer
	if _, err := img.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}

	img2, err := Parse(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Parse re-emitted real fixture: %v", err)
	}

	assertSectionsEqual(t, img.Sections(), img2.Sections())
}

func TestWriteTo_RoundTripMultiProfileFixture(t *testing.T) {
	// Multi-profile UKIs have repeated .cmdline / .profile sections.
	// Section ordering and per-section bytes must all round-trip.
	path := filepath.Join("..", "..", "internal", "kernel", "testdata", "uki-multi-profile.efi")
	img, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	var buf bytes.Buffer
	if _, err := img.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}

	img2, err := Parse(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Parse re-emitted multi-profile fixture: %v", err)
	}

	assertSectionsEqual(t, img.Sections(), img2.Sections())
}

func assertSectionsEqual(t *testing.T, want, got []Section) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("section count: want=%d got=%d", len(want), len(got))
	}
	for i := range want {
		if want[i].Name != got[i].Name {
			t.Errorf("Section[%d] name: want=%q got=%q", i, want[i].Name, got[i].Name)
		}
		if !bytes.Equal(want[i].Data, got[i].Data) {
			t.Errorf("Section[%d] (%s) data differs: want %d bytes, got %d bytes", i, want[i].Name, len(want[i].Data), len(got[i].Data))
		}
	}
}

// --- Minimal PE32+ synthesizer -----------------------------------------------
//
// Just enough PE32+ to satisfy debug/pe.NewFile: DOS header, "PE\0\0", COFF
// file header, PE32+ optional header (all zero except Magic and the rva-
// count consistency byte), then section headers and concatenated section
// data. No alignment, no relocations — debug/pe is permissive about both.
// Mirrors the helper in internal/kernel/inspect_uki_test.go; both will
// converge when that file is refactored onto pkg/uki.

type peSection struct {
	name string
	data []byte
}

func buildPE(t *testing.T, sections []peSection) []byte {
	t.Helper()
	const (
		dosHeaderLen     = 64
		peSigLen         = 4
		coffHeaderLen    = 20
		optHeaderLen     = 240
		sectionHeaderLen = 40
	)

	nSections := uint16(len(sections))
	headerLen := dosHeaderLen + peSigLen + coffHeaderLen + optHeaderLen + int(nSections)*sectionHeaderLen

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
	dos := make([]byte, dosHeaderLen)
	dos[0], dos[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(dos[0x3C:], uint32(dosHeaderLen))
	buf.Write(dos)
	buf.WriteString("PE\x00\x00")

	coff := make([]byte, coffHeaderLen)
	binary.LittleEndian.PutUint16(coff[0:2], 0x8664) // Machine: AMD64
	binary.LittleEndian.PutUint16(coff[2:4], nSections)
	binary.LittleEndian.PutUint16(coff[16:18], uint16(optHeaderLen))
	buf.Write(coff)

	opt := make([]byte, optHeaderLen)
	binary.LittleEndian.PutUint16(opt[0:2], 0x20B) // Magic: PE32+
	// debug/pe asserts NumberOfRvaAndSizes*8 == SizeOfOptionalHeader-112.
	binary.LittleEndian.PutUint32(opt[108:112], 16)
	buf.Write(opt)

	for i, s := range sections {
		hdr := make([]byte, sectionHeaderLen)
		copy(hdr[0:8], s.name)
		binary.LittleEndian.PutUint32(hdr[8:12], prep[i].size)    // VirtualSize
		binary.LittleEndian.PutUint32(hdr[16:20], prep[i].size)   // SizeOfRawData
		binary.LittleEndian.PutUint32(hdr[20:24], prep[i].offset) // PointerToRawData
		buf.Write(hdr)
	}

	for _, s := range sections {
		buf.Write(s.data)
	}

	return buf.Bytes()
}
