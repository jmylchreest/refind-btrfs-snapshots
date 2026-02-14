package kernel

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createSnapshotModules creates a fake /lib/modules/ structure in a temp dir.
func createSnapshotModules(t *testing.T, versions []string, pkgbases map[string]string) string {
	t.Helper()
	root := t.TempDir()
	modulesDir := filepath.Join(root, "lib", "modules")
	require.NoError(t, os.MkdirAll(modulesDir, 0755))

	for _, ver := range versions {
		verDir := filepath.Join(modulesDir, ver)
		require.NoError(t, os.MkdirAll(verDir, 0755))

		if pkgbase, ok := pkgbases[ver]; ok {
			require.NoError(t, os.WriteFile(filepath.Join(verDir, "pkgbase"), []byte(pkgbase), 0644))
		}
	}

	return root
}

func TestGetSnapshotModuleVersions_Single(t *testing.T) {
	root := createSnapshotModules(t, []string{"6.19.0-2-cachyos"}, nil)
	versions := GetSnapshotModuleVersions(root)
	assert.Equal(t, []string{"6.19.0-2-cachyos"}, versions)
}

func TestGetSnapshotModuleVersions_Multiple(t *testing.T) {
	root := createSnapshotModules(t, []string{"6.12.9-arch1-1", "6.12.10-arch1-1"}, nil)
	versions := GetSnapshotModuleVersions(root)
	assert.Len(t, versions, 2)
	assert.Contains(t, versions, "6.12.9-arch1-1")
	assert.Contains(t, versions, "6.12.10-arch1-1")
}

func TestGetSnapshotModuleVersions_Empty(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "lib", "modules"), 0755))

	versions := GetSnapshotModuleVersions(root)
	assert.Empty(t, versions)
}

func TestGetSnapshotModuleVersions_NoModulesDir(t *testing.T) {
	root := t.TempDir()
	// No /lib/modules/ at all
	versions := GetSnapshotModuleVersions(root)
	assert.Nil(t, versions)
}

func TestGetSnapshotModuleVersions_FilesIgnored(t *testing.T) {
	root := t.TempDir()
	modulesDir := filepath.Join(root, "lib", "modules")
	require.NoError(t, os.MkdirAll(modulesDir, 0755))

	// Create a directory (should be included)
	require.NoError(t, os.MkdirAll(filepath.Join(modulesDir, "6.19.0-2-cachyos"), 0755))
	// Create a regular file (should be ignored)
	require.NoError(t, os.WriteFile(filepath.Join(modulesDir, "somefile.txt"), []byte("test"), 0644))

	versions := GetSnapshotModuleVersions(root)
	assert.Equal(t, []string{"6.19.0-2-cachyos"}, versions)
}

func TestGetSnapshotModuleVersions_ExtramodulesFiltered(t *testing.T) {
	root := t.TempDir()
	modulesDir := filepath.Join(root, "lib", "modules")
	require.NoError(t, os.MkdirAll(modulesDir, 0755))

	require.NoError(t, os.MkdirAll(filepath.Join(modulesDir, "6.19.0-2-cachyos"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(modulesDir, "extramodules-6.19-cachyos"), 0755))

	versions := GetSnapshotModuleVersions(root)
	assert.Equal(t, []string{"6.19.0-2-cachyos"}, versions)
}

// --- ReadPkgbase tests ---

func TestReadPkgbase_Found(t *testing.T) {
	root := createSnapshotModules(t, []string{"6.19.0-2-cachyos"}, map[string]string{
		"6.19.0-2-cachyos": "linux-cachyos",
	})

	pkgbase := ReadPkgbase(root, "6.19.0-2-cachyos")
	assert.Equal(t, "linux-cachyos", pkgbase)
}

func TestReadPkgbase_NotFound(t *testing.T) {
	root := createSnapshotModules(t, []string{"6.19.0-2-cachyos"}, nil)

	pkgbase := ReadPkgbase(root, "6.19.0-2-cachyos")
	assert.Equal(t, "", pkgbase)
}

func TestReadPkgbase_WhitespaceHandled(t *testing.T) {
	root := createSnapshotModules(t, []string{"6.19.0"}, nil)
	// Write pkgbase with trailing newline
	pkgbasePath := filepath.Join(root, "lib", "modules", "6.19.0", "pkgbase")
	require.NoError(t, os.WriteFile(pkgbasePath, []byte("linux-lts\n"), 0644))

	pkgbase := ReadPkgbase(root, "6.19.0")
	assert.Equal(t, "linux-lts", pkgbase)
}

func TestReadPkgbase_EmptyFile(t *testing.T) {
	root := createSnapshotModules(t, []string{"6.19.0"}, nil)
	pkgbasePath := filepath.Join(root, "lib", "modules", "6.19.0", "pkgbase")
	require.NoError(t, os.WriteFile(pkgbasePath, []byte(""), 0644))

	pkgbase := ReadPkgbase(root, "6.19.0")
	assert.Equal(t, "", pkgbase)
}

func TestReadPkgbase_NonexistentModuleDir(t *testing.T) {
	root := t.TempDir()
	pkgbase := ReadPkgbase(root, "6.19.0")
	assert.Equal(t, "", pkgbase)
}

func TestReadPkgbase_MultipleKernels(t *testing.T) {
	root := createSnapshotModules(t,
		[]string{"6.12.10-arch1-1", "6.12.10-lts"},
		map[string]string{
			"6.12.10-arch1-1": "linux",
			"6.12.10-lts":     "linux-lts",
		},
	)

	assert.Equal(t, "linux", ReadPkgbase(root, "6.12.10-arch1-1"))
	assert.Equal(t, "linux-lts", ReadPkgbase(root, "6.12.10-lts"))
}
