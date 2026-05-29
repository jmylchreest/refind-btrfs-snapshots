package config

// Defaults returns a Config populated with the documented default values.
// Mirrors the SetDefault block in the legacy viper init exactly so the
// koanf migration produces identical resolved state for any given input.
func Defaults() Config {
	return Config{
		Snapshot: SnapshotConfig{
			SearchDirectories: []string{"/.snapshots"},
			MaxDepth:          3,
			SelectionCount:    0,
			DestinationDir:    "/.refind-btrfs-snapshots",
			WritableMethod:    "toggle",
		},
		Refind: RefindConfig{
			ConfigPath: "/EFI/refind/refind.conf",
		},
		ESP: ESPConfig{
			UUID:       "",
			AutoDetect: Truthy(true),
			MountPoint: "",
		},
		Behavior: BehaviorConfig{
			ExitOnSnapshotBoot:  Truthy(true),
			CleanupOldSnapshots: Truthy(true),
		},
		Kernel: KernelConfig{
			StaleSnapshotAction: "delete",
		},
		BLS: BLSConfig{
			WriteEntries: Truthy(false),
			EntriesDir:   "/loader/entries",
			EntryPrefix:  "bls-btrfs-snapshots-",
		},
		UKI: UKIConfig{
			WriteEntries: Truthy(false),
			OutputDir:    "/EFI/Linux",
			EntryPrefix:  "uki-btrfs-snapshots-",
		},
		Advanced: AdvancedConfig{
			Naming: NamingConfig{
				RwsnapFormat: "2006-01-02_15-04-05",
				MenuFormat:   "2006-01-02T15:04:05Z",
			},
		},
		Display:  DisplayConfig{LocalTime: Truthy(false)},
		LogLevel: "info",
	}
}
