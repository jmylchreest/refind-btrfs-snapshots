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

// BootPlan describes how to boot one snapshot. One plan per snapshot per
// boot set — boot mode is fstab-determined per snapshot, not system-wide.
type BootPlan struct {
	Snapshot *btrfs.Snapshot
	Mode     BootMode
	Layout   BootLayout

	BootSet   *BootSet         // nil for btrfs-mode plans
	Staleness *StalenessResult // nil for btrfs-mode plans

	// SnapshotKernel and SnapshotInitrds are absolute paths within the
	// btrfs filesystem (e.g. /@/.snapshots/73/snapshot/boot/vmlinuz-linux).
	// SnapshotInitrds is empty for LayoutUKI — the initramfs is embedded.
	SnapshotKernel  string
	SnapshotInitrds []string

	// BtrfsVolume is the rEFInd "volume" identifier (label, UUID, etc.).
	BtrfsVolume string
}

func (bp *BootPlan) ShouldSkip() bool {
	if bp.Mode == BootModeBtrfs {
		return false
	}
	return bp.Staleness != nil && bp.Staleness.IsStale && bp.Staleness.Action == ActionDelete
}

func (bp *BootPlan) IsStale() bool {
	if bp.Mode == BootModeBtrfs {
		return false
	}
	return bp.Staleness != nil && bp.Staleness.IsStale
}

// Planner produces BootPlans by inspecting each snapshot's fstab to decide
// whether its kernel lives on the ESP or inside the snapshot. Layout-specific
// filtering (e.g. refind/bls binaries dropping UKI plans they can't act on)
// is the consumer's responsibility, not the planner's.
type Planner struct {
	fstabManager *fstab.Manager
	checker      *Checker
	bootSets     []*BootSet
	rootFS       *btrfs.Filesystem
}

func NewPlanner(fstabMgr *fstab.Manager, checker *Checker, bootSets []*BootSet, rootFS *btrfs.Filesystem) *Planner {
	return &Planner{
		fstabManager: fstabMgr,
		checker:      checker,
		bootSets:     bootSets,
		rootFS:       rootFS,
	}
}

// Plan emits one BootPlan per (snapshot × boot set). A snapshot in ESP
// mode yields one plan per boot set; a snapshot in btrfs mode yields one
// plan per kernel found inside the snapshot.
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
