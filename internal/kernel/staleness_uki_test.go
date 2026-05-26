package kernel

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Documented contract: CheckSnapshot must work for UKI-layout boot sets,
// sourcing the kernel version from the UKI's InspectedMetadata.Version
// (populated from the PE .uname section). Behavioural rule is identical
// to split-layout: snapshot's /lib/modules/<ver>/ must contain a directory
// matching the version.

func setupModulesDir(t *testing.T, fsPath string, version string) {
	t.Helper()
	modDir := filepath.Join(fsPath, "lib", "modules", version)
	require.NoError(t, os.MkdirAll(modDir, 0o755))
}

func makeUKIBootSet(version string) *BootSet {
	return &BootSet{
		KernelName: "linux",
		Layout:     LayoutUKI,
		UKI: &BootImage{
			Filename:   "linux.efi",
			Role:       RoleUKI,
			KernelName: "linux",
			Inspected:  &InspectedMetadata{Format: "uki", Version: version},
		},
	}
}

func TestCheckSnapshot_UKILayout_Fresh(t *testing.T) {
	tmpDir := t.TempDir()
	setupModulesDir(t, tmpDir, "6.19.0-uki")

	bs := makeUKIBootSet("6.19.0-uki")
	checker := NewChecker(ActionDelete)
	result := checker.CheckSnapshot(tmpDir, bs)

	assert.False(t, result.IsStale, "matching .uname version must mark snapshot fresh")
	assert.Equal(t, MatchBinaryHeader, result.Method,
		"UKI version comes from .uname which is the binary-header equivalent")
	assert.Equal(t, "6.19.0-uki", result.ExpectedVersion)
}

func TestCheckSnapshot_UKILayout_StaleVersionMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	setupModulesDir(t, tmpDir, "6.18.0-old")

	bs := makeUKIBootSet("6.19.0-uki")
	checker := NewChecker(ActionDelete)
	result := checker.CheckSnapshot(tmpDir, bs)

	assert.True(t, result.IsStale, "snapshot modules don't match UKI's .uname → stale")
	assert.Equal(t, ReasonModulesMissing, result.Reason)
	assert.Equal(t, "6.19.0-uki", result.ExpectedVersion)
}

func TestCheckSnapshot_UKILayout_NoModulesDir(t *testing.T) {
	tmpDir := t.TempDir()
	// Snapshot has no /lib/modules at all.

	bs := makeUKIBootSet("6.19.0-uki")
	checker := NewChecker(ActionDelete)
	result := checker.CheckSnapshot(tmpDir, bs)

	assert.True(t, result.IsStale)
	assert.Equal(t, ReasonNoModulesDir, result.Reason)
}

func TestCheckSnapshot_UKILayout_NoInspectedMetadata(t *testing.T) {
	// UKI without inspected version → KernelVersion() returns "" → falls through
	// to pkgbase matching like split-layout sets without inspection.
	tmpDir := t.TempDir()
	setupModulesDir(t, tmpDir, "6.19.0-uki")
	// Plant a pkgbase file matching the kernel name.
	pkgbasePath := filepath.Join(tmpDir, "lib", "modules", "6.19.0-uki", "pkgbase")
	require.NoError(t, os.WriteFile(pkgbasePath, []byte("linux\n"), 0o644))

	bs := &BootSet{
		KernelName: "linux",
		Layout:     LayoutUKI,
		UKI: &BootImage{
			Filename:   "linux.efi",
			Role:       RoleUKI,
			KernelName: "linux",
			// No Inspected — version unknown
		},
	}

	checker := NewChecker(ActionDelete)
	result := checker.CheckSnapshot(tmpDir, bs)

	assert.False(t, result.IsStale, "pkgbase match should succeed when binary version unavailable")
	assert.Equal(t, MatchPkgbase, result.Method)
}
