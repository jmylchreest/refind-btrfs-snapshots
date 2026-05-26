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
			AutoDetect: true,
			MountPoint: "",
		},
		Behavior: BehaviorConfig{
			ExitOnSnapshotBoot:  true,
			CleanupOldSnapshots: true,
		},
		Kernel: KernelConfig{
			StaleSnapshotAction: "delete",
		},
		UKI: UKIConfig{
			SnapshotStrategy: "skip",
		},
		Advanced: AdvancedConfig{
			Naming: NamingConfig{
				RwsnapFormat: "2006-01-02_15-04-05",
				MenuFormat:   "2006-01-02T15:04:05Z",
			},
		},
		Display:  DisplayConfig{LocalTime: false},
		LogLevel: "info",
	}
}
