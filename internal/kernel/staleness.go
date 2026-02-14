package kernel

import (
	"fmt"

	"github.com/rs/zerolog/log"
)

// StaleAction defines what to do when a snapshot is stale relative to a boot set.
type StaleAction string

const (
	// ActionWarn logs a warning but generates the boot entry normally.
	ActionWarn StaleAction = "warn"

	// ActionDisable generates the boot entry with a "disabled" directive.
	ActionDisable StaleAction = "disable"

	// ActionDelete skips the boot entry entirely (not generated).
	ActionDelete StaleAction = "delete"

	// ActionFallback uses the fallback initramfs if available.
	// If no fallback exists, automatically downgrades to ActionDisable with a warning.
	ActionFallback StaleAction = "fallback"
)

// ParseStaleAction converts a string to a StaleAction.
// Returns ActionWarn and logs a warning for unrecognised values.
func ParseStaleAction(s string) StaleAction {
	switch StaleAction(s) {
	case ActionWarn, ActionDisable, ActionDelete, ActionFallback:
		return StaleAction(s)
	default:
		log.Warn().
			Str("value", s).
			Str("default", string(ActionDelete)).
			Msg("Unknown stale_snapshot_action, defaulting to 'delete'")
		return ActionDelete
	}
}

// StaleReason describes why a snapshot was determined to be stale.
type StaleReason string

const (
	// ReasonModulesMissing means the snapshot's /lib/modules/ does not contain
	// the kernel version that the current boot kernel expects.
	ReasonModulesMissing StaleReason = "modules_missing"

	// ReasonNoModulesDir means the snapshot has no /lib/modules/ directory at all.
	ReasonNoModulesDir StaleReason = "no_modules_dir"
)

// MatchMethod describes how the staleness check matched (or failed to match)
// module versions to boot kernel versions.
type MatchMethod string

const (
	// MatchBinaryHeader means the inspected kernel version from the bzImage header
	// was compared against snapshot module directory names.
	MatchBinaryHeader MatchMethod = "binary_header"

	// MatchPkgbase means the pkgbase file in the module directory was used to
	// match the module version to the boot set's kernel name.
	MatchPkgbase MatchMethod = "pkgbase"

	// MatchAssumedFresh means staleness could not be determined (no inspected
	// version, no pkgbase match) so the snapshot is assumed bootable.
	MatchAssumedFresh MatchMethod = "assumed_fresh"
)

// StalenessResult describes whether a snapshot is stale relative to a boot set,
// and what action should be taken.
type StalenessResult struct {
	// IsStale is true if the snapshot's kernel modules don't match the boot kernel.
	IsStale bool

	// Reason explains why the snapshot is stale (only meaningful when IsStale=true).
	Reason StaleReason

	// Action is the resolved action to take for this snapshot+bootset combination.
	Action StaleAction

	// SnapshotModules lists the kernel versions found in the snapshot's /lib/modules/.
	SnapshotModules []string

	// ExpectedVersion is the kernel version the boot set expects (from inspection).
	// Empty if inspection was not available.
	ExpectedVersion string

	// Method describes how the match was performed.
	Method MatchMethod

	// FallbackUsed is true if the fallback initramfs was substituted.
	FallbackUsed bool

	// Warning is a human-readable message for logging (empty if no warning).
	Warning string
}

// StatusString returns a human-readable status label: "stale", "fresh", or "unknown".
func (r *StalenessResult) StatusString() string {
	if r.IsStale {
		return "stale"
	}
	if r.Method == MatchAssumedFresh {
		return "unknown"
	}
	return "fresh"
}

// Checker performs staleness checks for snapshots against boot sets.
type Checker struct {
	defaultAction StaleAction
}

// NewChecker creates a staleness checker with the given default action.
func NewChecker(action StaleAction) *Checker {
	return &Checker{defaultAction: action}
}

// CheckSnapshot determines if a snapshot is stale relative to a boot set.
// It uses the best available matching method:
//  1. Binary header version (most reliable, requires kernel inspection)
//  2. Pkgbase file matching (Arch-specific, reliable)
//  3. Assumes fresh with warning (when neither method is available)
//
// The snapshotFSPath should be the root filesystem path of the mounted snapshot.
func (c *Checker) CheckSnapshot(snapshotFSPath string, bootSet *BootSet) *StalenessResult {
	snapshotModules := GetSnapshotModuleVersions(snapshotFSPath)

	// No modules directory at all — definitely stale
	if len(snapshotModules) == 0 {
		result := &StalenessResult{
			IsStale:         true,
			Reason:          ReasonNoModulesDir,
			SnapshotModules: snapshotModules,
			ExpectedVersion: bootSet.KernelVersion(),
			Method:          MatchBinaryHeader,
			Warning:         "no /lib/modules found in snapshot",
		}
		result.Action = c.resolveAction(result, bootSet)
		return result
	}

	kernelVersion := bootSet.KernelVersion()

	// Path 1: Binary header version available (best reliability)
	if kernelVersion != "" {
		for _, modVer := range snapshotModules {
			if modVer == kernelVersion {
				log.Debug().
					Str("kernel_name", bootSet.KernelName).
					Str("version", kernelVersion).
					Str("snapshot_module", modVer).
					Msg("Snapshot module version matches boot kernel (binary header)")
				return &StalenessResult{
					IsStale:         false,
					SnapshotModules: snapshotModules,
					ExpectedVersion: kernelVersion,
					Method:          MatchBinaryHeader,
				}
			}
		}

		// No direct version match — stale
		result := &StalenessResult{
			IsStale:         true,
			Reason:          ReasonModulesMissing,
			SnapshotModules: snapshotModules,
			ExpectedVersion: kernelVersion,
			Method:          MatchBinaryHeader,
			Warning: fmt.Sprintf("snapshot has modules %v but boot kernel expects %s",
				snapshotModules, kernelVersion),
		}
		result.Action = c.resolveAction(result, bootSet)
		return result
	}

	// Path 2: No inspected version — try pkgbase matching
	for _, modVer := range snapshotModules {
		pkgbase := ReadPkgbase(snapshotFSPath, modVer)
		if pkgbase != "" && pkgbase == bootSet.KernelName {
			log.Debug().
				Str("kernel_name", bootSet.KernelName).
				Str("module_version", modVer).
				Str("pkgbase", pkgbase).
				Msg("Snapshot module pkgbase matches boot set kernel name")
			return &StalenessResult{
				IsStale:         false,
				SnapshotModules: snapshotModules,
				Method:          MatchPkgbase,
			}
		}
	}

	// Path 3: Cannot determine — assume fresh with warning
	log.Debug().
		Str("kernel_name", bootSet.KernelName).
		Strs("snapshot_modules", snapshotModules).
		Msg("Could not verify kernel match; assuming snapshot is bootable")

	return &StalenessResult{
		IsStale:         false,
		SnapshotModules: snapshotModules,
		Method:          MatchAssumedFresh,
		Warning:         "could not inspect kernel binary or match pkgbase; assuming snapshot is bootable",
	}
}

// resolveAction determines the final action for a stale snapshot, handling
// the fallback-to-disable downgrade when no fallback initramfs exists.
func (c *Checker) resolveAction(result *StalenessResult, bootSet *BootSet) StaleAction {
	if !result.IsStale {
		return ""
	}

	action := c.defaultAction

	if action == ActionFallback {
		if bootSet.HasFallback() {
			result.FallbackUsed = true
			log.Debug().
				Str("kernel_name", bootSet.KernelName).
				Str("fallback", bootSet.Fallback.Filename).
				Msg("Using fallback initramfs for stale snapshot")
			return ActionFallback
		}

		// Downgrade to disable
		log.Warn().
			Str("kernel_name", bootSet.KernelName).
			Msg("Fallback initramfs not found, downgrading stale action from 'fallback' to 'disable'")
		result.Warning = "fallback initramfs not available; entry will be disabled"
		return ActionDisable
	}

	return action
}
