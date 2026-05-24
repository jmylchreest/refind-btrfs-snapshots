package diff

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/runner"
	"github.com/rs/zerolog/log"
)

// Apply writes every file diff in the patch through the supplied runner,
// creating parent directories as needed. Per-file errors are collected and
// reported in a single joined error so a failure on one file doesn't prevent
// other files from being written.
func Apply(patch *PatchDiff, r runner.Runner) error {
	var errs []error

	for _, fileDiff := range patch.Files {
		if err := r.MkdirAll(filepath.Dir(fileDiff.Path), 0755, fmt.Sprintf("Create directory for %s", fileDiff.Path)); err != nil {
			log.Warn().Err(err).Str("path", fileDiff.Path).Msg("Failed to create directory")
			errs = append(errs, fmt.Errorf("mkdir %s: %w", filepath.Dir(fileDiff.Path), err))
			continue
		}

		if err := r.WriteFile(fileDiff.Path, []byte(fileDiff.Modified), 0644, fmt.Sprintf("Write %s", fileDiff.Path)); err != nil {
			log.Warn().Err(err).Str("path", fileDiff.Path).Msg("Failed to write file")
			errs = append(errs, fmt.Errorf("write %s: %w", fileDiff.Path, err))
			continue
		}

		log.Info().Str("path", fileDiff.Path).Str("type", FileType(fileDiff.Path)).Msg("Successfully updated file")
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to apply %d of %d changes: %w", len(errs), len(patch.Files), errors.Join(errs...))
	}
	return nil
}

// FileType classifies a path for logging — fstab, refind config, refind_linux
// config, refind include file, or unknown.
func FileType(path string) string {
	switch {
	case strings.HasSuffix(path, "/etc/fstab"):
		return "fstab"
	case strings.HasSuffix(path, "refind-btrfs-snapshots.conf"):
		return "refind_include"
	case strings.HasSuffix(path, "refind_linux.conf"):
		return "refind_linux"
	case strings.HasSuffix(path, "refind.conf"):
		return "refind_config"
	case strings.Contains(path, "/EFI/") && strings.HasSuffix(path, ".conf"):
		return "refind_config"
	default:
		return "unknown"
	}
}
