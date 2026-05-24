package config

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/posflag"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"
	"github.com/rs/zerolog/log"
	"github.com/spf13/pflag"
)

// EnvPrefix is the prefix used for environment variable bindings.
const EnvPrefix = "REFIND_BTRFS_SNAPSHOTS_"

// Load resolves configuration from defaults, optional YAML file, environment
// variables, and command flags (in that precedence order) into a typed Config.
//
// Behavior parity with the legacy viper-based loader:
//   - Missing config file: silently uses defaults.
//   - Malformed config file: logs a warning and falls back to defaults
//     (rather than returning an error).
//   - Environment variables: only top-level keys propagate (LOG_LEVEL works,
//     SNAPSHOT_SEARCH_DIRECTORIES does not). Matches the viper-without-
//     SetEnvKeyReplacer behavior captured in cmd/testdata/parity baselines.
//   - Flag values: only flags marked Changed override file/env, matching
//     viper.BindPFlag's pflag.Changed-aware lookup.
//
// Validation runs after merging and reports invalid writable_method,
// stale_snapshot_action, and max_depth — a deliberate change from the
// legacy code which caught these mid-run (or silently defaulted them).
func Load(cfgFile string, flags *pflag.FlagSet) (*Config, error) {
	k := koanf.New(".")

	if err := k.Load(structs.Provider(Defaults(), "koanf"), nil); err != nil {
		return nil, fmt.Errorf("load defaults: %w", err)
	}

	if cfgFile != "" {
		if err := k.Load(file.Provider(cfgFile), yaml.Parser()); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				log.Debug().Str("config_file", cfgFile).Msg("No config file found, using defaults")
			} else {
				log.Warn().Err(err).Str("config_file", cfgFile).Msg("Config file found but failed to parse, using defaults")
			}
		} else {
			log.Debug().Str("config_file", cfgFile).Msg("Using config file")
		}
	}

	envProvider := env.Provider(".", env.Opt{
		Prefix:        EnvPrefix,
		TransformFunc: envTransform,
	})
	if err := k.Load(envProvider, nil); err != nil {
		return nil, fmt.Errorf("load env: %w", err)
	}

	if flags != nil {
		if err := k.Load(posflag.Provider(flags, ".", k), nil); err != nil {
			return nil, fmt.Errorf("load flags: %w", err)
		}
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// envTransform strips the prefix and lowercases the key, deliberately
// preserving underscores rather than converting them to dot separators.
// This matches the legacy viper behavior where only top-level env vars
// (e.g. LOG_LEVEL) resolve — nested keys with underscores in segments
// (e.g. SNAPSHOT_SEARCH_DIRECTORIES) are silently ignored because they
// produce a top-level key the Config struct doesn't have.
func envTransform(key, value string) (string, any) {
	stripped := strings.TrimPrefix(key, EnvPrefix)
	return strings.ToLower(stripped), value
}
