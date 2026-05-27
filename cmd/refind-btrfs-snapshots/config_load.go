package main

import (
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/cliconfig"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/config"
	"github.com/spf13/cobra"
)

// flagToKey maps cobra flag names (dash-separated, command-specific) to
// dotted koanf keys so cliconfig.Load can apply flag overrides at the highest
// precedence. Keep in sync with the flag declarations in cmd/root.go and
// each command file's init().
var flagToKey = map[string]string{
	"log-level":        "log_level",
	"local-time":       "display.local_time",
	"config-path":      "refind.config_path",
	"esp-path":         "esp.mount_point",
	"count":            "snapshot.selection_count",
	"dry-run":          "dry_run",
	"force":            "force",
	"generate-include": "generate_include",
	"yes":              "yes",
}

func loadConfig(cmd *cobra.Command) (*config.Config, error) {
	return cliconfig.Load(cmd, "/etc/refind-btrfs-snapshots.yaml", flagToKey)
}
