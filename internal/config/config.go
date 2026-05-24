// Package config defines the typed configuration schema and loader for the
// refind-btrfs-snapshots tool. Application code reads configuration through
// the typed Config struct instead of string-keyed lookups.
package config

// Config is the root configuration. Fields with no koanf tag at the top
// level are bound to top-level YAML keys; nested structs map to YAML
// sections via their koanf tag.
type Config struct {
	Snapshot SnapshotConfig `koanf:"snapshot"`
	Refind   RefindConfig   `koanf:"refind"`
	ESP      ESPConfig      `koanf:"esp"`
	Behavior BehaviorConfig `koanf:"behavior"`
	Kernel   KernelConfig   `koanf:"kernel"`
	Display  DisplayConfig  `koanf:"display"`
	Advanced AdvancedConfig `koanf:"advanced"`
	List     ListConfig     `koanf:"list"`

	LogLevel        string `koanf:"log_level"`
	DryRun          bool   `koanf:"dry_run"`
	Force           bool   `koanf:"force"`
	GenerateInclude bool   `koanf:"generate_include"`
	Yes             bool   `koanf:"yes"`
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
	AutoDetect bool   `koanf:"auto_detect"`
	MountPoint string `koanf:"mount_point"`
}

type BehaviorConfig struct {
	ExitOnSnapshotBoot  bool `koanf:"exit_on_snapshot_boot"`
	CleanupOldSnapshots bool `koanf:"cleanup_old_snapshots"`
}

type KernelConfig struct {
	StaleSnapshotAction string          `koanf:"stale_snapshot_action"`
	BootImagePatterns   []PatternConfig `koanf:"boot_image_patterns"`
}

// PatternConfig describes how to recognise a boot image file by name.
// Mirrors kernel.PatternConfig so the config layer doesn't depend on
// internal/kernel; conversion happens in the command layer.
type PatternConfig struct {
	Glob        string `koanf:"glob"`
	Role        string `koanf:"role"`
	StripPrefix string `koanf:"strip_prefix"`
	StripSuffix string `koanf:"strip_suffix"`
	KernelName  string `koanf:"kernel_name"`
}

type DisplayConfig struct {
	LocalTime bool `koanf:"local_time"`
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
	ShowAll  bool   `koanf:"show_all"`
	ShowSize bool   `koanf:"show_size"`
}
