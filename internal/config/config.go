// Package config defines the typed configuration schema and loader.
package config

type Config struct {
	Snapshot SnapshotConfig `koanf:"snapshot"`
	Refind   RefindConfig   `koanf:"refind"`
	ESP      ESPConfig      `koanf:"esp"`
	Behavior BehaviorConfig `koanf:"behavior"`
	Kernel   KernelConfig   `koanf:"kernel"`
	UKI      UKIConfig      `koanf:"uki"`
	BLS      BLSConfig      `koanf:"bls"`
	Display  DisplayConfig  `koanf:"display"`
	Advanced AdvancedConfig `koanf:"advanced"`
	List     ListConfig     `koanf:"list"`

	LogLevel        string `koanf:"log_level"`
	DryRun          Truthy `koanf:"dry_run"`
	Force           Truthy `koanf:"force"`
	GenerateInclude Truthy `koanf:"generate_include"`

	// AutoApprove binds to --yes / -y (YAML key kept as "yes" for user familiarity).
	AutoApprove Truthy `koanf:"yes"`
}

type SnapshotConfig struct {
	SearchDirectories []string `koanf:"search_directories"`
	MaxDepth          int      `koanf:"max_depth"`
	SelectionCount    int      `koanf:"selection_count"`
	DestinationDir    string   `koanf:"destination_dir"`
	WritableMethod    string   `koanf:"writable_method"`
}

type RefindConfig struct {
	ConfigPath string `koanf:"config_path"`
}

type ESPConfig struct {
	UUID       string `koanf:"uuid"`
	AutoDetect Truthy `koanf:"auto_detect"`
	MountPoint string `koanf:"mount_point"`
}

type BehaviorConfig struct {
	ExitOnSnapshotBoot  Truthy `koanf:"exit_on_snapshot_boot"`
	CleanupOldSnapshots Truthy `koanf:"cleanup_old_snapshots"`
}

type KernelConfig struct {
	StaleSnapshotAction string          `koanf:"stale_snapshot_action"`
	BootImagePatterns   []PatternConfig `koanf:"boot_image_patterns"`
}

// PatternConfig mirrors kernel.PatternConfig so the config package stays
// independent of internal/kernel; the command layer converts between them.
type PatternConfig struct {
	Glob        string `koanf:"glob"`
	Role        string `koanf:"role"`
	StripPrefix string `koanf:"strip_prefix"`
	StripSuffix string `koanf:"strip_suffix"`
	KernelName  string `koanf:"kernel_name"`
}

type DisplayConfig struct {
	LocalTime Truthy `koanf:"local_time"`
}

// BLSConfig: optional BLS Type #1 entry output, consumed by the bls-btrfs-snapshots
// binary. rEFInd does not read these. Only ESP-mode snapshots emit — systemd-boot
// and BLS-GRUB cannot traverse btrfs subvolumes.
type BLSConfig struct {
	WriteEntries Truthy `koanf:"write_entries"`
	EntriesDir   string `koanf:"entries_dir"`
	EntryPrefix  string `koanf:"entry_prefix"`
}

// UKIConfig.SnapshotStrategy: see docs/USAGE.md "UKI Snapshots: ESP-mode Caveat".
type UKIConfig struct {
	SnapshotStrategy string `koanf:"snapshot_strategy"`
}

type AdvancedConfig struct {
	Naming NamingConfig `koanf:"naming"`
}

type NamingConfig struct {
	RwsnapFormat string `koanf:"rwsnap_format"`
	MenuFormat   string `koanf:"menu_format"`
}

type ListConfig struct {
	Format   string `koanf:"format"`
	ShowAll  Truthy `koanf:"show_all"`
	ShowSize Truthy `koanf:"show_size"`
}
