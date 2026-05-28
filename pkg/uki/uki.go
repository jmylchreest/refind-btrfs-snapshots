// Package uki provides parsing, manipulation, and writing of Unified Kernel
// Images (UKIs) — PE32+ binaries with the section layout described by the
// UAPI Unified Kernel Image specification:
// https://uapi-group.org/specifications/specs/unified_kernel_image/
//
// A UKI is a single signed PE binary that bundles a kernel (.linux), an
// initramfs (.initrd), a kernel command line (.cmdline), os-release
// metadata (.osrel), the kernel version string (.uname), and optional
// extras like .sbat (Secure Boot revocation) and repeated .profile +
// .cmdline pairs (multi-profile UKIs). This package treats them as a
// general-purpose data structure: read sections, mutate them, write them
// back. It does not depend on systemd's ukify or any external binary.
package uki

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Standard UKI section names per the UAPI spec.
const (
	SectionLinux   = ".linux"
	SectionInitrd  = ".initrd"
	SectionCmdline = ".cmdline"
	SectionOSRel   = ".osrel"
	SectionUname   = ".uname"
	SectionSBAT    = ".sbat"
	SectionProfile = ".profile"
)

// ErrNotUKI is returned by Parse when the input is a valid PE32+ binary
// but has no .linux section (the UAPI-required marker that distinguishes
// a UKI from a plain EFI executable).
var ErrNotUKI = errors.New("uki: no .linux section (not a UKI)")

// PE32+ structural constants.
const (
	dosELfanewOffset = 0x3C // 4-byte offset to PE signature inside DOS header
	peSignatureLen   = 4    // "PE\0\0"
	coffHeaderLen    = 20
	sectionHeaderLen = 40

	// OptionalHeader field offsets (PE32+ layout); all relative to the
	// start of the OptionalHeader.
	optFieldSectionAlignment = 32
	optFieldFileAlignment    = 36
	optFieldSizeOfImage      = 56
	optFieldSizeOfHeaders    = 60

	// COFF field offsets relative to COFF header start.
	coffFieldNumberOfSections     = 2
	coffFieldSizeOfOptionalHeader = 16
)

// Image is a parsed UKI. Sections appear in PE file order; multi-profile
// UKIs may have repeated section names (e.g. one .cmdline per profile).
type Image struct {
	sections []Section

	// Original PE structural state, captured at Parse time so WriteTo can
	// re-emit a valid PE32+ without re-deriving these from scratch.
	rawHeader        []byte // bytes from start of file through end of OptionalHeader
	coffOffset       uint32 // file offset of COFF header (== eLfanew + 4)
	optOffset        uint32 // file offset of OptionalHeader (== coffOffset + 20)
	optHeaderSize    uint16
	fileAlignment    uint32
	sectionAlignment uint32
}

// Section is one PE section in a UKI.
type Section struct {
	Name string
	Data []byte

	// Characteristics is the PE section flags (IMAGE_SCN_*). Preserved on
	// round-trip; on new sections added via SetSection it defaults to 0.
	// Typical UKI data section flags: 0x40000040 (CNT_INITIALIZED_DATA |
	// MEM_READ).
	Characteristics uint32

	// VirtualAddress is the RVA at which the loader maps this section in
	// memory. Preserved verbatim through round-trip; for newly added
	// sections, WriteTo synthesises an RVA from the existing layout.
	VirtualAddress uint32
}

// Parse reads a UKI from r. Returns ErrNotUKI for valid PE32+ inputs that
// lack a .linux section; returns a wrapped pe-package error for inputs
// that aren't valid PE.
func Parse(r io.Reader) (*Image, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("uki: read: %w", err)
	}
	f, err := pe.NewFile(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("uki: parse PE: %w", err)
	}
	defer f.Close()

	if len(raw) < dosELfanewOffset+4 {
		return nil, fmt.Errorf("uki: truncated DOS header")
	}
	eLfanew := binary.LittleEndian.Uint32(raw[dosELfanewOffset : dosELfanewOffset+4])
	coffOffset := eLfanew + peSignatureLen
	optOffset := coffOffset + coffHeaderLen
	if uint64(optOffset)+coffFieldSizeOfOptionalHeader+2 > uint64(len(raw)) {
		return nil, fmt.Errorf("uki: truncated COFF header")
	}
	optHeaderSize := binary.LittleEndian.Uint16(raw[coffOffset+coffFieldSizeOfOptionalHeader : coffOffset+coffFieldSizeOfOptionalHeader+2])
	if uint64(optOffset)+uint64(optHeaderSize) > uint64(len(raw)) {
		return nil, fmt.Errorf("uki: truncated OptionalHeader")
	}

	img := &Image{
		coffOffset:    coffOffset,
		optOffset:     optOffset,
		optHeaderSize: optHeaderSize,
	}
	if int(optHeaderSize) >= optFieldFileAlignment+4 {
		img.sectionAlignment = binary.LittleEndian.Uint32(raw[optOffset+optFieldSectionAlignment : optOffset+optFieldSectionAlignment+4])
		img.fileAlignment = binary.LittleEndian.Uint32(raw[optOffset+optFieldFileAlignment : optOffset+optFieldFileAlignment+4])
	}

	sectionTableStart := optOffset + uint32(optHeaderSize)
	img.rawHeader = make([]byte, sectionTableStart)
	copy(img.rawHeader, raw[:sectionTableStart])

	var hasLinux bool
	for _, s := range f.Sections {
		name := strings.TrimRight(s.Name, "\x00")
		data, err := s.Data()
		if err != nil {
			return nil, fmt.Errorf("uki: section %q: %w", name, err)
		}
		// SizeOfRawData is the on-disk size, padded to FileAlignment. Trim
		// to VirtualSize so callers see only the meaningful content and
		// round-trips compare equal regardless of padding.
		if s.VirtualSize > 0 && int(s.VirtualSize) < len(data) {
			data = data[:s.VirtualSize]
		}
		if name == SectionLinux {
			hasLinux = true
		}
		img.sections = append(img.sections, Section{
			Name:            name,
			Data:            data,
			Characteristics: s.Characteristics,
			VirtualAddress:  s.VirtualAddress,
		})
	}
	if !hasLinux {
		return nil, ErrNotUKI
	}
	return img, nil
}

// ParseFile opens path and parses it as a UKI.
func ParseFile(path string) (*Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// Sections returns the parsed sections in PE file order. The returned slice
// is a copy; mutating it does not affect the Image.
func (img *Image) Sections() []Section {
	out := make([]Section, len(img.sections))
	copy(out, img.sections)
	return out
}

// Section returns the first section with the given name, or nil. Multi-
// profile UKIs may have repeated sections (one .cmdline per profile); use
// Sections() to iterate them in order.
func (img *Image) Section(name string) *Section {
	for i := range img.sections {
		if img.sections[i].Name == name {
			return &img.sections[i]
		}
	}
	return nil
}

// SetSection replaces the first section matching name, or appends a new
// section if none exists. Replacement preserves the existing section's
// Characteristics and VirtualAddress so firmware-visible attributes don't
// change. Newly appended sections default to Characteristics=0 and
// VirtualAddress=0; if those need to be set, mutate the Section pointer
// returned by Section(name) after the call.
func (img *Image) SetSection(name string, data []byte) {
	for i := range img.sections {
		if img.sections[i].Name == name {
			img.sections[i].Data = data
			return
		}
	}
	img.sections = append(img.sections, Section{Name: name, Data: data})
}

// RemoveSection drops the first section matching name. Returns true if a
// section was removed, false if no match was found.
func (img *Image) RemoveSection(name string) bool {
	for i := range img.sections {
		if img.sections[i].Name == name {
			img.sections = append(img.sections[:i], img.sections[i+1:]...)
			return true
		}
	}
	return false
}

// WriteTo emits the UKI as a valid PE32+ binary. The original DOS, PE
// signature, COFF, and OptionalHeader bytes are preserved (so firmware-
// relevant fields like entry point, image base, and stack/heap sizes are
// untouched); only NumberOfSections, SizeOfHeaders, and the section table
// + section data are re-laid-out. Section data is padded to the original
// FileAlignment.
//
// Authenticode signatures are NOT preserved across WriteTo — any
// modification invalidates them, and signing is a separate concern.
// Callers needing signed output must re-sign the emitted bytes.
func (img *Image) WriteTo(w io.Writer) (int64, error) {
	if img.rawHeader == nil {
		return 0, fmt.Errorf("uki: WriteTo on uninitialised Image")
	}

	fileAlign := img.fileAlignment
	if fileAlign < 1 {
		fileAlign = 1
	}

	sectionTableOffset := uint32(len(img.rawHeader))
	sectionTableSize := uint32(len(img.sections)) * sectionHeaderLen
	sizeOfHeaders := alignUp(sectionTableOffset+sectionTableSize, fileAlign)

	type layout struct {
		ptr  uint32 // PointerToRawData
		size uint32 // SizeOfRawData
		vs   uint32 // VirtualSize
	}
	layouts := make([]layout, len(img.sections))
	offset := sizeOfHeaders
	for i, s := range img.sections {
		vs := uint32(len(s.Data))
		layouts[i] = layout{
			ptr:  offset,
			size: alignUp(vs, fileAlign),
			vs:   vs,
		}
		offset += layouts[i].size
	}

	header := make([]byte, len(img.rawHeader))
	copy(header, img.rawHeader)

	binary.LittleEndian.PutUint16(header[img.coffOffset+coffFieldNumberOfSections:img.coffOffset+coffFieldNumberOfSections+2], uint16(len(img.sections)))
	if int(img.optHeaderSize) >= optFieldSizeOfHeaders+4 {
		binary.LittleEndian.PutUint32(header[img.optOffset+optFieldSizeOfHeaders:img.optOffset+optFieldSizeOfHeaders+4], sizeOfHeaders)
	}

	var buf bytes.Buffer
	buf.Grow(int(offset))
	buf.Write(header)

	for i, s := range img.sections {
		hdr := make([]byte, sectionHeaderLen)
		copy(hdr[0:8], s.Name)
		binary.LittleEndian.PutUint32(hdr[8:12], layouts[i].vs)
		binary.LittleEndian.PutUint32(hdr[12:16], s.VirtualAddress)
		binary.LittleEndian.PutUint32(hdr[16:20], layouts[i].size)
		binary.LittleEndian.PutUint32(hdr[20:24], layouts[i].ptr)
		binary.LittleEndian.PutUint32(hdr[36:40], s.Characteristics)
		buf.Write(hdr)
	}

	if pad := int(sizeOfHeaders) - buf.Len(); pad > 0 {
		buf.Write(make([]byte, pad))
	}

	for i, s := range img.sections {
		if got := uint32(buf.Len()); got != layouts[i].ptr {
			return 0, fmt.Errorf("uki: layout drift at section %q: expected ptr=%d got=%d", s.Name, layouts[i].ptr, got)
		}
		buf.Write(s.Data)
		if pad := int(layouts[i].size) - len(s.Data); pad > 0 {
			buf.Write(make([]byte, pad))
		}
	}

	n, err := w.Write(buf.Bytes())
	return int64(n), err
}

// alignUp rounds v up to the nearest multiple of align (which must be a
// power of two). Returns v unchanged when align <= 1.
func alignUp(v, align uint32) uint32 {
	if align <= 1 {
		return v
	}
	return (v + align - 1) &^ (align - 1)
}
