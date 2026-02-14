package kernel

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
)

// GetSnapshotModuleVersions lists kernel module versions available in a snapshot.
// It reads directory names from <snapshotFSPath>/lib/modules/, filtering out
// non-directory entries and special directories like "extramodules-*".
//
// Returns an empty slice (not error) if /lib/modules/ does not exist, since
// some snapshots may legitimately not contain module directories.
func GetSnapshotModuleVersions(snapshotFSPath string) []string {
	modulesDir := filepath.Join(snapshotFSPath, "lib", "modules")

	entries, err := os.ReadDir(modulesDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Debug().
				Str("path", modulesDir).
				Msg("No /lib/modules directory in snapshot")
			return nil
		}
		log.Warn().Err(err).
			Str("path", modulesDir).
			Msg("Failed to read /lib/modules directory in snapshot")
		return nil
	}

	var versions []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()

		// Filter out special directories that aren't kernel module versions
		if strings.HasPrefix(name, "extramodules-") {
			log.Trace().Str("dir", name).Msg("Skipping extramodules directory")
			continue
		}

		versions = append(versions, name)
	}

	log.Debug().
		Str("snapshot", snapshotFSPath).
		Strs("versions", versions).
		Msg("Found kernel module versions in snapshot")

	return versions
}

// ReadPkgbase reads the pkgbase file from a kernel module directory.
// On Arch-based systems, /lib/modules/<version>/pkgbase contains the kernel
// package name (e.g., "linux", "linux-lts", "linux-cachyos").
//
// This provides the mapping between a module version directory and the kernel
// package it belongs to, which is critical for matching modules to boot images.
//
// Returns empty string if the file doesn't exist (non-Arch systems, etc.).
func ReadPkgbase(snapshotFSPath string, moduleVersion string) string {
	pkgbasePath := filepath.Join(snapshotFSPath, "lib", "modules", moduleVersion, "pkgbase")

	data, err := os.ReadFile(pkgbasePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Trace().
				Str("path", pkgbasePath).
				Msg("No pkgbase file (non-Arch system or missing file)")
			return ""
		}
		log.Warn().Err(err).
			Str("path", pkgbasePath).
			Msg("Failed to read pkgbase file")
		return ""
	}

	pkgbase := strings.TrimSpace(string(data))

	log.Trace().
		Str("module_version", moduleVersion).
		Str("pkgbase", pkgbase).
		Msg("Read pkgbase from module directory")

	return pkgbase
}
