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
	"fmt"
	"path/filepath"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/esp"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

// detectESPPath resolves the ESP mount point from config (uuid > auto_detect > mount_point).
func detectESPPath() (string, error) {
	espUUID := viper.GetString("esp.uuid")
	espDetector := esp.NewESPDetector(espUUID)

	if viper.GetBool("esp.auto_detect") {
		detectedESP, err := espDetector.FindESP()
		if err != nil {
			return "", fmt.Errorf("failed to detect ESP: %w", err)
		}
		if detectedESP.MountPoint == "" {
			return "", fmt.Errorf("ESP is not mounted")
		}
		log.Info().Str("path", detectedESP.MountPoint).Msg("Auto-detected ESP path")
		if err := espDetector.ValidateESPPath(detectedESP.MountPoint); err != nil {
			return "", fmt.Errorf("ESP validation failed: %w", err)
		}
		return detectedESP.MountPoint, nil
	}

	if mp := viper.GetString("esp.mount_point"); mp != "" {
		log.Info().Str("path", mp).Msg("Using configured ESP path")
		detector := esp.NewESPDetector("")
		if err := detector.ValidateESPPath(mp); err != nil {
			return "", fmt.Errorf("ESP validation failed: %w", err)
		}
		return mp, nil
	}

	return "", fmt.Errorf("ESP path not configured and auto-detection disabled")
}

// buildKernelScanner creates a kernel.Scanner from config, using custom patterns
// if configured or built-in defaults otherwise.
func buildKernelScanner(espPath string) *kernel.Scanner {
	var bootImagePatterns []kernel.PatternConfig
	if patterns := viper.Get("kernel.boot_image_patterns"); patterns != nil {
		if patternList, ok := patterns.([]interface{}); ok {
			for _, p := range patternList {
				if pm, ok := p.(map[string]interface{}); ok {
					pc := kernel.PatternConfig{}
					if g, ok := pm["glob"].(string); ok {
						pc.Glob = g
					}
					if r, ok := pm["role"].(string); ok {
						role, err := kernel.ParseImageRole(r)
						if err != nil {
							log.Warn().Err(err).Str("glob", pc.Glob).Msg("Invalid role in boot_image_patterns, skipping")
							continue
						}
						pc.Role = role
					}
					if sp, ok := pm["strip_prefix"].(string); ok {
						pc.StripPrefix = sp
					}
					if ss, ok := pm["strip_suffix"].(string); ok {
						pc.StripSuffix = ss
					}
					if kn, ok := pm["kernel_name"].(string); ok {
						pc.KernelName = kn
					}
					bootImagePatterns = append(bootImagePatterns, pc)
				}
			}
		}
	}
	return kernel.NewScanner(espPath, bootImagePatterns)
}

// scanBootImages discovers all boot images across standard ESP directories.
func scanBootImages(espPath string, scanner *kernel.Scanner) []*kernel.BootImage {
	searchDirs := []string{
		filepath.Join(espPath, "boot"),
		filepath.Join(espPath, "EFI", "Linux"),
		espPath,
	}

	var allImages []*kernel.BootImage
	for _, searchDir := range searchDirs {
		images, err := scanner.ScanDir(searchDir)
		if err != nil {
			log.Trace().Err(err).Str("dir", searchDir).Msg("No boot images found in directory")
			continue
		}
		if len(images) > 0 {
			allImages = append(allImages, images...)
			log.Debug().Str("dir", searchDir).Int("count", len(images)).Msg("Found boot images")
		}
	}
	return allImages
}

// detectBootSets is a convenience that detects the ESP, scans for boot images,
// inspects kernels, and returns assembled boot sets. Returns nil on any error
// (ESP not found, no images, etc.) so callers can gracefully degrade.
func detectBootSets() []*kernel.BootSet {
	espPath, err := detectESPPath()
	if err != nil {
		log.Debug().Err(err).Msg("Could not detect ESP for boot set discovery")
		return nil
	}

	scanner := buildKernelScanner(espPath)
	allImages := scanBootImages(espPath, scanner)
	if len(allImages) == 0 {
		log.Debug().Msg("No boot images found on ESP")
		return nil
	}

	scanner.InspectAll(allImages)
	bootSets := scanner.BuildBootSets(allImages)

	log.Info().Int("boot_sets", len(bootSets)).Str("esp", espPath).Msg("Detected boot configurations on ESP")
	return bootSets
}
