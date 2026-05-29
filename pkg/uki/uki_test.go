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
	path := filepath.Join("testdata", "uki-single-profile.efi")
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
	path := filepath.Join("testdata", "uki-single-profile.efi")
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
	path := filepath.Join("testdata", "uki-multi-profile.efi")
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

// --- SetSection / RemoveSection mutation -------------------------------------

func TestSetSection_ReplacesExisting(t *testing.T) {
	pe := buildPE(t, []peSection{
		{name: ".linux", data: []byte("kernel")},
		{name: ".cmdline", data: []byte("root=UUID=x rw")},
	})
	img, err := Parse(bytes.NewReader(pe))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	want := []byte("root=UUID=x rw rootflags=subvol=/snap-100,subvolid=100")
	img.SetSection(".cmdline", want)

	if got := img.Section(".cmdline"); got == nil || !bytes.Equal(got.Data, want) {
		t.Fatalf("Section(.cmdline) = %v, want %q", got, want)
	}

	var buf bytes.Buffer
	if _, err := img.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	rt, err := Parse(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Parse re-emitted: %v", err)
	}
	if got := rt.Section(".cmdline"); got == nil || !bytes.Equal(got.Data, want) {
		t.Errorf("round-trip .cmdline = %v, want %q", got, want)
	}
	if got := rt.Section(".linux"); got == nil || !bytes.Equal(got.Data, []byte("kernel")) {
		t.Errorf("round-trip .linux altered: %v", got)
	}
}

func TestSetSection_PreservesCharacteristicsAndVA(t *testing.T) {
	// Replacing an existing section must keep its PE Characteristics and
	// VirtualAddress — otherwise the firmware would see a section change
	// flags or jump to a new RVA.
	pe := buildPE(t, []peSection{
		{name: ".linux", data: []byte("k")},
		{name: ".cmdline", data: []byte("old")},
	})
	img, err := Parse(bytes.NewReader(pe))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	orig := img.Section(".cmdline")
	origChar := orig.Characteristics
	origVA := orig.VirtualAddress

	img.SetSection(".cmdline", []byte("new content longer than old"))

	got := img.Section(".cmdline")
	if got.Characteristics != origChar {
		t.Errorf("Characteristics: orig=%#x got=%#x", origChar, got.Characteristics)
	}
	if got.VirtualAddress != origVA {
		t.Errorf("VirtualAddress: orig=%#x got=%#x", origVA, got.VirtualAddress)
	}
}

func TestSetSection_AppendsWhenAbsent(t *testing.T) {
	pe := buildPE(t, []peSection{
		{name: ".linux", data: []byte("k")},
	})
	img, err := Parse(bytes.NewReader(pe))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	img.SetSection(".sbat", []byte("sbat,1,SBAT Version,sbat,1,https://x"))

	if len(img.Sections()) != 2 {
		t.Errorf("section count after append = %d, want 2", len(img.Sections()))
	}
	var buf bytes.Buffer
	if _, err := img.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	rt, err := Parse(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Parse re-emitted: %v", err)
	}
	if got := rt.Section(".sbat"); got == nil {
		t.Fatal("appended .sbat missing after round-trip")
	}
}

func TestRemoveSection_DropsAndReportsFound(t *testing.T) {
	pe := buildPE(t, []peSection{
		{name: ".linux", data: []byte("k")},
		{name: ".cmdline", data: []byte("c")},
		{name: ".osrel", data: []byte("ID=arch")},
	})
	img, err := Parse(bytes.NewReader(pe))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if !img.RemoveSection(".cmdline") {
		t.Error("RemoveSection(.cmdline) = false, want true")
	}
	if img.Section(".cmdline") != nil {
		t.Error("Section(.cmdline) not nil after Remove")
	}
	if len(img.Sections()) != 2 {
		t.Errorf("section count = %d, want 2", len(img.Sections()))
	}

	var buf bytes.Buffer
	if _, err := img.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	rt, err := Parse(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Parse re-emitted: %v", err)
	}
	if rt.Section(".cmdline") != nil {
		t.Error(".cmdline survived round-trip after Remove")
	}
	if rt.Section(".linux") == nil || rt.Section(".osrel") == nil {
		t.Error("other sections missing after round-trip")
	}
}

func TestRemoveSection_ReturnsFalseForMissing(t *testing.T) {
	pe := buildPE(t, []peSection{{name: ".linux", data: []byte("k")}})
	img, err := Parse(bytes.NewReader(pe))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if img.RemoveSection(".nope") {
		t.Error("RemoveSection(.nope) = true, want false")
	}
}

func TestParse_RejectsPE32(t *testing.T) {
	// The UAPI UKI spec mandates PE32+. Manufacture a PE32 image by
	// flipping the OptionalHeader Magic byte on an otherwise-valid
	// PE32+ blob and assert the strict gate trips.
	pe := buildPE(t, []peSection{{name: ".linux", data: []byte("k")}})
	const dosHeaderLen, peSigLen, coffHeaderLen = 64, 4, 20
	optMagicOff := dosHeaderLen + peSigLen + coffHeaderLen
	binary.LittleEndian.PutUint16(pe[optMagicOff:optMagicOff+2], 0x10B) // PE32

	_, err := Parse(bytes.NewReader(pe))
	if !errors.Is(err, ErrNotPE32Plus) {
		t.Fatalf("err = %v, want ErrNotPE32Plus", err)
	}
}

func TestParseFile_OpenError(t *testing.T) {
	_, err := ParseFile(filepath.Join(t.TempDir(), "does-not-exist.efi"))
	if err == nil {
		t.Fatal("expected error for non-existent path, got nil")
	}
}

func TestUname_EmptyWhenMissing(t *testing.T) {
	pe := buildPE(t, []peSection{{name: ".linux", data: []byte("k")}})
	img, err := Parse(bytes.NewReader(pe))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := img.Uname(); got != "" {
		t.Errorf("Uname() = %q, want empty", got)
	}
}

func TestSBATPreservedThroughRoundTrip(t *testing.T) {
	// The real ukify-built fixture carries a .sbat section. Confirm
	// that mutating an unrelated section (.cmdline) leaves .sbat
	// byte-identical through Parse → SetSection → WriteTo → Parse.
	img, err := ParseFile(filepath.Join("testdata", "uki-single-profile.efi"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	origSBAT := img.Section(SectionSBAT)
	if origSBAT == nil {
		t.Skip("fixture has no .sbat — regenerate with sbat support")
	}
	origBytes := append([]byte(nil), origSBAT.Data...)

	img.SetCmdline("root=UUID=x rw rootflags=subvol=@/snap,subvolid=42")
	var buf bytes.Buffer
	if _, err := img.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	rt, err := Parse(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	rtSBAT := rt.Section(SectionSBAT)
	if rtSBAT == nil {
		t.Fatal("re-parsed image has no .sbat")
	}
	if !bytes.Equal(origBytes, rtSBAT.Data) {
		t.Errorf(".sbat mutated by .cmdline rewrite\norig %x\nrt   %x", origBytes, rtSBAT.Data)
	}
}

func TestSetSection_RewriteRealFixtureCmdline(t *testing.T) {
	// End-to-end UKI cloning shape: load a real ukify-built UKI, swap its
	// .cmdline for a snapshot-rooted one, write, reparse, verify the
	// rewritten cmdline reads back AND all other sections survive intact.
	path := filepath.Join("testdata", "uki-single-profile.efi")
	img, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	origSections := img.Sections()

	newCmdline := []byte("root=UUID=fixture-uuid rw rootflags=subvol=@/.snapshots/100/snapshot,subvolid=100")
	img.SetSection(".cmdline", newCmdline)

	var buf bytes.Buffer
	if _, err := img.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}

	rt, err := Parse(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Parse re-emitted: %v", err)
	}

	if got := rt.Section(".cmdline"); got == nil || !bytes.Equal(got.Data, newCmdline) {
		t.Errorf("round-trip .cmdline = %v, want %q", got, newCmdline)
	}

	if len(rt.Sections()) != len(origSections) {
		t.Fatalf("section count: orig=%d rt=%d", len(origSections), len(rt.Sections()))
	}
	for i, orig := range origSections {
		got := rt.Sections()[i]
		if orig.Name != got.Name {
			t.Errorf("Section[%d] name: orig=%q rt=%q", i, orig.Name, got.Name)
		}
		if orig.Name == ".cmdline" {
			continue // expected to differ
		}
		if !bytes.Equal(orig.Data, got.Data) {
			t.Errorf("Section[%d] (%s) data altered by .cmdline rewrite", i, orig.Name)
		}
	}
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
