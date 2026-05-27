// Package cliconfig wires cobra flags into the koanf config loader.
// Each binary supplies its own default config path and a flag→koanf-key map.
package cliconfig

import (
	"strconv"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Load reads --config (or defaultPath), loads the config, and applies any
// explicitly-set flags from cmd whose names appear in flagToKey as the
// highest-precedence overrides.
func Load(cmd *cobra.Command, defaultPath string, flagToKey map[string]string) (*config.Config, error) {
	path, _ := cmd.Flags().GetString("config")
	if path == "" {
		path = defaultPath
	}
	return config.Load(path, flagOverrides(cmd.Flags(), flagToKey))
}

func flagOverrides(flags *pflag.FlagSet, flagToKey map[string]string) map[string]any {
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
