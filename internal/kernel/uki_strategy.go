package kernel

import "github.com/rs/zerolog/log"

// UKIStrategy controls how planESPMode treats UKI-layout boot sets.
//
// A UKI's embedded .cmdline is the only cmdline the systemd-stub honours on
// standard UKIs — load_options from the boot loader are ignored. That means
// snapshot menu entries pointing at an ESP-resident UKI would always boot the
// live root regardless of any options we set. UKIStrategy picks the response
// to that limitation; btrfs-mode UKI sets (UKI inside the snapshot itself)
// are unaffected.
type UKIStrategy string

const (
	// UKIStrategySkip omits ESP-mode UKI snapshot plans entirely. Safest;
	// no misleading entries appear in the menu. Default.
	UKIStrategySkip UKIStrategy = "skip"

	// UKIStrategyWarn generates the plan and logs a warning that the entry
	// will boot live root, not the snapshot.
	UKIStrategyWarn UKIStrategy = "warn"

	// UKIStrategyDisable generates the plan with Disabled=true so the
	// generator emits rEFInd's `disabled` directive — the entry is visible
	// but unbootable, surfacing the limitation to users without false hope.
	UKIStrategyDisable UKIStrategy = "disable"
)

// ParseUKIStrategy converts a string to a UKIStrategy. Empty or unknown
// values fall back to UKIStrategySkip (the safest default).
func ParseUKIStrategy(s string) UKIStrategy {
	switch UKIStrategy(s) {
	case UKIStrategySkip, UKIStrategyWarn, UKIStrategyDisable:
		return UKIStrategy(s)
	case "":
		return UKIStrategySkip
	default:
		log.Warn().
			Str("value", s).
			Str("default", string(UKIStrategySkip)).
			Msg("Unknown uki.snapshot_strategy, defaulting to 'skip'")
		return UKIStrategySkip
	}
}
