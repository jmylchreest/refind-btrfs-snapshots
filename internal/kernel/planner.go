package kernel

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/fstab"
	"github.com/rs/zerolog/log"
)

// BootMode describes how a snapshot's kernel should be loaded.
type BootMode string

const (
	// BootModeESP means the kernel is on the ESP (separate /boot partition).
	// Staleness checks apply because the ESP kernel may not match the
	// snapshot's modules.
	BootModeESP BootMode = "esp"

	// BootModeBtrfs means the kernel is inside the btrfs snapshot itself.
	// The snapshot is always self-consistent — staleness is impossible.
	BootModeBtrfs BootMode = "btrfs"
)

// BootPlan describes how to boot a specific snapshot. Each snapshot gets its
// own BootPlan because the boot mode is determined per-snapshot from its fstab.
type BootPlan struct {
	// Snapshot is the snapshot this plan is for.
	Snapshot *btrfs.Snapshot

	// Mode is the detected boot mode for this snapshot.
	Mode BootMode

	// --- ESP-mode fields (populated when Mode == BootModeESP) ---

	// BootSet is the ESP boot set matched to this snapshot.
	// Nil for btrfs-mode snapshots.
	BootSet *BootSet

	// Staleness is the staleness check result for this snapshot+bootset.
	// Nil for btrfs-mode snapshots.
	Staleness *StalenessResult

	// --- Btrfs-mode fields (populated when Mode == BootModeBtrfs) ---

	// SnapshotKernel is the snapshot-relative path to the kernel image.
	// e.g., "/@/.snapshots/73/snapshot/boot/vmlinuz-linux"
	SnapshotKernel string

	// SnapshotInitrds are the snapshot-relative paths to initramfs images.
	// e.g., ["/@/.snapshots/73/snapshot/boot/initramfs-linux.img"]
	SnapshotInitrds []string

	// BtrfsVolume is the identifier for the btrfs partition to use in
	// rEFInd's "volume" directive (partition label, UUID, etc.).
	BtrfsVolume string
}

// ShouldSkip returns true if this snapshot should be excluded from generation
// (e.g., stale with action=delete in ESP mode).
func (bp *BootPlan) ShouldSkip() bool {
	if bp.Mode == BootModeBtrfs {
		return false // btrfs-mode snapshots are never stale
	}
	return bp.Staleness != nil && bp.Staleness.IsStale && bp.Staleness.Action == ActionDelete
}

// IsStale returns true if this snapshot has staleness issues.
// Always false for btrfs-mode snapshots.
func (bp *BootPlan) IsStale() bool {
	if bp.Mode == BootModeBtrfs {
		return false
	}
	return bp.Staleness != nil && bp.Staleness.IsStale
}

// Planner creates BootPlans for snapshots by inspecting each snapshot's fstab
// to determine whether kernels come from the ESP or from within the snapshot.
type Planner struct {
	fstabManager *fstab.Manager
	checker      *Checker
	bootSets     []*BootSet
	rootFS       *btrfs.Filesystem
}

// NewPlanner creates a Planner.
//
// Parameters:
//   - fstabMgr: used to parse snapshot fstab files
//   - checker: staleness checker for ESP-mode snapshots (may be nil if no boot sets)
//   - bootSets: detected ESP boot sets (may be empty)
//   - rootFS: the root btrfs filesystem (used to determine if /boot is on the same device)
func NewPlanner(fstabMgr *fstab.Manager, checker *Checker, bootSets []*BootSet, rootFS *btrfs.Filesystem) *Planner {
	return &Planner{
		fstabManager: fstabMgr,
		checker:      checker,
		bootSets:     bootSets,
		rootFS:       rootFS,
	}
}

// Plan creates a BootPlan for each snapshot. A snapshot may produce multiple
// BootPlans when in ESP mode with multiple boot sets. In btrfs mode, each
// snapshot produces exactly one plan per detected in-snapshot kernel.
func (p *Planner) Plan(snapshots []*btrfs.Snapshot) []*BootPlan {
	var plans []*BootPlan

	for _, snapshot := range snapshots {
		snapshotPlans := p.planSnapshot(snapshot)
		plans = append(plans, snapshotPlans...)
	}

	return plans
}

// planSnapshot determines the boot mode for a single snapshot and creates
// the appropriate BootPlan(s).
func (p *Planner) planSnapshot(snapshot *btrfs.Snapshot) []*BootPlan {
	bootMountInfo := p.analyzeSnapshotBoot(snapshot)

	if bootMountInfo.BootOnSameBtrfs {
		return p.planBtrfsMode(snapshot)
	}

	return p.planESPMode(snapshot)
}

// analyzeSnapshotBoot parses the snapshot's fstab and determines how /boot
// is mounted. Falls back to ESP mode if the fstab cannot be read.
func (p *Planner) analyzeSnapshotBoot(snapshot *btrfs.Snapshot) *fstab.BootMountInfo {
	fstabPath := btrfs.GetSnapshotFstabPath(snapshot)

	// Check if fstab exists
	if _, err := os.Stat(fstabPath); errors.Is(err, os.ErrNotExist) {
		log.Debug().
			Str("snapshot", snapshot.Path).
			Str("fstab_path", fstabPath).
			Msg("No fstab in snapshot, assuming ESP mode")
		return &fstab.BootMountInfo{
			HasSeparateBootMount: false,
			BootOnSameBtrfs:      false, // conservative: assume ESP if we can't read fstab
		}
	}

	parsed, err := p.fstabManager.ParseFstab(fstabPath)
	if err != nil {
		log.Warn().Err(err).
			Str("snapshot", snapshot.Path).
			Msg("Failed to parse snapshot fstab, assuming ESP mode")
		return &fstab.BootMountInfo{
			HasSeparateBootMount: false,
			BootOnSameBtrfs:      false,
		}
	}

	return p.fstabManager.AnalyzeBootMount(parsed, p.rootFS)
}

// planBtrfsMode creates BootPlans for a snapshot whose /boot is part of the
// btrfs filesystem. It scans for kernel images inside the snapshot.
func (p *Planner) planBtrfsMode(snapshot *btrfs.Snapshot) []*BootPlan {
	// Look for kernel images inside the snapshot's /boot directory
	bootDir := filepath.Join(snapshot.FilesystemPath, "boot")
	kernelImages := findKernelImages(bootDir)

	if len(kernelImages) == 0 {
		log.Warn().
			Str("snapshot", snapshot.Path).
			Str("boot_dir", bootDir).
			Msg("Btrfs-mode snapshot has no kernel images in /boot, falling back to ESP mode")
		// Fall back to ESP mode — snapshot claims /boot is local but
		// no kernels were found (possibly deleted or not yet installed)
		return p.planESPMode(snapshot)
	}

	// Build volume identifier for rEFInd's volume directive
	btrfsVolume := p.buildBtrfsVolume()

	var plans []*BootPlan
	for _, ki := range kernelImages {
		// Build snapshot-subvolume-relative paths for rEFInd
		// rEFInd's btrfs driver sees the filesystem from the root,
		// so paths are like /@/.snapshots/73/snapshot/boot/vmlinuz-linux
		snapshotSubvolPath := snapshot.Path
		if !strings.HasPrefix(snapshotSubvolPath, "/") {
			snapshotSubvolPath = "/" + snapshotSubvolPath
		}

		loaderPath := filepath.Join(snapshotSubvolPath, "boot", ki.kernelFilename)
		// Normalize to forward slashes for rEFInd
		loaderPath = "/" + strings.TrimPrefix(filepath.ToSlash(loaderPath), "/")

		var initrdPaths []string
		for _, initrd := range ki.initrdFilenames {
			initrdPath := filepath.Join(snapshotSubvolPath, "boot", initrd)
			initrdPath = "/" + strings.TrimPrefix(filepath.ToSlash(initrdPath), "/")
			initrdPaths = append(initrdPaths, initrdPath)
		}

		plan := &BootPlan{
			Snapshot:        snapshot,
			Mode:            BootModeBtrfs,
			SnapshotKernel:  loaderPath,
			SnapshotInitrds: initrdPaths,
			BtrfsVolume:     btrfsVolume,
		}

		log.Debug().
			Str("snapshot", snapshot.Path).
			Str("kernel", loaderPath).
			Strs("initrds", initrdPaths).
			Str("volume", btrfsVolume).
			Msg("Planned btrfs-mode boot for snapshot")

		plans = append(plans, plan)
	}

	return plans
}

// planESPMode creates BootPlans for a snapshot whose /boot is on the ESP.
// One plan is created per boot set; staleness is checked.
func (p *Planner) planESPMode(snapshot *btrfs.Snapshot) []*BootPlan {
	if len(p.bootSets) == 0 {
		// No boot sets detected — create a single plan with no staleness info
		log.Debug().
			Str("snapshot", snapshot.Path).
			Msg("ESP mode but no boot sets detected, creating plan without staleness")
		return []*BootPlan{{
			Snapshot: snapshot,
			Mode:     BootModeESP,
		}}
	}

	var plans []*BootPlan
	for _, bs := range p.bootSets {
		var staleness *StalenessResult
		if p.checker != nil {
			staleness = p.checker.CheckSnapshot(snapshot.FilesystemPath, bs)

			if staleness.IsStale {
				log.Warn().
					Str("snapshot", snapshot.Path).
					Str("kernel", bs.KernelName).
					Str("action", string(staleness.Action)).
					Str("reason", string(staleness.Reason)).
					Str("method", string(staleness.Method)).
					Msg("Snapshot is stale for boot kernel")
			} else {
				log.Debug().
					Str("snapshot", snapshot.Path).
					Str("kernel", bs.KernelName).
					Str("method", string(staleness.Method)).
					Msg("Snapshot is fresh for boot kernel")
			}
		}

		plans = append(plans, &BootPlan{
			Snapshot:  snapshot,
			Mode:      BootModeESP,
			BootSet:   bs,
			Staleness: staleness,
		})
	}

	return plans
}

// buildBtrfsVolume returns the best identifier for the root btrfs filesystem
// to use in rEFInd's "volume" directive.
// rEFInd accepts: filesystem label, partition label, or partition GUID.
func (p *Planner) buildBtrfsVolume() string {
	if p.rootFS == nil {
		return ""
	}

	// Prefer filesystem label (most readable in rEFInd menus)
	if p.rootFS.Label != "" {
		return p.rootFS.Label
	}

	// Fall back to partition label
	if p.rootFS.PartLabel != "" {
		return p.rootFS.PartLabel
	}

	// Fall back to filesystem UUID
	if p.rootFS.UUID != "" {
		return p.rootFS.UUID
	}

	// Fall back to partition UUID
	if p.rootFS.PartUUID != "" {
		return p.rootFS.PartUUID
	}

	return ""
}

// kernelImageSet represents a kernel and its associated initramfs files
// found inside a snapshot's /boot directory.
type kernelImageSet struct {
	kernelFilename  string
	initrdFilenames []string
}

// findKernelImages scans a directory for kernel images and pairs them with
// their initramfs. Uses the default patterns for matching.
func findKernelImages(bootDir string) []kernelImageSet {
	entries, err := os.ReadDir(bootDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Debug().Str("dir", bootDir).Msg("Boot directory does not exist in snapshot")
		} else {
			log.Warn().Err(err).Str("dir", bootDir).Msg("Failed to read snapshot boot directory")
		}
		return nil
	}

	patterns := DefaultPatterns()

	// First pass: find all images by matching patterns
	type imageMatch struct {
		filename   string
		role       ImageRole
		kernelName string
	}
	var matches []imageMatch

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		filename := entry.Name()
		for _, pattern := range patterns {
			ok, err := filepath.Match(pattern.Glob, filename)
			if err != nil || !ok {
				continue
			}
			matches = append(matches, imageMatch{
				filename:   filename,
				role:       pattern.Role,
				kernelName: pattern.DeriveKernelName(filename),
			})
			break // first match wins
		}
	}

	// Group by kernel name
	type imageGroup struct {
		kernel   string
		initrds  []string
		fallback string
	}
	groups := make(map[string]*imageGroup)

	for _, m := range matches {
		if m.kernelName == "" {
			continue // microcode, skip grouping
		}
		g, exists := groups[m.kernelName]
		if !exists {
			g = &imageGroup{}
			groups[m.kernelName] = g
		}
		switch m.role {
		case RoleKernel:
			g.kernel = m.filename
		case RoleInitramfs:
			g.initrds = append(g.initrds, m.filename)
		case RoleFallbackInitramfs:
			g.fallback = m.filename
		}
	}

	// Also collect microcode images (shared across all sets)
	var microcodeFiles []string
	for _, m := range matches {
		if m.role == RoleMicrocode {
			microcodeFiles = append(microcodeFiles, m.filename)
		}
	}

	// Build kernel image sets
	var result []kernelImageSet
	for name, g := range groups {
		if g.kernel == "" {
			log.Debug().Str("kernel_name", name).Msg("Skipping kernel group with no kernel image in snapshot /boot")
			continue
		}

		var allInitrds []string
		// Microcode first (rEFInd loads initrds in order)
		allInitrds = append(allInitrds, microcodeFiles...)
		// Then primary initramfs
		allInitrds = append(allInitrds, g.initrds...)

		result = append(result, kernelImageSet{
			kernelFilename:  g.kernel,
			initrdFilenames: allInitrds,
		})
	}

	return result
}

// GroupBySnapshot groups plans by snapshot path.
func GroupBySnapshot(plans []*BootPlan) map[string][]*BootPlan {
	result := make(map[string][]*BootPlan)
	for _, p := range plans {
		result[p.Snapshot.Path] = append(result[p.Snapshot.Path], p)
	}
	return result
}

// FormatStaleSummary returns a human-readable staleness summary string for
// a boot plan. Used in operation summaries and logging.
func (bp *BootPlan) FormatStaleSummary() string {
	if bp.Mode == BootModeBtrfs {
		return fmt.Sprintf("%s (btrfs-mode, always fresh)", bp.Snapshot.Path)
	}
	if bp.Staleness == nil || !bp.Staleness.IsStale {
		return ""
	}
	kernelName := ""
	if bp.BootSet != nil {
		kernelName = bp.BootSet.KernelName
	}
	return fmt.Sprintf("%s (kernel=%s, action=%s)", bp.Snapshot.Path, kernelName, bp.Staleness.Action)
}
