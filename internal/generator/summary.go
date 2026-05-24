package generator

import "github.com/rs/zerolog/log"

// OperationSummary records what happened during a generation run so the
// final log line shows exactly which snapshots were added/removed, which
// fstabs and configs were updated, and which snapshots were detected stale.
type OperationSummary struct {
	IncludedSnapshots []string // All snapshots selected for this run
	AddedSnapshots    []string // Snapshots actually added to configs (new ones)
	RemovedSnapshots  []string // Snapshots removed from configs (due to stale-delete)
	StaleSnapshots    []string // Snapshots detected as stale
	UpdatedFstabs     []string
	UpdatedConfigs    []string
	WritableChanges   []string
}

// LogSummary emits the comprehensive operation summary log line that runs
// at the end of every generation invocation (dry or live).
func LogSummary(summary *OperationSummary, isDryRun bool) {
	prefix := ""
	if isDryRun {
		prefix = "[DRY RUN] "
	}

	log.Info().
		Strs("included_snapshots", summary.IncludedSnapshots).
		Strs("added_snapshots", summary.AddedSnapshots).
		Strs("removed_snapshots", summary.RemovedSnapshots).
		Strs("stale_snapshots", summary.StaleSnapshots).
		Strs("updated_fstabs", summary.UpdatedFstabs).
		Strs("updated_configs", summary.UpdatedConfigs).
		Strs("writable_changes", summary.WritableChanges).
		Msg(prefix + "Operation summary")
}
