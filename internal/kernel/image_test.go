package kernel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseImageRole_Valid(t *testing.T) {
	tests := []struct {
		input    string
		expected ImageRole
	}{
		{"kernel", RoleKernel},
		{"initramfs", RoleInitramfs},
		{"fallback_initramfs", RoleFallbackInitramfs},
		{"microcode", RoleMicrocode},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			role, err := ParseImageRole(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, role)
		})
	}
}

func TestParseImageRole_Invalid(t *testing.T) {
	_, err := ParseImageRole("bogus")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown image role")
}

func TestParseImageRole_Empty(t *testing.T) {
	_, err := ParseImageRole("")
	assert.Error(t, err)
}

func TestDeriveKernelName_StripPrefix(t *testing.T) {
	p := PatternConfig{
		Glob:        "vmlinuz-*",
		Role:        RoleKernel,
		StripPrefix: "vmlinuz-",
	}
	assert.Equal(t, "linux", p.DeriveKernelName("vmlinuz-linux"))
	assert.Equal(t, "linux-lts", p.DeriveKernelName("vmlinuz-linux-lts"))
	assert.Equal(t, "linux-zen", p.DeriveKernelName("vmlinuz-linux-zen"))
	assert.Equal(t, "linux-cachyos", p.DeriveKernelName("vmlinuz-linux-cachyos"))
}

func TestDeriveKernelName_StripPrefixAndSuffix(t *testing.T) {
	p := PatternConfig{
		Glob:        "initramfs-*.img",
		Role:        RoleInitramfs,
		StripPrefix: "initramfs-",
		StripSuffix: ".img",
	}
	assert.Equal(t, "linux", p.DeriveKernelName("initramfs-linux.img"))
	assert.Equal(t, "linux-lts", p.DeriveKernelName("initramfs-linux-lts.img"))
}

func TestDeriveKernelName_FallbackInitramfs(t *testing.T) {
	p := PatternConfig{
		Glob:        "initramfs-*-fallback.img",
		Role:        RoleFallbackInitramfs,
		StripPrefix: "initramfs-",
		StripSuffix: "-fallback.img",
	}
	assert.Equal(t, "linux", p.DeriveKernelName("initramfs-linux-fallback.img"))
	assert.Equal(t, "linux-lts", p.DeriveKernelName("initramfs-linux-lts-fallback.img"))
	assert.Equal(t, "linux-cachyos", p.DeriveKernelName("initramfs-linux-cachyos-fallback.img"))
}

func TestDeriveKernelName_Override(t *testing.T) {
	p := PatternConfig{
		Glob:       "vmlinuz",
		Role:       RoleKernel,
		KernelName: "linux",
	}
	// Override takes precedence, strip rules are ignored
	assert.Equal(t, "linux", p.DeriveKernelName("vmlinuz"))
	assert.Equal(t, "linux", p.DeriveKernelName("anything-else"))
}

func TestDeriveKernelName_OverrideTakesPrecedenceOverStrip(t *testing.T) {
	p := PatternConfig{
		Glob:        "vmlinuz-*",
		Role:        RoleKernel,
		StripPrefix: "vmlinuz-",
		KernelName:  "custom",
	}
	assert.Equal(t, "custom", p.DeriveKernelName("vmlinuz-linux"))
}

func TestDeriveKernelName_NoStripRules(t *testing.T) {
	p := PatternConfig{
		Glob: "intel-ucode.img",
		Role: RoleMicrocode,
	}
	// No strip rules, no override — returns the filename itself
	assert.Equal(t, "intel-ucode.img", p.DeriveKernelName("intel-ucode.img"))
}

func TestDeriveKernelName_EmptyAfterStrip(t *testing.T) {
	p := PatternConfig{
		Glob:        "vmlinuz-",
		Role:        RoleKernel,
		StripPrefix: "vmlinuz-",
	}
	// Stripping removes everything
	assert.Equal(t, "", p.DeriveKernelName("vmlinuz-"))
}

func TestDeriveKernelName_StripDoesNotMatch(t *testing.T) {
	p := PatternConfig{
		Glob:        "vmlinuz-*",
		Role:        RoleKernel,
		StripPrefix: "nope-",
	}
	// Prefix doesn't match, filename returned unchanged
	assert.Equal(t, "vmlinuz-linux", p.DeriveKernelName("vmlinuz-linux"))
}

func TestDeriveKernelName_DebianStyle(t *testing.T) {
	p := PatternConfig{
		Glob:        "initrd.img-*",
		Role:        RoleInitramfs,
		StripPrefix: "initrd.img-",
	}
	assert.Equal(t, "6.1.0-21-amd64", p.DeriveKernelName("initrd.img-6.1.0-21-amd64"))
}

func TestBootSet_HasFallback(t *testing.T) {
	bs := &BootSet{KernelName: "linux"}
	assert.False(t, bs.HasFallback())

	bs.Fallback = &BootImage{Filename: "initramfs-linux-fallback.img"}
	assert.True(t, bs.HasFallback())
}

func TestBootSet_KernelVersion_Inspected(t *testing.T) {
	bs := &BootSet{
		KernelName: "linux",
		Kernel: &BootImage{
			Filename: "vmlinuz-linux",
			Inspected: &InspectedMetadata{
				Version: "6.19.0-2-cachyos",
			},
		},
	}
	assert.Equal(t, "6.19.0-2-cachyos", bs.KernelVersion())
}

func TestBootSet_KernelVersion_NoInspection(t *testing.T) {
	bs := &BootSet{
		KernelName: "linux",
		Kernel:     &BootImage{Filename: "vmlinuz-linux"},
	}
	assert.Equal(t, "", bs.KernelVersion())
}

func TestBootSet_KernelVersion_NoKernel(t *testing.T) {
	bs := &BootSet{KernelName: "linux"}
	assert.Equal(t, "", bs.KernelVersion())
}

func TestBootSet_DisplayName(t *testing.T) {
	tests := []struct {
		kernelName string
		expected   string
	}{
		{"linux", "Linux"},
		{"linux-lts", "Linux-lts"},
		{"linux-cachyos", "Linux-cachyos"},
		{"", "Linux"},
	}

	for _, tt := range tests {
		t.Run(tt.kernelName, func(t *testing.T) {
			bs := &BootSet{KernelName: tt.kernelName}
			assert.Equal(t, tt.expected, bs.DisplayName())
		})
	}
}

func TestRoleOrder_AllRolesDefined(t *testing.T) {
	// Ensure all roles have an entry in the order map
	roles := []ImageRole{RoleKernel, RoleInitramfs, RoleFallbackInitramfs, RoleMicrocode}
	for _, role := range roles {
		_, exists := roleOrder[role]
		assert.True(t, exists, "roleOrder missing entry for %q", role)
	}
}

func TestRoleOrder_KernelFirst(t *testing.T) {
	assert.Less(t, roleOrder[RoleKernel], roleOrder[RoleInitramfs])
	assert.Less(t, roleOrder[RoleInitramfs], roleOrder[RoleFallbackInitramfs])
	assert.Less(t, roleOrder[RoleFallbackInitramfs], roleOrder[RoleMicrocode])
}
