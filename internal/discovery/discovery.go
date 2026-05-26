// Package discovery wires together ESP detection and boot-image scanning
// without depending on the CLI's config package. It is the shared backbone
// for any binary that needs to enumerate kernels, initramfs, UKIs, and
// BLS entries on the live system.
package discovery

import (
	"fmt"
	"path/filepath"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/esp"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/rs/zerolog/log"
)

// ESPOptions controls how the ESP mount point is resolved.
// Precedence: UUID > AutoDetect > MountPoint.
type ESPOptions struct {
	// UUID, if set, locates the ESP by filesystem UUID.
	UUID string
	// AutoDetect, when true, asks esp.Detector to scan block devices.
	AutoDetect bool
	// MountPoint is a literal fallback path (e.g. "/boot"). Only consulted
	// when both UUID and AutoDetect are empty/false.
	MountPoint string
}

// ResolveESP returns the mounted, validated ESP path according to opts.
// Returns an error when no option produces a valid path.
func ResolveESP(opts ESPOptions) (string, error) {
	detector := esp.NewESPDetector(opts.UUID)

	if opts.UUID != "" {
		detected, err := detector.FindESP()
		if err != nil {
			return "", fmt.Errorf("failed to find ESP by UUID %s: %w", opts.UUID, err)
		}
		if detected.MountPoint == "" {
			return "", fmt.Errorf("ESP with UUID %s is not mounted", opts.UUID)
		}
		log.Info().Str("path", detected.MountPoint).Str("uuid", opts.UUID).Msg("Found ESP by UUID")
		if err := detector.ValidateESPPath(detected.MountPoint); err != nil {
			return "", fmt.Errorf("ESP validation failed: %w", err)
		}
		return detected.MountPoint, nil
	}

	if opts.AutoDetect {
		detected, err := detector.FindESP()
		if err != nil {
			return "", fmt.Errorf("failed to detect ESP: %w", err)
		}
		if detected.MountPoint == "" {
			return "", fmt.Errorf("ESP is not mounted")
		}
		log.Info().Str("path", detected.MountPoint).Msg("Auto-detected ESP path")
		if err := detector.ValidateESPPath(detected.MountPoint); err != nil {
			return "", fmt.Errorf("ESP validation failed: %w", err)
		}
		return detected.MountPoint, nil
	}

	if mp := opts.MountPoint; mp != "" {
		log.Info().Str("path", mp).Msg("Using configured ESP path")
		fallback := esp.NewESPDetector("")
		if err := fallback.ValidateESPPath(mp); err != nil {
			return "", fmt.Errorf("ESP validation failed: %w", err)
		}
		return mp, nil
	}

	return "", fmt.Errorf("ESP path not configured and auto-detection disabled")
}

// StandardScanDirs returns the canonical ESP-relative locations to scan
// for boot images: <esp>/boot, <esp>/EFI/Linux, and <esp> itself.
func StandardScanDirs(espPath string) []string {
	return []string{
		filepath.Join(espPath, "boot"),
		filepath.Join(espPath, "EFI", "Linux"),
		espPath,
	}
}

// ScanBootImages walks the standard ESP scan dirs with scanner and returns
// the aggregated list of discovered images. Empty/unreadable dirs are
// silently skipped by the underlying variadic ScanDir.
func ScanBootImages(scanner *kernel.Scanner, espPath string) []*kernel.BootImage {
	dirs := StandardScanDirs(espPath)
	images, err := scanner.ScanDir(dirs...)
	if err != nil {
		log.Trace().Err(err).Strs("dirs", dirs).Msg("No boot images found")
		return nil
	}
	if len(images) > 0 {
		log.Debug().Int("count", len(images)).Strs("dirs", dirs).Msg("Found boot images")
	}
	return images
}

// DetectBootSets runs the full pipeline: resolve ESP, build scanner, scan,
// inspect, and assemble boot sets. Returns the assembled sets and the
// resolved ESP path so callers can reuse it. Returns (nil, "") on any
// fatal error (ESP not found, no images, etc.) so callers can degrade.
func DetectBootSets(opts ESPOptions, patterns []kernel.PatternConfig) ([]*kernel.BootSet, string) {
	espPath, err := ResolveESP(opts)
	if err != nil {
		log.Debug().Err(err).Msg("Could not detect ESP for boot set discovery")
		return nil, ""
	}

	scanner := kernel.NewScanner(espPath, patterns)
	images := ScanBootImages(scanner, espPath)
	if len(images) == 0 {
		log.Debug().Msg("No boot images found on ESP")
		return nil, espPath
	}

	scanner.InspectAll(images)
	sets := scanner.BuildBootSets(images)

	log.Info().Int("boot_sets", len(sets)).Str("esp", espPath).Msg("Detected boot configurations on ESP")
	return sets, espPath
}
