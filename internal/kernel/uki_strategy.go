package kernel

import "github.com/rs/zerolog/log"

// UKIStrategy chooses how to handle ESP-mode UKI snapshot entries.
// systemd-stub ignores load_options on standard UKIs, so a snapshot
// menu entry pointing at an ESP-resident UKI would always boot live
// root regardless of options we'd set. Btrfs-mode UKIs (inside the
// snapshot itself) are unaffected and always emitted.
type UKIStrategy string

const (
	UKIStrategySkip    UKIStrategy = "skip"
	UKIStrategyWarn    UKIStrategy = "warn"
	UKIStrategyDisable UKIStrategy = "disable"
)

// ParseUKIStrategy falls back to UKIStrategySkip for empty or unknown
// values — the safest default for a constraint users may not understand.
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
