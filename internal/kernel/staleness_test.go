package kernel

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ParseStaleAction tests ---

func TestParseStaleAction_Valid(t *testing.T) {
	tests := []struct {
		input    string
		expected StaleAction
	}{
		{"warn", ActionWarn},
		{"disable", ActionDisable},
		{"delete", ActionDelete},
		{"fallback", ActionFallback},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, ParseStaleAction(tt.input))
		})
	}
}

func TestParseStaleAction_Invalid(t *testing.T) {
	// Should default to ActionWarn
	assert.Equal(t, ActionWarn, ParseStaleAction("bogus"))
	assert.Equal(t, ActionWarn, ParseStaleAction(""))
}

// --- Checker tests ---

func makeBootSet(kernelName string, inspectedVersion string, hasFallback bool) *BootSet {
	bs := &BootSet{
		KernelName: kernelName,
		Kernel: &BootImage{
			Filename:   "vmlinuz-" + kernelName,
			KernelName: kernelName,
		},
		Initramfs: &BootImage{
			Filename:   "initramfs-" + kernelName + ".img",
			KernelName: kernelName,
		},
	}

	if inspectedVersion != "" {
		bs.Kernel.Inspected = &InspectedMetadata{Version: inspectedVersion}
	}

	if hasFallback {
		bs.Fallback = &BootImage{
			Filename:   "initramfs-" + kernelName + "-fallback.img",
			KernelName: kernelName,
		}
	}

	return bs
}

func makeSnapshotWithModules(t *testing.T, versions []string, pkgbases map[string]string) string {
	t.Helper()
	return createSnapshotModules(t, versions, pkgbases)
}

// --- Fresh (not stale) tests ---

func TestCheckSnapshot_Fresh_BinaryHeaderMatch(t *testing.T) {
	snapshotFS := makeSnapshotWithModules(t, []string{"6.19.0-2-cachyos"}, nil)
	bootSet := makeBootSet("linux-cachyos", "6.19.0-2-cachyos", false)

	checker := NewChecker(ActionWarn)
	result := checker.CheckSnapshot(snapshotFS, bootSet)

	assert.False(t, result.IsStale)
	assert.Equal(t, MatchBinaryHeader, result.Method)
	assert.Contains(t, result.SnapshotModules, "6.19.0-2-cachyos")
}

func TestCheckSnapshot_Fresh_MultipleModulesOneMatches(t *testing.T) {
	snapshotFS := makeSnapshotWithModules(t, []string{"6.12.9-arch1-1", "6.19.0-2-cachyos"}, nil)
	bootSet := makeBootSet("linux-cachyos", "6.19.0-2-cachyos", false)

	checker := NewChecker(ActionWarn)
	result := checker.CheckSnapshot(snapshotFS, bootSet)

	assert.False(t, result.IsStale)
	assert.Equal(t, MatchBinaryHeader, result.Method)
}

func TestCheckSnapshot_Fresh_PkgbaseMatch(t *testing.T) {
	snapshotFS := makeSnapshotWithModules(t,
		[]string{"6.19.0-2-cachyos"},
		map[string]string{"6.19.0-2-cachyos": "linux-cachyos"},
	)
	// No inspected version — forces pkgbase path
	bootSet := makeBootSet("linux-cachyos", "", false)

	checker := NewChecker(ActionWarn)
	result := checker.CheckSnapshot(snapshotFS, bootSet)

	assert.False(t, result.IsStale)
	assert.Equal(t, MatchPkgbase, result.Method)
}

func TestCheckSnapshot_Fresh_AssumedFresh(t *testing.T) {
	// Modules exist but no inspected version and no pkgbase → assume fresh
	snapshotFS := makeSnapshotWithModules(t, []string{"6.19.0-2-cachyos"}, nil)
	bootSet := makeBootSet("linux", "", false) // different kernel name, no version

	checker := NewChecker(ActionWarn)
	result := checker.CheckSnapshot(snapshotFS, bootSet)

	assert.False(t, result.IsStale)
	assert.Equal(t, MatchAssumedFresh, result.Method)
	assert.NotEmpty(t, result.Warning)
}

// --- Stale tests ---

func TestCheckSnapshot_Stale_VersionMismatch(t *testing.T) {
	snapshotFS := makeSnapshotWithModules(t, []string{"6.12.9-arch1-1"}, nil)
	bootSet := makeBootSet("linux", "6.12.10-arch1-1", false)

	checker := NewChecker(ActionWarn)
	result := checker.CheckSnapshot(snapshotFS, bootSet)

	assert.True(t, result.IsStale)
	assert.Equal(t, ReasonModulesMissing, result.Reason)
	assert.Equal(t, MatchBinaryHeader, result.Method)
	assert.Equal(t, "6.12.10-arch1-1", result.ExpectedVersion)
	assert.Contains(t, result.SnapshotModules, "6.12.9-arch1-1")
}

func TestCheckSnapshot_Stale_NoModulesDir(t *testing.T) {
	snapshotFS := t.TempDir() // no /lib/modules/
	bootSet := makeBootSet("linux", "6.19.0", false)

	checker := NewChecker(ActionWarn)
	result := checker.CheckSnapshot(snapshotFS, bootSet)

	assert.True(t, result.IsStale)
	assert.Equal(t, ReasonNoModulesDir, result.Reason)
}

func TestCheckSnapshot_Stale_EmptyModulesDir(t *testing.T) {
	snapshotFS := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(snapshotFS, "lib", "modules"), 0755))

	bootSet := makeBootSet("linux", "6.19.0", false)

	checker := NewChecker(ActionWarn)
	result := checker.CheckSnapshot(snapshotFS, bootSet)

	assert.True(t, result.IsStale)
	assert.Equal(t, ReasonNoModulesDir, result.Reason)
}

// --- ResolveAction tests ---

func TestResolveAction_Warn(t *testing.T) {
	snapshotFS := makeSnapshotWithModules(t, []string{"6.12.9"}, nil)
	bootSet := makeBootSet("linux", "6.12.10", false)

	checker := NewChecker(ActionWarn)
	result := checker.CheckSnapshot(snapshotFS, bootSet)

	assert.True(t, result.IsStale)
	assert.Equal(t, ActionWarn, result.Action)
}

func TestResolveAction_Disable(t *testing.T) {
	snapshotFS := makeSnapshotWithModules(t, []string{"6.12.9"}, nil)
	bootSet := makeBootSet("linux", "6.12.10", false)

	checker := NewChecker(ActionDisable)
	result := checker.CheckSnapshot(snapshotFS, bootSet)

	assert.True(t, result.IsStale)
	assert.Equal(t, ActionDisable, result.Action)
}

func TestResolveAction_Delete(t *testing.T) {
	snapshotFS := makeSnapshotWithModules(t, []string{"6.12.9"}, nil)
	bootSet := makeBootSet("linux", "6.12.10", false)

	checker := NewChecker(ActionDelete)
	result := checker.CheckSnapshot(snapshotFS, bootSet)

	assert.True(t, result.IsStale)
	assert.Equal(t, ActionDelete, result.Action)
}

func TestResolveAction_FallbackExists(t *testing.T) {
	snapshotFS := makeSnapshotWithModules(t, []string{"6.12.9"}, nil)
	bootSet := makeBootSet("linux", "6.12.10", true) // has fallback

	checker := NewChecker(ActionFallback)
	result := checker.CheckSnapshot(snapshotFS, bootSet)

	assert.True(t, result.IsStale)
	assert.Equal(t, ActionFallback, result.Action)
	assert.True(t, result.FallbackUsed)
}

func TestResolveAction_FallbackMissing_DowngradesToDisable(t *testing.T) {
	snapshotFS := makeSnapshotWithModules(t, []string{"6.12.9"}, nil)
	bootSet := makeBootSet("linux", "6.12.10", false) // no fallback

	checker := NewChecker(ActionFallback)
	result := checker.CheckSnapshot(snapshotFS, bootSet)

	assert.True(t, result.IsStale)
	assert.Equal(t, ActionDisable, result.Action) // downgraded
	assert.False(t, result.FallbackUsed)
	assert.Contains(t, result.Warning, "fallback initramfs not available")
}

func TestResolveAction_NotStale_NoAction(t *testing.T) {
	snapshotFS := makeSnapshotWithModules(t, []string{"6.12.10"}, nil)
	bootSet := makeBootSet("linux", "6.12.10", false)

	checker := NewChecker(ActionDelete) // even with delete action, not stale = no action
	result := checker.CheckSnapshot(snapshotFS, bootSet)

	assert.False(t, result.IsStale)
	assert.Equal(t, StaleAction(""), result.Action)
}

// --- MatchMethod preference tests ---

func TestMatchMethod_PrefersBinaryHeader(t *testing.T) {
	// Both version and pkgbase available — should use binary_header
	snapshotFS := makeSnapshotWithModules(t,
		[]string{"6.19.0-2-cachyos"},
		map[string]string{"6.19.0-2-cachyos": "linux-cachyos"},
	)
	bootSet := makeBootSet("linux-cachyos", "6.19.0-2-cachyos", false)

	checker := NewChecker(ActionWarn)
	result := checker.CheckSnapshot(snapshotFS, bootSet)

	assert.False(t, result.IsStale)
	assert.Equal(t, MatchBinaryHeader, result.Method)
}

func TestMatchMethod_FallsToPkgbase(t *testing.T) {
	snapshotFS := makeSnapshotWithModules(t,
		[]string{"6.19.0-2-cachyos"},
		map[string]string{"6.19.0-2-cachyos": "linux-cachyos"},
	)
	// No inspected version
	bootSet := makeBootSet("linux-cachyos", "", false)

	checker := NewChecker(ActionWarn)
	result := checker.CheckSnapshot(snapshotFS, bootSet)

	assert.False(t, result.IsStale)
	assert.Equal(t, MatchPkgbase, result.Method)
}

func TestMatchMethod_FallsToAssumedFresh(t *testing.T) {
	snapshotFS := makeSnapshotWithModules(t, []string{"6.19.0-2-cachyos"}, nil)
	// No inspected version, no pkgbase, different kernel name
	bootSet := makeBootSet("linux", "", false)

	checker := NewChecker(ActionWarn)
	result := checker.CheckSnapshot(snapshotFS, bootSet)

	assert.False(t, result.IsStale)
	assert.Equal(t, MatchAssumedFresh, result.Method)
}
