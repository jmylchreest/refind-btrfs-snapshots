package kernel

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestBootDir creates a temporary directory with the given filenames.
func createTestBootDir(t *testing.T, files []string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		path := filepath.Join(dir, f)
		require.NoError(t, os.WriteFile(path, []byte("test"), 0644))
	}
	return dir
}

func TestScanDir_TypicalArch(t *testing.T) {
	dir := createTestBootDir(t, []string{
		"vmlinuz-linux",
		"initramfs-linux.img",
		"initramfs-linux-fallback.img",
		"intel-ucode.img",
	})

	scanner := NewScanner(dir, DefaultPatterns())
	images, err := scanner.ScanDir(dir)
	require.NoError(t, err)
	assert.Len(t, images, 4)

	// Check roles
	roles := make(map[ImageRole]int)
	for _, img := range images {
		roles[img.Role]++
	}
	assert.Equal(t, 1, roles[RoleKernel])
	assert.Equal(t, 1, roles[RoleInitramfs])
	assert.Equal(t, 1, roles[RoleFallbackInitramfs])
	assert.Equal(t, 1, roles[RoleMicrocode])
}

func TestScanDir_MultipleKernels(t *testing.T) {
	dir := createTestBootDir(t, []string{
		"vmlinuz-linux",
		"vmlinuz-linux-lts",
		"initramfs-linux.img",
		"initramfs-linux-lts.img",
		"initramfs-linux-fallback.img",
		"initramfs-linux-lts-fallback.img",
	})

	scanner := NewScanner(dir, DefaultPatterns())
	images, err := scanner.ScanDir(dir)
	require.NoError(t, err)
	assert.Len(t, images, 6)

	// Check kernel names
	kernelNames := make(map[string]bool)
	for _, img := range images {
		if img.Role == RoleKernel {
			kernelNames[img.KernelName] = true
		}
	}
	assert.True(t, kernelNames["linux"])
	assert.True(t, kernelNames["linux-lts"])
}

func TestScanDir_CachyOS(t *testing.T) {
	dir := createTestBootDir(t, []string{
		"vmlinuz-linux-cachyos",
		"initramfs-linux-cachyos.img",
		"amd-ucode.img",
	})

	scanner := NewScanner(dir, DefaultPatterns())
	images, err := scanner.ScanDir(dir)
	require.NoError(t, err)
	assert.Len(t, images, 3)

	// Verify kernel name derivation
	for _, img := range images {
		if img.Role == RoleKernel {
			assert.Equal(t, "linux-cachyos", img.KernelName)
		}
	}
}

func TestScanDir_GenericKernel(t *testing.T) {
	dir := createTestBootDir(t, []string{
		"vmlinuz",
		"initramfs.img",
	})

	scanner := NewScanner(dir, DefaultPatterns())
	images, err := scanner.ScanDir(dir)
	require.NoError(t, err)
	assert.Len(t, images, 2)

	for _, img := range images {
		assert.Equal(t, "linux", img.KernelName, "generic filenames should map to 'linux'")
	}
}

func TestScanDir_BzImage(t *testing.T) {
	dir := createTestBootDir(t, []string{
		"bzImage",
	})

	scanner := NewScanner(dir, DefaultPatterns())
	images, err := scanner.ScanDir(dir)
	require.NoError(t, err)
	require.Len(t, images, 1)
	assert.Equal(t, RoleKernel, images[0].Role)
	assert.Equal(t, "linux", images[0].KernelName)
}

func TestScanDir_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	scanner := NewScanner(dir, DefaultPatterns())
	images, err := scanner.ScanDir(dir)
	require.NoError(t, err)
	assert.Empty(t, images)
}

func TestScanDir_NonexistentDir(t *testing.T) {
	scanner := NewScanner("/tmp", DefaultPatterns())
	_, err := scanner.ScanDir("/nonexistent/path/12345")
	assert.Error(t, err)
}

func TestScanDir_NoMatchingFiles(t *testing.T) {
	dir := createTestBootDir(t, []string{
		"random-file.txt",
		"config.cfg",
		".hidden",
	})

	scanner := NewScanner(dir, DefaultPatterns())
	images, err := scanner.ScanDir(dir)
	require.NoError(t, err)
	assert.Empty(t, images)
}

func TestScanDir_DirectoriesSkipped(t *testing.T) {
	dir := t.TempDir()
	// Create a directory that looks like a kernel name
	require.NoError(t, os.Mkdir(filepath.Join(dir, "vmlinuz-linux"), 0755))
	// Create a real file too
	require.NoError(t, os.WriteFile(filepath.Join(dir, "vmlinuz-linux-lts"), []byte("test"), 0644))

	scanner := NewScanner(dir, DefaultPatterns())
	images, err := scanner.ScanDir(dir)
	require.NoError(t, err)
	require.Len(t, images, 1)
	assert.Equal(t, "vmlinuz-linux-lts", images[0].Filename)
}

func TestScanDir_CustomPatterns(t *testing.T) {
	dir := createTestBootDir(t, []string{
		"kernel-custom",
		"initrd-custom.img",
	})

	patterns := []PatternConfig{
		{Glob: "kernel-*", Role: RoleKernel, StripPrefix: "kernel-"},
		{Glob: "initrd-*.img", Role: RoleInitramfs, StripPrefix: "initrd-", StripSuffix: ".img"},
	}

	scanner := NewScanner(dir, patterns)
	images, err := scanner.ScanDir(dir)
	require.NoError(t, err)
	assert.Len(t, images, 2)

	for _, img := range images {
		assert.Equal(t, "custom", img.KernelName)
	}
}

func TestScanDir_EmptyPatternList(t *testing.T) {
	dir := createTestBootDir(t, []string{"vmlinuz-linux"})

	// Empty pattern list falls back to defaults
	scanner := NewScanner(dir, nil)
	images, err := scanner.ScanDir(dir)
	require.NoError(t, err)
	assert.Len(t, images, 1) // DefaultPatterns should match vmlinuz-linux
}

func TestScanDir_FallbackMatchesBeforeRegular(t *testing.T) {
	dir := createTestBootDir(t, []string{
		"initramfs-linux-fallback.img",
	})

	scanner := NewScanner(dir, DefaultPatterns())
	images, err := scanner.ScanDir(dir)
	require.NoError(t, err)
	require.Len(t, images, 1)
	assert.Equal(t, RoleFallbackInitramfs, images[0].Role, "fallback pattern should match before regular initramfs")
	assert.Equal(t, "linux", images[0].KernelName)
}

func TestScanDir_FirstMatchWins(t *testing.T) {
	dir := createTestBootDir(t, []string{
		"vmlinuz-linux",
	})

	// Two patterns match "vmlinuz-linux", first one should win
	patterns := []PatternConfig{
		{Glob: "vmlinuz-*", Role: RoleKernel, StripPrefix: "vmlinuz-"},
		{Glob: "vmlinuz-*", Role: RoleInitramfs, StripPrefix: "vmlinuz-"}, // would be wrong role
	}

	scanner := NewScanner(dir, patterns)
	images, err := scanner.ScanDir(dir)
	require.NoError(t, err)
	require.Len(t, images, 1)
	assert.Equal(t, RoleKernel, images[0].Role)
}

func TestScanDir_ESPRelativePaths(t *testing.T) {
	dir := createTestBootDir(t, []string{
		"vmlinuz-linux",
	})

	scanner := NewScanner(dir, DefaultPatterns())
	images, err := scanner.ScanDir(dir)
	require.NoError(t, err)
	require.Len(t, images, 1)

	// Path should be ESP-relative (starting with /)
	assert.Equal(t, "/vmlinuz-linux", images[0].Path)
	// AbsPath should be the full path
	assert.Equal(t, filepath.Join(dir, "vmlinuz-linux"), images[0].AbsPath)
}

func TestScanDir_SortOrder(t *testing.T) {
	dir := createTestBootDir(t, []string{
		"intel-ucode.img",              // microcode
		"initramfs-linux.img",          // initramfs
		"initramfs-linux-fallback.img", // fallback
		"vmlinuz-linux",                // kernel
	})

	scanner := NewScanner(dir, DefaultPatterns())
	images, err := scanner.ScanDir(dir)
	require.NoError(t, err)
	require.Len(t, images, 4)

	// Should be sorted: kernel, initramfs, fallback, microcode
	assert.Equal(t, RoleKernel, images[0].Role)
	assert.Equal(t, RoleInitramfs, images[1].Role)
	assert.Equal(t, RoleFallbackInitramfs, images[2].Role)
	assert.Equal(t, RoleMicrocode, images[3].Role)
}

// --- BuildBootSets tests ---

func TestBuildBootSets_SingleKernel(t *testing.T) {
	images := []*BootImage{
		{Filename: "vmlinuz-linux", Role: RoleKernel, KernelName: "linux"},
		{Filename: "initramfs-linux.img", Role: RoleInitramfs, KernelName: "linux"},
		{Filename: "initramfs-linux-fallback.img", Role: RoleFallbackInitramfs, KernelName: "linux"},
		{Filename: "intel-ucode.img", Role: RoleMicrocode},
	}

	scanner := NewScanner("/boot", nil)
	sets := scanner.BuildBootSets(images)

	require.Len(t, sets, 1)
	bs := sets[0]
	assert.Equal(t, "linux", bs.KernelName)
	assert.NotNil(t, bs.Kernel)
	assert.NotNil(t, bs.Initramfs)
	assert.NotNil(t, bs.Fallback)
	assert.Len(t, bs.Microcode, 1)
}

func TestBuildBootSets_MultipleKernels(t *testing.T) {
	images := []*BootImage{
		{Filename: "vmlinuz-linux", Role: RoleKernel, KernelName: "linux"},
		{Filename: "vmlinuz-linux-lts", Role: RoleKernel, KernelName: "linux-lts"},
		{Filename: "initramfs-linux.img", Role: RoleInitramfs, KernelName: "linux"},
		{Filename: "initramfs-linux-lts.img", Role: RoleInitramfs, KernelName: "linux-lts"},
	}

	scanner := NewScanner("/boot", nil)
	sets := scanner.BuildBootSets(images)

	require.Len(t, sets, 2)
	// Sorted by kernel name
	assert.Equal(t, "linux", sets[0].KernelName)
	assert.Equal(t, "linux-lts", sets[1].KernelName)
}

func TestBuildBootSets_MicrocodeShared(t *testing.T) {
	images := []*BootImage{
		{Filename: "vmlinuz-linux", Role: RoleKernel, KernelName: "linux"},
		{Filename: "vmlinuz-linux-lts", Role: RoleKernel, KernelName: "linux-lts"},
		{Filename: "intel-ucode.img", Role: RoleMicrocode},
		{Filename: "amd-ucode.img", Role: RoleMicrocode},
	}

	scanner := NewScanner("/boot", nil)
	sets := scanner.BuildBootSets(images)

	require.Len(t, sets, 2)
	// Both sets should have the same microcode images
	assert.Len(t, sets[0].Microcode, 2)
	assert.Len(t, sets[1].Microcode, 2)
}

func TestBuildBootSets_KernelWithoutInitramfs(t *testing.T) {
	images := []*BootImage{
		{Filename: "vmlinuz-linux", Role: RoleKernel, KernelName: "linux"},
	}

	scanner := NewScanner("/boot", nil)
	sets := scanner.BuildBootSets(images)

	require.Len(t, sets, 1)
	assert.NotNil(t, sets[0].Kernel)
	assert.Nil(t, sets[0].Initramfs)
}

func TestBuildBootSets_InitramfsWithoutKernel(t *testing.T) {
	images := []*BootImage{
		{Filename: "initramfs-linux.img", Role: RoleInitramfs, KernelName: "linux"},
	}

	scanner := NewScanner("/boot", nil)
	sets := scanner.BuildBootSets(images)

	require.Len(t, sets, 1)
	assert.Nil(t, sets[0].Kernel)
	assert.NotNil(t, sets[0].Initramfs)
}

func TestBuildBootSets_FallbackOnly(t *testing.T) {
	images := []*BootImage{
		{Filename: "vmlinuz-linux", Role: RoleKernel, KernelName: "linux"},
		{Filename: "initramfs-linux-fallback.img", Role: RoleFallbackInitramfs, KernelName: "linux"},
	}

	scanner := NewScanner("/boot", nil)
	sets := scanner.BuildBootSets(images)

	require.Len(t, sets, 1)
	assert.Nil(t, sets[0].Initramfs)
	assert.NotNil(t, sets[0].Fallback)
	assert.True(t, sets[0].HasFallback())
}

func TestBuildBootSets_DuplicateKernel(t *testing.T) {
	images := []*BootImage{
		{Filename: "vmlinuz-linux", Role: RoleKernel, KernelName: "linux", AbsPath: "/boot/vmlinuz-linux"},
		{Filename: "vmlinuz-linux-copy", Role: RoleKernel, KernelName: "linux", AbsPath: "/boot/vmlinuz-linux-copy"},
	}

	scanner := NewScanner("/boot", nil)
	sets := scanner.BuildBootSets(images)

	require.Len(t, sets, 1)
	// First one wins
	assert.Equal(t, "vmlinuz-linux", sets[0].Kernel.Filename)
}

func TestBuildBootSets_EmptyInput(t *testing.T) {
	scanner := NewScanner("/boot", nil)
	sets := scanner.BuildBootSets(nil)
	assert.Empty(t, sets)
}

func TestBuildBootSets_SkipsEmptyKernelName(t *testing.T) {
	images := []*BootImage{
		{Filename: "mystery-file", Role: RoleKernel, KernelName: ""},
	}

	scanner := NewScanner("/boot", nil)
	sets := scanner.BuildBootSets(images)
	assert.Empty(t, sets)
}
