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

// ScanDir scans one or more directories for boot images matching the
// configured patterns. Each file is tested against patterns in order;
// the first match wins.
//
// When multiple directories are supplied, results are aggregated. Directories
// that cannot be read (missing, permission denied, etc.) are logged at trace
// level and skipped rather than aborting the scan. An error is returned only
// when every supplied directory failed to read.
//
// Returns discovered images sorted by role (kernels first, then UKI,
// initramfs, fallback, microcode).
func (s *Scanner) ScanDir(dirs ...string) ([]*BootImage, error) {
	if len(dirs) == 0 {
		return nil, nil
	}

	var images []*BootImage
	var lastErr error
	succeeded := 0

	for _, dir := range dirs {
		log.Debug().Str("dir", dir).Msg("Scanning directory for boot images")

		entries, err := os.ReadDir(dir)
		if err != nil {
			log.Trace().Err(err).Str("dir", dir).Msg("Skipping unreadable directory")
			lastErr = err
			continue
		}
		succeeded++

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
					break
				}
			}

			if !matched {
				log.Trace().Str("file", filename).Msg("No pattern matched, skipping")
			}
		}
	}

	if succeeded == 0 && lastErr != nil {
		return nil, lastErr
	}

	slices.SortFunc(images, func(a, b *BootImage) int {
		return cmp.Compare(roleOrder[a.Role], roleOrder[b.Role])
	})

	counts := make(map[ImageRole]int)
	for _, img := range images {
		counts[img.Role]++
	}
	log.Info().
		Int("kernels", counts[RoleKernel]).
		Int("uki", counts[RoleUKI]).
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
		meta, err := Inspect(img.AbsPath, img.Role)
		if err != nil {
			log.Warn().Err(err).
				Str("path", img.AbsPath).
				Str("filename", img.Filename).
				Str("role", string(img.Role)).
				Msg("Could not inspect boot image, falling back to filename-only detection")
			continue
		}
		if meta == nil {
			log.Trace().Str("filename", img.Filename).Str("role", string(img.Role)).Msg("Skipping inspection for this role")
			continue
		}
		img.Inspected = meta
		inspected++
		log.Debug().
			Str("filename", img.Filename).
			Str("role", string(img.Role)).
			Str("version", meta.Version).
			Str("format", meta.Format).
			Msg("Boot image inspected successfully")
	}

	log.Info().
		Int("inspected", inspected).
		Int("total", len(images)).
		Msg("Boot image inspection complete")
}

// bootSetKey identifies a unique BootSet by kernel name and layout, so a
// kernel that exists in multiple layouts (e.g. vmlinuz-X plus an X.efi UKI)
// is reported as two distinct sets.
type bootSetKey struct {
	name   string
	layout BootLayout
}

// BuildBootSets groups discovered images into BootSets keyed by (kernel name, layout).
// Microcode images (which have no KernelName) are shared across all sets.
// UKIs produce their own LayoutUKI set per kernel name.
// Logs warnings for orphaned images (initramfs without kernel, etc.).
func (s *Scanner) BuildBootSets(images []*BootImage) []*BootSet {
	setMap := make(map[bootSetKey]*BootSet)
	var microcode []*BootImage

	for _, img := range images {
		if img.Role == RoleMicrocode {
			microcode = append(microcode, img)
			log.Debug().Str("filename", img.Filename).Msg("Microcode image will be shared across all boot sets")
			continue
		}

		if img.KernelName == "" {
			log.Warn().
				Str("filename", img.Filename).
				Str("role", string(img.Role)).
				Msg("Boot image has no kernel name, skipping")
			continue
		}

		layout := layoutForRole(img.Role)
		key := bootSetKey{name: img.KernelName, layout: layout}
		bs, exists := setMap[key]
		if !exists {
			bs = &BootSet{KernelName: img.KernelName, Layout: layout}
			setMap[key] = bs
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
		case RoleUKI:
			if bs.UKI != nil {
				log.Warn().
					Str("kernel_name", img.KernelName).
					Str("existing", bs.UKI.Filename).
					Str("duplicate", img.Filename).
					Msg("Duplicate UKI for same kernel name, keeping first")
			} else {
				bs.UKI = img
			}
		}
	}

	keys := make([]bootSetKey, 0, len(setMap))
	for k := range setMap {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b bootSetKey) int {
		if c := cmp.Compare(a.name, b.name); c != 0 {
			return c
		}
		return cmp.Compare(string(a.layout), string(b.layout))
	})

	sets := make([]*BootSet, 0, len(keys))
	for _, k := range keys {
		bs := setMap[k]
		bs.Microcode = microcode

		switch bs.Layout {
		case LayoutSplit, LayoutBLS:
			if bs.Kernel == nil {
				log.Warn().
					Str("kernel_name", k.name).
					Str("layout", string(bs.Layout)).
					Msg("Boot set has no kernel image (orphaned initramfs/fallback)")
			}
			if bs.Initramfs == nil && bs.Kernel != nil {
				log.Warn().
					Str("kernel_name", k.name).
					Str("layout", string(bs.Layout)).
					Msg("Boot set has kernel but no initramfs")
			}
		case LayoutUKI:
			if bs.UKI == nil {
				log.Warn().
					Str("kernel_name", k.name).
					Msg("UKI boot set is missing its UKI image")
			}
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
			Str("kernel_name", k.name).
			Str("layout", string(bs.Layout)).
			Str("version", version).
			Str("has_fallback", hasFallback).
			Int("microcode_count", len(bs.Microcode)).
			Msg("Boot set assembled")

		sets = append(sets, bs)
	}

	return sets
}

// layoutForRole maps an ImageRole to the BootLayout that role implies when
// found on disk. Split and BLS share the same image roles; the BLS scanner
// re-labels matching sets after parsing /loader/entries/*.conf.
func layoutForRole(r ImageRole) BootLayout {
	if r == RoleUKI {
		return LayoutUKI
	}
	return LayoutSplit
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
