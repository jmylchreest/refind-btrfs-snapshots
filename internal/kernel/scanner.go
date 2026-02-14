package kernel

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rs/zerolog/log"
)

// Scanner discovers boot images in a directory using configurable patterns.
type Scanner struct {
	patterns []PatternConfig
	espPath  string
}

// NewScanner creates a new Scanner with the given ESP path and patterns.
// If patterns is nil or empty, DefaultPatterns() is used.
func NewScanner(espPath string, patterns []PatternConfig) *Scanner {
	if len(patterns) == 0 {
		patterns = DefaultPatterns()
		log.Debug().Int("count", len(patterns)).Msg("Using default boot image patterns")
	} else {
		log.Debug().Int("count", len(patterns)).Msg("Using configured boot image patterns")
	}

	return &Scanner{
		patterns: patterns,
		espPath:  espPath,
	}
}

// ScanDir scans a directory for boot images matching the configured patterns.
// Each file is tested against patterns in order; the first match wins.
// Returns discovered images sorted by role (kernels first, then initramfs, fallback, microcode).
func (s *Scanner) ScanDir(dir string) ([]*BootImage, error) {
	log.Debug().Str("dir", dir).Msg("Scanning directory for boot images")

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var images []*BootImage

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		matched := false

		for _, pattern := range s.patterns {
			ok, err := filepath.Match(pattern.Glob, filename)
			if err != nil {
				log.Warn().Err(err).Str("glob", pattern.Glob).Msg("Invalid glob pattern, skipping")
				continue
			}

			if ok {
				absPath := filepath.Join(dir, filename)
				espRelPath := s.espRelativePath(absPath)

				kernelName := pattern.DeriveKernelName(filename)

				log.Trace().
					Str("file", filename).
					Str("glob", pattern.Glob).
					Str("role", string(pattern.Role)).
					Str("kernel_name", kernelName).
					Msg("Boot image matched pattern")

				images = append(images, &BootImage{
					Path:       espRelPath,
					AbsPath:    absPath,
					Filename:   filename,
					Role:       pattern.Role,
					KernelName: kernelName,
				})

				matched = true
				break // first match wins
			}
		}

		if !matched {
			log.Trace().Str("file", filename).Msg("No pattern matched, skipping")
		}
	}

	// Sort for deterministic output: kernels first, then initramfs, fallback, microcode
	slices.SortFunc(images, func(a, b *BootImage) int {
		return cmp.Compare(roleOrder[a.Role], roleOrder[b.Role])
	})

	// Log summary
	counts := make(map[ImageRole]int)
	for _, img := range images {
		counts[img.Role]++
	}
	log.Info().
		Int("kernels", counts[RoleKernel]).
		Int("initramfs", counts[RoleInitramfs]).
		Int("fallback", counts[RoleFallbackInitramfs]).
		Int("microcode", counts[RoleMicrocode]).
		Int("total", len(images)).
		Msg("Boot image scan complete")

	return images, nil
}

// InspectAll attempts binary inspection on each discovered image.
// Inspection failures are logged as warnings; the image remains usable
// with filename-derived metadata only (Inspected will be nil).
func (s *Scanner) InspectAll(images []*BootImage) {
	inspected := 0

	for _, img := range images {
		switch img.Role {
		case RoleKernel:
			meta, err := InspectKernel(img.AbsPath)
			if err != nil {
				log.Warn().Err(err).
					Str("path", img.AbsPath).
					Str("filename", img.Filename).
					Msg("Could not inspect kernel binary, falling back to filename-only detection")
			} else {
				img.Inspected = meta
				inspected++
				log.Debug().
					Str("filename", img.Filename).
					Str("version", meta.Version).
					Str("protocol", meta.BootProtocol).
					Msg("Kernel binary inspected successfully")
			}

		case RoleInitramfs, RoleFallbackInitramfs:
			meta, err := InspectInitramfs(img.AbsPath)
			if err != nil {
				log.Warn().Err(err).
					Str("path", img.AbsPath).
					Str("filename", img.Filename).
					Msg("Could not inspect initramfs, falling back to filename-only detection")
			} else {
				img.Inspected = meta
				inspected++
				log.Debug().
					Str("filename", img.Filename).
					Str("compress_format", meta.CompressFormat).
					Msg("Initramfs inspected successfully")
			}

		case RoleMicrocode:
			// No binary inspection for microcode images
			log.Trace().Str("filename", img.Filename).Msg("Skipping inspection for microcode image")
		}
	}

	log.Info().
		Int("inspected", inspected).
		Int("total", len(images)).
		Msg("Boot image inspection complete")
}

// BuildBootSets groups discovered images into BootSets by KernelName.
// Microcode images (which have no KernelName) are shared across all sets.
// Logs warnings for orphaned images (initramfs without kernel, etc.).
func (s *Scanner) BuildBootSets(images []*BootImage) []*BootSet {
	setMap := make(map[string]*BootSet)
	var microcode []*BootImage

	for _, img := range images {
		// Microcode is shared across all boot sets
		if img.Role == RoleMicrocode {
			microcode = append(microcode, img)
			log.Debug().Str("filename", img.Filename).Msg("Microcode image will be shared across all boot sets")
			continue
		}

		// Skip images with no kernel name (shouldn't happen except microcode, but be safe)
		if img.KernelName == "" {
			log.Warn().
				Str("filename", img.Filename).
				Str("role", string(img.Role)).
				Msg("Boot image has no kernel name, skipping")
			continue
		}

		bs, exists := setMap[img.KernelName]
		if !exists {
			bs = &BootSet{KernelName: img.KernelName}
			setMap[img.KernelName] = bs
		}

		switch img.Role {
		case RoleKernel:
			if bs.Kernel != nil {
				log.Warn().
					Str("kernel_name", img.KernelName).
					Str("existing", bs.Kernel.Filename).
					Str("duplicate", img.Filename).
					Msg("Duplicate kernel for same kernel name, keeping first")
			} else {
				bs.Kernel = img
			}
		case RoleInitramfs:
			if bs.Initramfs != nil {
				log.Warn().
					Str("kernel_name", img.KernelName).
					Str("existing", bs.Initramfs.Filename).
					Str("duplicate", img.Filename).
					Msg("Duplicate initramfs for same kernel name, keeping first")
			} else {
				bs.Initramfs = img
			}
		case RoleFallbackInitramfs:
			if bs.Fallback != nil {
				log.Warn().
					Str("kernel_name", img.KernelName).
					Str("existing", bs.Fallback.Filename).
					Str("duplicate", img.Filename).
					Msg("Duplicate fallback initramfs for same kernel name, keeping first")
			} else {
				bs.Fallback = img
			}
		}
	}

	// Attach microcode to all boot sets and collect into sorted slice
	var sets []*BootSet
	// Sort by kernel name for deterministic output
	var names []string
	for name := range setMap {
		names = append(names, name)
	}
	slices.Sort(names)

	for _, name := range names {
		bs := setMap[name]
		bs.Microcode = microcode

		// Log warnings for incomplete boot sets
		if bs.Kernel == nil {
			log.Warn().
				Str("kernel_name", name).
				Msg("Boot set has no kernel image (orphaned initramfs/fallback)")
		}
		if bs.Initramfs == nil && bs.Kernel != nil {
			log.Warn().
				Str("kernel_name", name).
				Msg("Boot set has kernel but no initramfs")
		}

		hasFallback := "no"
		if bs.HasFallback() {
			hasFallback = "yes"
		}

		version := bs.KernelVersion()
		if version == "" {
			version = "(not inspected)"
		}

		log.Info().
			Str("kernel_name", name).
			Str("version", version).
			Str("has_fallback", hasFallback).
			Int("microcode_count", len(bs.Microcode)).
			Msg("Boot set assembled")

		sets = append(sets, bs)
	}

	return sets
}

// espRelativePath converts an absolute path to an ESP-relative path.
// e.g., /boot/efi/boot/vmlinuz-linux -> /boot/vmlinuz-linux
func (s *Scanner) espRelativePath(absPath string) string {
	if s.espPath == "" {
		return absPath
	}

	rel, err := filepath.Rel(s.espPath, absPath)
	if err != nil {
		return absPath
	}

	return "/" + strings.ReplaceAll(rel, "\\", "/")
}
