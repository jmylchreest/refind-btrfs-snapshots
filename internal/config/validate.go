package config

import "fmt"

// Validate checks the resolved configuration for invalid values.
// Returning an error here means the program will exit at startup rather
// than partway through generation — a deliberate behavior change from the
// legacy viper-based code, which silently defaulted unknown values for
// stale_snapshot_action and only caught invalid writable_method mid-run.
func (c *Config) Validate() error {
	switch c.Snapshot.WritableMethod {
	case "toggle", "copy":
	default:
		return fmt.Errorf("invalid snapshot.writable_method: %q (must be 'toggle' or 'copy')", c.Snapshot.WritableMethod)
	}

	switch c.Kernel.StaleSnapshotAction {
	case "warn", "disable", "delete", "fallback":
	default:
		return fmt.Errorf("invalid kernel.stale_snapshot_action: %q (must be one of: warn, disable, delete, fallback)", c.Kernel.StaleSnapshotAction)
	}

	if c.Snapshot.MaxDepth < 0 {
		return fmt.Errorf("invalid snapshot.max_depth: %d (must be >= 0)", c.Snapshot.MaxDepth)
	}

	return nil
}
