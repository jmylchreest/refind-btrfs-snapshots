package kernel

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

	// Layout describes the on-disk arrangement of this plan's kernel
	// (split kernel+initrd vs UKI, etc.). Generator branches on this
	// to emit the right submenu shape (UKIs have no initrd/options).
	Layout BootLayout

	// Disabled is true when the plan should be emitted with rEFInd's
	// 'disabled' directive — visible in the menu but unbootable. Currently
	// set when UKIStrategy=disable for an ESP-mode UKI plan.
	Disabled bool

	// BootSet is the ESP boot set matched to this snapshot.
	// Nil for btrfs-mode snapshots.
	BootSet *BootSet

	// Staleness is the staleness check result for this snapshot+bootset.
	// Nil for btrfs-mode snapshots.
	Staleness *StalenessResult

	// SnapshotKernel is the snapshot-relative path to the kernel image.
	// For LayoutSplit/LayoutBLS, points at vmlinuz. For LayoutUKI, points
	// at the .efi file inside the snapshot.
	// e.g., "/@/.snapshots/73/snapshot/boot/vmlinuz-linux"
	//       "/@/.snapshots/73/snapshot/boot/EFI/Linux/linux.efi"
	SnapshotKernel string

	// SnapshotInitrds are the snapshot-relative paths to initramfs images.
	// Empty for LayoutUKI (initramfs is embedded in the UKI).
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
	ukiStrategy  UKIStrategy
}

// NewPlanner creates a Planner.
//
// Parameters:
//   - fstabMgr: used to parse snapshot fstab files
//   - checker: staleness checker for ESP-mode snapshots (may be nil if no boot sets)
//   - bootSets: detected ESP boot sets (may be empty)
//   - rootFS: the root btrfs filesystem (used to determine if /boot is on the same device)
//   - ukiStrategy: how to handle ESP-mode UKI boot sets (skip/warn/disable)
func NewPlanner(fstabMgr *fstab.Manager, checker *Checker, bootSets []*BootSet, rootFS *btrfs.Filesystem, ukiStrategy UKIStrategy) *Planner {
	return &Planner{
		fstabManager: fstabMgr,
		checker:      checker,
		bootSets:     bootSets,
		rootFS:       rootFS,
		ukiStrategy:  ukiStrategy,
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
	bootDir := filepath.Join(snapshot.FilesystemPath, "boot")
	kernelImages := findKernelImages(bootDir)

	if len(kernelImages) == 0 {
		log.Warn().
			Str("snapshot", snapshot.Path).
			Str("boot_dir", bootDir).
			Msg("Btrfs-mode snapshot has no kernel images in /boot, falling back to ESP mode")
		return p.planESPMode(snapshot)
	}

	btrfsVolume := p.buildBtrfsVolume()
	snapshotSubvolPath := snapshot.Path
	if !strings.HasPrefix(snapshotSubvolPath, "/") {
		snapshotSubvolPath = "/" + snapshotSubvolPath
	}

	var plans []*BootPlan
	for _, ki := range kernelImages {
		loaderPath := filepath.Join(snapshotSubvolPath, ki.kernelRelPath)
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
			Layout:          ki.layout,
			SnapshotKernel:  loaderPath,
			SnapshotInitrds: initrdPaths,
			BtrfsVolume:     btrfsVolume,
		}

		log.Debug().
			Str("snapshot", snapshot.Path).
			Str("layout", string(ki.layout)).
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
		if bs.Layout == LayoutUKI {
			plan := p.planESPModeUKI(snapshot, bs)
			if plan != nil {
				plans = append(plans, plan)
			}
			continue
		}

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
			Layout:    bs.Layout,
			BootSet:   bs,
			Staleness: staleness,
		})
	}

	return plans
}

// planESPModeUKI applies the UKI strategy to an ESP-mode UKI boot set.
// Returns nil when the strategy is "skip" (no plan emitted for this snapshot+set).
// The systemd-stub ignores boot-loader-supplied cmdline on standard UKIs, so
// an ESP-resident UKI's embedded cmdline points at the live root regardless
// of any snapshot-targeted options we'd otherwise write.
func (p *Planner) planESPModeUKI(snapshot *btrfs.Snapshot, bs *BootSet) *BootPlan {
	switch p.ukiStrategy {
	case UKIStrategySkip:
		log.Debug().
			Str("snapshot", snapshot.Path).
			Str("kernel", bs.KernelName).
			Msg("Skipping ESP-mode UKI snapshot entry (uki.snapshot_strategy=skip)")
		return nil
	case UKIStrategyWarn:
		log.Warn().
			Str("snapshot", snapshot.Path).
			Str("kernel", bs.KernelName).
			Msg("ESP-mode UKI snapshot entry will boot live root (uki.snapshot_strategy=warn)")
		return &BootPlan{
			Snapshot: snapshot,
			Mode:     BootModeESP,
			Layout:   LayoutUKI,
			BootSet:  bs,
		}
	case UKIStrategyDisable:
		log.Info().
			Str("snapshot", snapshot.Path).
			Str("kernel", bs.KernelName).
			Msg("Marking ESP-mode UKI snapshot entry as disabled (uki.snapshot_strategy=disable)")
		return &BootPlan{
			Snapshot: snapshot,
			Mode:     BootModeESP,
			Layout:   LayoutUKI,
			BootSet:  bs,
			Disabled: true,
		}
	default:
		return nil
	}
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
// found inside a snapshot's /boot directory. For UKI sets, kernelRelPath is
// /boot/EFI/Linux/<file>.efi and initrdFilenames is nil.
type kernelImageSet struct {
	kernelRelPath   string // path relative to the snapshot root, e.g. "boot/vmlinuz-linux" or "boot/EFI/Linux/linux.efi"
	kernelFilename  string
	initrdFilenames []string
	layout          BootLayout
}

// findKernelImages scans a directory for kernel images and pairs them with
// their initramfs. Also walks <bootDir>/EFI/Linux/ for UKIs. Each returned
// kernelImageSet carries its layout so the planner can emit the correct
// submenu shape.
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
			break
		}
	}

	type imageGroup struct {
		kernel   string
		initrds  []string
		fallback string
	}
	groups := make(map[string]*imageGroup)

	for _, m := range matches {
		if m.kernelName == "" {
			continue
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

	var microcodeFiles []string
	for _, m := range matches {
		if m.role == RoleMicrocode {
			microcodeFiles = append(microcodeFiles, m.filename)
		}
	}

	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	slices.Sort(names)

	var result []kernelImageSet
	for _, name := range names {
		g := groups[name]
		if g.kernel == "" {
			log.Debug().Str("kernel_name", name).Msg("Skipping kernel group with no kernel image in snapshot /boot")
			continue
		}

		var allInitrds []string
		allInitrds = append(allInitrds, microcodeFiles...)
		allInitrds = append(allInitrds, g.initrds...)

		result = append(result, kernelImageSet{
			kernelRelPath:   filepath.ToSlash(filepath.Join("boot", g.kernel)),
			kernelFilename:  g.kernel,
			initrdFilenames: allInitrds,
			layout:          LayoutSplit,
		})
	}

	result = append(result, findUKIsInSnapshot(bootDir)...)

	return result
}

// findUKIsInSnapshot walks <bootDir>/EFI/Linux/ for *.efi UKIs. Each becomes
// a self-contained kernelImageSet with no initrds and layout=UKI.
func findUKIsInSnapshot(bootDir string) []kernelImageSet {
	ukiDir := filepath.Join(bootDir, "EFI", "Linux")
	entries, err := os.ReadDir(ukiDir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Debug().Err(err).Str("dir", ukiDir).Msg("Could not scan snapshot UKI directory")
		}
		return nil
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".efi") {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)

	out := make([]kernelImageSet, 0, len(names))
	for _, name := range names {
		out = append(out, kernelImageSet{
			kernelRelPath:  filepath.ToSlash(filepath.Join("boot", "EFI", "Linux", name)),
			kernelFilename: name,
			layout:         LayoutUKI,
		})
	}
	return out
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
