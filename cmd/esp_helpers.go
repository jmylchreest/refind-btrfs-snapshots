// This file is part of refind-btrfs-snapshots.
//
// refind-btrfs-snapshots is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// refind-btrfs-snapshots is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with refind-btrfs-snapshots. If not, see <https://www.gnu.org/licenses/>.

package cmd

import (
	"slices"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/config"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/discovery"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/rs/zerolog/log"
)

// espOptionsFromConfig translates the CLI config's ESP block into the
// primitive options struct accepted by the discovery package.
func espOptionsFromConfig(cfg *config.Config) discovery.ESPOptions {
	return discovery.ESPOptions{
		UUID:       cfg.ESP.UUID,
		AutoDetect: cfg.ESP.AutoDetect.IsTrue(),
		MountPoint: cfg.ESP.MountPoint,
	}
}

// detectESPPath resolves the ESP mount point from config (uuid > auto_detect > mount_point).
func detectESPPath(cfg *config.Config) (string, error) {
	return discovery.ResolveESP(espOptionsFromConfig(cfg))
}

// buildKernelScanner creates a kernel.Scanner from config, using custom patterns
// if configured or built-in defaults otherwise.
func buildKernelScanner(espPath string, cfgPatterns []config.PatternConfig) *kernel.Scanner {
	return kernel.NewScanner(espPath, kernelPatternsFromConfig(cfgPatterns))
}

// kernelPatternsFromConfig converts the CLI config's pattern list into
// the kernel package's PatternConfig, dropping any entries with unknown roles.
func kernelPatternsFromConfig(cfgPatterns []config.PatternConfig) []kernel.PatternConfig {
	var patterns []kernel.PatternConfig
	for _, p := range cfgPatterns {
		role, err := kernel.ParseImageRole(p.Role)
		if err != nil {
			log.Warn().Err(err).Str("glob", p.Glob).Msg("Invalid role in boot_image_patterns, skipping")
			continue
		}
		patterns = append(patterns, kernel.PatternConfig{
			Glob:        p.Glob,
			Role:        role,
			StripPrefix: p.StripPrefix,
			StripSuffix: p.StripSuffix,
			KernelName:  p.KernelName,
		})
	}
	return patterns
}

// scanBootImages discovers all boot images across standard ESP directories.
func scanBootImages(espPath string, scanner *kernel.Scanner) []*kernel.BootImage {
	return discovery.ScanBootImages(scanner, espPath)
}

// detectBootSets is a convenience that detects the ESP, scans for boot images,
// inspects kernels, and returns assembled boot sets. Returns nil on any error
// (ESP not found, no images, etc.) so callers can gracefully degrade.
func detectBootSets(cfg *config.Config) []*kernel.BootSet {
	sets, _ := discovery.DetectBootSets(espOptionsFromConfig(cfg), kernelPatternsFromConfig(cfg.Kernel.BootImagePatterns))
	return sets
}

// discoverSnapshots detects btrfs filesystems, finds snapshots, deduplicates,
// sorts newest-first, and applies the configured selection count.
func discoverSnapshots(cfg *config.Config, searchDirOverrides []string) ([]*btrfs.Snapshot, *btrfs.Manager) {
	searchDirs := cfg.Snapshot.SearchDirectories
	if len(searchDirOverrides) > 0 {
		searchDirs = searchDirOverrides
		log.Debug().Strs("search_dirs", searchDirs).Msg("Using overridden search directories")
	}
	btrfsManager := btrfs.NewManager(searchDirs, cfg.Snapshot.MaxDepth, cfg.Advanced.Naming.RwsnapFormat, cfg.Display.LocalTime.IsTrue())

	filesystems, err := btrfsManager.DetectBtrfsFilesystems()
	if err != nil {
		log.Warn().Err(err).Msg("Could not detect btrfs filesystems")
		return nil, btrfsManager
	}

	var snapshots []*btrfs.Snapshot
	seen := make(map[string]bool)
	for _, fs := range filesystems {
		found, err := btrfsManager.FindSnapshots(fs)
		if err != nil {
			log.Warn().Err(err).Str("fs", fs.GetBestIdentifier()).Msg("Failed to find snapshots")
			continue
		}
		for _, s := range found {
			if !seen[s.Path] {
				seen[s.Path] = true
				snapshots = append(snapshots, s)
			}
		}
	}

	slices.SortFunc(snapshots, func(a, b *btrfs.Snapshot) int {
		return b.SnapshotTime.Compare(a.SnapshotTime)
	})

	if count := cfg.Snapshot.SelectionCount; count > 0 && len(snapshots) > count {
		snapshots = snapshots[:count]
	}

	return snapshots, btrfsManager
}
