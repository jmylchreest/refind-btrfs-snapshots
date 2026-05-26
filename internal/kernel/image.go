package kernel

import (
	"fmt"
	"strings"
)

// ImageRole classifies what role a boot image serves on disk.
type ImageRole string

const (
	RoleKernel            ImageRole = "kernel"
	RoleInitramfs         ImageRole = "initramfs"
	RoleFallbackInitramfs ImageRole = "fallback_initramfs"
	RoleMicrocode         ImageRole = "microcode"
	// RoleUKI is a Unified Kernel Image: a single PE/EFI binary that bundles
	// kernel, initramfs, cmdline, and metadata. See the Boot Loader Specification
	// Type #2 entries: https://uapi-group.org/specifications/specs/boot_loader_specification/
	RoleUKI ImageRole = "uki"
)

// roleOrder defines the sort priority for each ImageRole.
// Lower values sort first. Declared alongside the constants so new roles
// are easy to add in one place.
var roleOrder = map[ImageRole]int{
	RoleKernel:            0,
	RoleUKI:               1,
	RoleInitramfs:         2,
	RoleFallbackInitramfs: 3,
	RoleMicrocode:         4,
}

// ParseImageRole converts a string to an ImageRole, returning an error for unknown values.
func ParseImageRole(s string) (ImageRole, error) {
	switch ImageRole(s) {
	case RoleKernel, RoleInitramfs, RoleFallbackInitramfs, RoleMicrocode, RoleUKI:
		return ImageRole(s), nil
	default:
		return "", fmt.Errorf("unknown image role: %q (valid: kernel, initramfs, fallback_initramfs, microcode, uki)", s)
	}
}

// BootLayout describes how a kernel's artefacts are arranged on disk.
// LayoutSplit is loose kernel+initrd files. LayoutBLS is a Type #1 .conf
// referencing loose files. LayoutUKI is a Type #2 self-contained PE.
// Spec: https://uapi-group.org/specifications/specs/boot_loader_specification/
type BootLayout string

const (
	LayoutSplit BootLayout = "split"
	LayoutBLS   BootLayout = "bls"
	LayoutUKI   BootLayout = "uki"
)

func ParseBootLayout(s string) (BootLayout, error) {
	switch BootLayout(s) {
	case LayoutSplit, LayoutBLS, LayoutUKI:
		return BootLayout(s), nil
	default:
		return "", fmt.Errorf("unknown boot layout: %q (valid: split, bls, uki)", s)
	}
}

// PatternConfig is a YAML-deserializable boot image matching pattern.
// Patterns are evaluated in order; the first match wins for each file.
type PatternConfig struct {
	// Glob is the filename glob pattern (e.g., "vmlinuz-*", "initramfs-*-fallback.img").
	Glob string `yaml:"glob" mapstructure:"glob"`

	// Role classifies the matched file (kernel, initramfs, fallback_initramfs, microcode).
	Role ImageRole `yaml:"role" mapstructure:"role"`

	// StripPrefix is removed from the filename to derive KernelName.
	// e.g., "vmlinuz-" strips "vmlinuz-linux" to "linux".
	StripPrefix string `yaml:"strip_prefix,omitempty" mapstructure:"strip_prefix"`

	// StripSuffix is removed from the filename to derive KernelName.
	// e.g., ".img" strips "initramfs-linux.img" to "initramfs-linux" (combined with StripPrefix).
	StripSuffix string `yaml:"strip_suffix,omitempty" mapstructure:"strip_suffix"`

	// KernelName overrides the strip-derived name. Used for generic filenames like
	// "vmlinuz" where no suffix exists to strip (set to "linux").
	// Also left empty for microcode images which are shared across all boot sets.
	KernelName string `yaml:"kernel_name,omitempty" mapstructure:"kernel_name"`
}

// DeriveKernelName extracts the kernel name from a filename using this pattern's rules.
// If KernelName is set, it is returned directly (override).
// Otherwise, StripPrefix and StripSuffix are applied to the filename.
// Returns empty string for microcode patterns (no kernel association).
func (p *PatternConfig) DeriveKernelName(filename string) string {
	if p.KernelName != "" {
		return p.KernelName
	}

	name := filename
	if p.StripPrefix != "" {
		name = strings.TrimPrefix(name, p.StripPrefix)
	}
	if p.StripSuffix != "" {
		name = strings.TrimSuffix(name, p.StripSuffix)
	}

	// If stripping produced an empty string, return empty
	if name == "" {
		return ""
	}

	return name
}

// InspectedMetadata holds binary-inspection results for a boot image.
// These fields are optional and populated on a best-effort basis. Which
// fields are present depends on the detected Format.
type InspectedMetadata struct {
	// Format is the inspector's verdict on the binary type, independent of
	// any hint passed in by discovery. One of: "bzimage", "uki", "initramfs",
	// "microcode", "unknown".
	Format string

	// Version is the short kernel version string (e.g., "6.19.0-2-cachyos").
	// Extracted from the bzImage header for split kernels, or from a UKI's
	// .uname section for UKIs. Empty for initramfs/microcode.
	Version string

	// VersionFull is the complete version string from the kernel header
	// (e.g., "6.19.0-2-cachyos (linux-cachyos@cachyos) #1 SMP PREEMPT_DYNAMIC ...").
	VersionFull string

	// BootProtocol is the Linux boot protocol version (e.g., "2.15").
	// Only populated for bzImage kernels.
	BootProtocol string

	// CompressFormat is the detected compression format of an initramfs image
	// (e.g., "gzip", "zstd", "xz", "lz4", "cpio", "unknown").
	// Only populated for initramfs/fallback images.
	CompressFormat string

	// Cmdline is the embedded kernel command line. Only populated for UKIs
	// (from the .cmdline PE section).
	Cmdline string

	// OSReleaseID identifies the OS image baked into a UKI (e.g., "arch",
	// "fedora"). Read from the ID= field of the .osrel PE section.
	OSReleaseID string

	// OSReleasePrettyName is the human-readable name from the .osrel section
	// of a UKI (e.g., "Arch Linux").
	OSReleasePrettyName string

	// MicrocodeVendor is "Intel" or "AMD" for microcode images. Empty for
	// other roles or when the file could not be parsed.
	MicrocodeVendor string

	// MicrocodeLatestDate is the most recent update date among all blocks
	// in a microcode image, formatted as "YYYY-MM-DD". Empty when unparseable.
	MicrocodeLatestDate string

	// MicrocodeBlockCount is the number of update blocks (Intel) or patch
	// records (AMD) in a microcode image.
	MicrocodeBlockCount int

	// MicrocodeRevisions lists the update_revision (Intel) or patch_id (AMD)
	// values per block, in encounter order.
	MicrocodeRevisions []uint32

	// MicrocodeProcessorSignatures lists the CPU identifier of each block:
	// processor_signature for Intel, processor_rev_id for AMD.
	MicrocodeProcessorSignatures []uint32
}

// BootImage is a discovered boot image file. Inspected is nil when binary
// inspection failed — the scanner falls back to filename-derived metadata.
type BootImage struct {
	Path       string // ESP-relative path, e.g. /boot/vmlinuz-linux
	AbsPath    string // absolute filesystem path
	Filename   string
	Role       ImageRole
	KernelName string // empty for microcode
	Inspected  *InspectedMetadata
}

// BootSet groups the artefacts that make a single bootable configuration.
// Slots not relevant to the Layout are nil.
type BootSet struct {
	KernelName string
	Layout     BootLayout

	Kernel    *BootImage // Split / BLS
	Initramfs *BootImage // Split / BLS
	Fallback  *BootImage // Split / BLS

	// Entry is the parsed BLS .conf for LayoutBLS sets. Typed as any to
	// avoid an import cycle with the bls package.
	Entry any

	UKI *BootImage // LayoutUKI

	// Microcode is shared across all layouts. UKIs may embed it in .ucode.
	Microcode []*BootImage
}

func (bs *BootSet) HasFallback() bool { return bs.Fallback != nil }

// PrimaryImage is the UKI for LayoutUKI, the Kernel otherwise — the image
// from which we read version metadata for staleness checks.
func (bs *BootSet) PrimaryImage() *BootImage {
	if bs.Layout == LayoutUKI {
		return bs.UKI
	}
	return bs.Kernel
}

func (bs *BootSet) KernelVersion() string {
	if img := bs.PrimaryImage(); img != nil && img.Inspected != nil {
		return img.Inspected.Version
	}
	return ""
}

func (bs *BootSet) DisplayName() string {
	if bs.KernelName == "" {
		return "Linux"
	}
	return strings.ToUpper(bs.KernelName[:1]) + bs.KernelName[1:]
}
