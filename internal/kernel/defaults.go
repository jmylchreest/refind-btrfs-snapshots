package kernel

// DefaultPatterns returns the standard set of boot image matching patterns.
// These cover Arch Linux, Debian/Ubuntu, Fedora, Gentoo, and generic setups.
//
// Order matters: patterns are evaluated top-to-bottom, first match wins per file.
// More specific patterns (e.g., fallback initramfs) must appear before generic ones.
func DefaultPatterns() []PatternConfig {
	return []PatternConfig{
		// Fallback initramfs (must be before regular initramfs to match first)
		{
			Glob:        "initramfs-*-fallback.img",
			Role:        RoleFallbackInitramfs,
			StripPrefix: "initramfs-",
			StripSuffix: "-fallback.img",
		},

		// Regular initramfs (Arch-style)
		{
			Glob:        "initramfs-*.img",
			Role:        RoleInitramfs,
			StripPrefix: "initramfs-",
			StripSuffix: ".img",
		},

		// Kernels (Arch/generic vmlinuz-* naming)
		{
			Glob:        "vmlinuz-*",
			Role:        RoleKernel,
			StripPrefix: "vmlinuz-",
		},

		// Debian/Ubuntu initramfs (initrd.img-<version>)
		{
			Glob:        "initrd.img-*",
			Role:        RoleInitramfs,
			StripPrefix: "initrd.img-",
		},

		// Generic kernel filenames (no suffix to strip, override kernel name)
		{
			Glob:       "vmlinuz",
			Role:       RoleKernel,
			KernelName: "linux",
		},
		{
			Glob:       "vmlinuz.efi",
			Role:       RoleKernel,
			KernelName: "linux",
		},
		{
			Glob:       "bzImage",
			Role:       RoleKernel,
			KernelName: "linux",
		},

		// Generic initramfs filenames (no suffix to strip, override kernel name)
		{
			Glob:       "initrd.img",
			Role:       RoleInitramfs,
			KernelName: "linux",
		},
		{
			Glob:       "initrd",
			Role:       RoleInitramfs,
			KernelName: "linux",
		},
		{
			Glob:       "initramfs.img",
			Role:       RoleInitramfs,
			KernelName: "linux",
		},

		// Microcode images (shared across all boot sets, no kernel name)
		{
			Glob: "intel-ucode.img",
			Role: RoleMicrocode,
		},
		{
			Glob: "amd-ucode.img",
			Role: RoleMicrocode,
		},
	}
}
