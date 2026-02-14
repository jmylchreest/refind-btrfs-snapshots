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
)

// roleOrder defines the sort priority for each ImageRole.
// Lower values sort first. Declared alongside the constants so new roles
// are easy to add in one place.
var roleOrder = map[ImageRole]int{
	RoleKernel:            0,
	RoleInitramfs:         1,
	RoleFallbackInitramfs: 2,
	RoleMicrocode:         3,
}

// ParseImageRole converts a string to an ImageRole, returning an error for unknown values.
func ParseImageRole(s string) (ImageRole, error) {
	switch ImageRole(s) {
	case RoleKernel, RoleInitramfs, RoleFallbackInitramfs, RoleMicrocode:
		return ImageRole(s), nil
	default:
		return "", fmt.Errorf("unknown image role: %q (valid: kernel, initramfs, fallback_initramfs, microcode)", s)
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
// These fields are optional and populated on a best-effort basis.
type InspectedMetadata struct {
	// Version is the short kernel version string (e.g., "6.19.0-2-cachyos").
	// Extracted from the bzImage header for kernels. Empty for initramfs/microcode.
	Version string

	// VersionFull is the complete version string from the kernel header
	// (e.g., "6.19.0-2-cachyos (linux-cachyos@cachyos) #1 SMP PREEMPT_DYNAMIC ...").
	VersionFull string

	// BootProtocol is the Linux boot protocol version (e.g., "2.15").
	// Only populated for kernel images.
	BootProtocol string

	// CompressFormat is the detected compression format of an initramfs image
	// (e.g., "gzip", "zstd", "xz", "lz4", "cpio", "unknown").
	// Only populated for initramfs/fallback images.
	CompressFormat string
}

// BootImage represents a discovered boot image file on disk.
type BootImage struct {
	// Path is the ESP-relative path (e.g., "/boot/vmlinuz-linux").
	Path string

	// AbsPath is the absolute filesystem path (e.g., "/boot/efi/boot/vmlinuz-linux").
	AbsPath string

	// Filename is the base filename (e.g., "vmlinuz-linux").
	Filename string

	// Role classifies this image (kernel, initramfs, fallback_initramfs, microcode).
	Role ImageRole

	// KernelName is the derived kernel identifier that groups related images together
	// (e.g., "linux", "linux-lts", "linux-cachyos"). Empty for microcode.
	KernelName string

	// Inspected holds binary-inspection metadata. Nil if inspection was not attempted
	// or failed (in which case the scanner logs a warning and falls back to filename-only).
	Inspected *InspectedMetadata
}

// BootSet groups related boot images that share a kernel name.
// A typical set contains a kernel, its matching initramfs, an optional fallback,
// and any shared microcode images.
type BootSet struct {
	// KernelName is the shared identifier (e.g., "linux", "linux-lts").
	KernelName string

	// Kernel is the kernel image (e.g., vmlinuz-linux). May be nil if only
	// an initramfs was found (edge case, logged as warning).
	Kernel *BootImage

	// Initramfs is the primary initramfs image. May be nil if not found.
	Initramfs *BootImage

	// Fallback is the fallback initramfs image. Nil if not present on disk.
	Fallback *BootImage

	// Microcode contains shared microcode images (e.g., intel-ucode.img, amd-ucode.img).
	// These are not kernel-specific and are included in every boot set.
	Microcode []*BootImage
}

// HasFallback returns whether a fallback initramfs exists for this boot set.
func (bs *BootSet) HasFallback() bool {
	return bs.Fallback != nil
}

// KernelVersion returns the inspected kernel version, or empty string if
// inspection was not available (triggering filename-only staleness matching).
func (bs *BootSet) KernelVersion() string {
	if bs.Kernel != nil && bs.Kernel.Inspected != nil {
		return bs.Kernel.Inspected.Version
	}
	return ""
}

// DisplayName returns a human-readable name for this boot set.
// Uses KernelName with the first letter capitalised (e.g., "linux" -> "Linux",
// "linux-lts" -> "Linux-lts").
func (bs *BootSet) DisplayName() string {
	if bs.KernelName == "" {
		return "Linux"
	}
	return strings.ToUpper(bs.KernelName[:1]) + bs.KernelName[1:]
}
