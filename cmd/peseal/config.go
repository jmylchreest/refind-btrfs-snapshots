package main

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/config"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Config is peseal's own typed configuration. Kept separate from the
// internal/config Config used by the snapshot-tooling binaries because
// peseal has no snapshot, ESP, kernel, BLS, or UKI concerns — only key
// material and which paths to sign.
type Config struct {
	KeyPath  string         `koanf:"key_path"`
	CertPath string         `koanf:"cert_path"`
	Paths    []string       `koanf:"paths"`
	LogLevel string         `koanf:"log_level"`
	DryRun   config.Truthy  `koanf:"dry_run"`

	// SkipAlreadySigned controls whether `peseal sign` is idempotent.
	// When true (the default), files already signed by the configured
	// cert are skipped on subsequent runs. Set false to always re-sign.
	SkipAlreadySigned config.Truthy `koanf:"skip_already_signed"`

	// AutoApprove binds to --yes / -y for parity with the other binaries.
	AutoApprove config.Truthy `koanf:"yes"`
}

// defaults returns a Config initialised with the values peseal ships with
// when no config file is present. Used both by Load and by tests.
func defaults() *Config {
	return &Config{
		Paths:             []string{"/boot/efi/EFI/Linux/*.efi", "/boot/EFI/Linux/*.efi"},
		SkipAlreadySigned: config.Truthy(true),
		LogLevel:          "info",
	}
}

func loadConfig(cmd *cobra.Command) (*Config, error) {
	path, _ := cmd.Flags().GetString("config")
	if path == "" {
		path = "/etc/peseal.yaml"
	}
	return loadConfigFrom(path)
}

func loadConfigFrom(path string) (*Config, error) {
	k := koanf.New(".")
	if err := k.Load(structs.Provider(defaults(), "koanf"), nil); err != nil {
		return nil, fmt.Errorf("load defaults: %w", err)
	}
	if path != "" {
		if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				log.Debug().Str("config_file", path).Msg("No config file found, using defaults")
			} else {
				log.Warn().Err(err).Str("config_file", path).Msg("Config file found but failed to parse, using defaults")
			}
		} else {
			log.Debug().Str("config_file", path).Msg("Using config file")
		}
	}
	cfg := defaults()
	if err := k.Unmarshal("", cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	return cfg, nil
}
