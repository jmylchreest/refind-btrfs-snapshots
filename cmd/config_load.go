package cmd

import (
	"strconv"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// flagToKey maps cobra/pflag flag names (dash-separated, command-specific) to
// dotted koanf keys so config.Load can apply flag overrides at the highest
// precedence level. Only flags that have been explicitly set (pflag.Changed)
// are propagated, matching viper's BindPFlag behavior.
//
// Keep this in sync with the flag declarations in cmd/root.go and each command
// file's init().
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

// loadConfig resolves configuration from defaults, the optional config file,
// environment variables, and the cobra command's flag set (highest precedence).
func loadConfig(cmd *cobra.Command) (*config.Config, error) {
	path := cfgFile
	if path == "" {
		path = "/etc/refind-btrfs-snapshots.yaml"
	}
	return config.Load(path, flagOverrides(cmd.Flags()))
}

// flagOverrides walks the supplied flag set and returns the koanf-keyed
// values of any flag that the user explicitly set on the command line.
func flagOverrides(flags *pflag.FlagSet) map[string]any {
	overrides := make(map[string]any)
	flags.Visit(func(f *pflag.Flag) {
		key, ok := flagToKey[f.Name]
		if !ok {
			return
		}
		overrides[key] = flagValueAs(f)
	})
	if len(overrides) == 0 {
		return nil
	}
	return overrides
}

// flagValueAs reads the typed value from a pflag.Flag, preserving its declared
// type (bool / int / string) so the koanf merge sees the right Go type rather
// than the raw string form.
func flagValueAs(f *pflag.Flag) any {
	switch f.Value.Type() {
	case "bool":
		b, _ := strconv.ParseBool(f.Value.String())
		return b
	case "int":
		i, _ := strconv.Atoi(f.Value.String())
		return i
	default:
		return f.Value.String()
	}
}
