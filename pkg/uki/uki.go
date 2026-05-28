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

// Image is a parsed UKI. Sections appear in PE file order; multi-profile
// UKIs may have repeated section names (e.g. one .cmdline per profile).
type Image struct {
	sections []Section
}

// Section is one PE section in a UKI.
type Section struct {
	Name string
	Data []byte
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

	img := &Image{}
	var hasLinux bool
	for _, s := range f.Sections {
		name := strings.TrimRight(s.Name, "\x00")
		data, err := s.Data()
		if err != nil {
			return nil, fmt.Errorf("uki: section %q: %w", name, err)
		}
		if name == SectionLinux {
			hasLinux = true
		}
		img.sections = append(img.sections, Section{Name: name, Data: data})
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
