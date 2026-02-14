# Usage Guide

Full documentation for `refind-btrfs-snapshots`. For installation and quick start, see the [README](../README.md).

## Table of Contents

- [How It Works](#how-it-works)
- [Boot Mode Detection](#boot-mode-detection)
- [Commands](#commands)
  - [generate](#generate)
  - [list](#list)
  - [version](#version)
- [Configuration Reference](#configuration-reference)
  - [Configuration File Locations](#configuration-file-locations)
  - [ESP Detection Priority](#esp-detection-priority)
  - [All Options](#all-options)
- [Kernel Detection & Staleness](#kernel-detection--staleness)
  - [The Problem](#the-problem)
  - [How Staleness Checking Works](#how-staleness-checking-works)
  - [Staleness Match Methods](#staleness-match-methods)
  - [Stale Snapshot Actions](#stale-snapshot-actions)
  - [Boot Image Patterns](#boot-image-patterns)
- [Include File Management](#include-file-management)
- [Systemd Integration](#systemd-integration)
- [Integration Examples](#integration-examples)
- [Time and Format Handling](#time-and-format-handling)
- [Troubleshooting](#troubleshooting)
- [Development](#development)

## How It Works

1. **Detection Phase**: Discovers btrfs volumes, snapshots, and ESP location
2. **Boot Planning Phase**: Parses each snapshot's `/etc/fstab` to determine its boot mode — ESP mode (kernel on a separate partition) or btrfs mode (kernel inside the snapshot)
3. **Kernel Scan Phase**: For ESP-mode snapshots, scans the ESP for boot images, groups them into boot sets, inspects kernel binaries for version info, and checks each snapshot for matching kernel modules. For btrfs-mode snapshots, discovers kernels directly inside the snapshot's `/boot` directory.
4. **Analysis Phase**: Determines optimal configuration method (`refind_linux.conf` vs include files)
5. **Generation Phase**: Creates boot entries with proper kernel parameters and initrd paths, applying staleness actions for ESP-mode snapshots as configured
6. **Validation Phase**: Shows unified diff of all changes before applying
7. **Application Phase**: Updates configuration files atomically

## Boot Mode Detection

Boot mode is determined **per-snapshot** by inspecting each snapshot's own `/etc/fstab`. This is important because a system's boot configuration can change over time — some snapshots may have been taken when `/boot` was a separate ESP partition, and others when `/boot` was part of the btrfs subvolume.

### ESP Mode

When a snapshot's fstab shows `/boot` mounted on a non-btrfs filesystem (typically vfat), the snapshot is in **ESP mode**:

- Kernel and initramfs are loaded from the ESP (EFI System Partition)
- Staleness checking applies — if the snapshot's kernel modules don't match the ESP's kernel, the configured `stale_snapshot_action` is applied
- Submenu entries only override `options` (to set the snapshot's `rootflags=subvol=...`)

```
submenuentry "Arch Linux (2025-01-15T10:00:00Z)" {
    options  "... rootflags=subvol=/@/.snapshots/42/snapshot ..."
}
```

### Btrfs Mode

When a snapshot's fstab shows `/boot` is not separately mounted (or is mounted from the same btrfs filesystem), the snapshot is in **btrfs mode**:

- The snapshot contains its own kernel and initramfs in its `/boot` directory
- rEFInd's btrfs EFI driver loads these directly from the snapshot subvolume
- Staleness is impossible — the kernel and modules are always in sync within the snapshot
- Submenu entries override `volume`, `loader`, and `initrd` to point into the snapshot

```
submenuentry "Arch Linux (2025-02-14T10:00:00Z)" {
    volume   ARCH_ROOT
    loader   /@/.snapshots/73/snapshot/boot/vmlinuz-linux
    initrd   /@/.snapshots/73/snapshot/boot/initramfs-linux.img
    options  "... rootflags=subvol=/@/.snapshots/73/snapshot ..."
}
```

### Mixed Mode

A single rEFInd menu entry can contain both ESP-mode and btrfs-mode submenus. This happens naturally when a system transitions between boot configurations — older snapshots retain their original mode. The `list snapshots` and `list bootsets` commands show a `BOOT` column indicating each snapshot's detected mode.

> **Note:** Boot mode detection requires rEFInd's btrfs EFI driver (`btrfs_x64.efi`) to be installed for btrfs-mode entries to work. This driver is included with rEFInd and is typically loaded automatically.

## Commands

### `generate`

Generate rEFInd boot entries for btrfs snapshots.

```bash
sudo refind-btrfs-snapshots generate [flags]
```

**Flags:**

| Flag | Short | Description |
|------|-------|-------------|
| `--config-path` | | Path to rEFInd main config file |
| `--count` | `-n` | Number of snapshots to include (0 = all) |
| `--dry-run` | | Show what would be done without making changes |
| `--esp-path` | `-e` | Path to ESP mount point |
| `--force` | | Force generation even if booted from snapshot |
| `--generate-include` | `-g` | Force generation of `refind-btrfs-snapshots.conf` include file |
| `--yes` | `-y` | Automatically approve all changes without prompting |

**Examples:**

```bash
# Preview changes for top 5 snapshots
sudo refind-btrfs-snapshots generate --dry-run --count 5

# Generate include file and auto-approve
sudo refind-btrfs-snapshots generate -g -y

# Use custom rEFInd config path with debug logging
sudo refind-btrfs-snapshots generate --config-path /path/to/refind.conf --log-level debug

# Force operation even if booted from snapshot
sudo refind-btrfs-snapshots generate --force --dry-run
```

### `list`

List btrfs volumes, snapshots, and boot sets.

```bash
sudo refind-btrfs-snapshots list [command] [flags]
```

**Subcommands:**

| Subcommand | Description |
|------------|-------------|
| `volumes` | List all btrfs filesystems/volumes |
| `snapshots` | List all snapshots for detected volumes |
| `bootsets` | List detected boot image sets on the ESP |

**Flags (shared):**

| Flag | Short | Description |
|------|-------|-------------|
| `--all` | | Show all snapshots, including non-bootable ones |
| `--format` | `-f` | Output format: `table`, `json`, `yaml` (default: `table`) |
| `--search-dirs` | | Override snapshot search directories |
| `--show-size` | | Calculate and show snapshot sizes (slower) |

**Flags (bootsets only):**

| Flag | Description |
|------|-------------|
| `--json` | Output in JSON format |
| `--show-images` | Show individual boot images in addition to boot sets |

**Examples:**

```bash
# List all detected volumes
sudo refind-btrfs-snapshots list volumes

# List bootable snapshots in table format (includes BOOT mode column)
sudo refind-btrfs-snapshots list snapshots

# List all snapshots (including non-bootable) with sizes in JSON
sudo refind-btrfs-snapshots list snapshots --all --show-size -f json

# Show detected boot sets with snapshot compatibility matrix
sudo refind-btrfs-snapshots list bootsets

# Show individual boot images alongside boot sets
sudo refind-btrfs-snapshots list bootsets --show-images

# JSON output for scripting
sudo refind-btrfs-snapshots list bootsets --json
```

### `version`

Show version information.

```bash
refind-btrfs-snapshots version
```

## Configuration Reference

### Configuration File Locations

Configuration files are searched in the following order:

1. `--config` flag path (highest priority)
2. `/etc/refind-btrfs-snapshots.yaml` (recommended location)
3. Built-in defaults (lowest priority)

### ESP Detection Priority

The ESP detection system uses a three-tier priority system:

#### 1. UUID-Based Detection (Highest Priority)

```yaml
esp:
  uuid: "A1B2-C3D4"
```

- **Use case**: Multiple ESPs or specific ESP targeting
- **Reliability**: Highest — immune to mount point changes

#### 2. Automatic Detection (Default)

```yaml
esp:
  auto_detect: true
```

- **Method**: Scans `/proc/mounts` and `/sys/block` for ESP characteristics
- **Detection criteria**: VFAT filesystem with ESP partition type (EF00), common mount points (`/boot/efi`, `/efi`, `/boot`), presence of `/EFI` directory structure
- **Use case**: Standard single-ESP systems

#### 3. Manual Mount Point (Lowest Priority)

```yaml
esp:
  auto_detect: false
  mount_point: "/boot/efi"
```

- **Use case**: Non-standard ESP locations or troubleshooting

### All Options

| Category | Option | Default | Description |
|----------|--------|---------|-------------|
| **Snapshot** | `snapshot.selection_count` | `0` | Number of snapshots to include (0 = all) |
| | `snapshot.search_directories` | `["/.snapshots"]` | Directories to scan for snapshots |
| | `snapshot.max_depth` | `3` | Maximum search depth in snapshot directories |
| | `snapshot.writable_method` | `"toggle"` | Method for writable snapshots: `toggle` or `copy` |
| | `snapshot.destination_dir` | `"/.refind-btrfs-snapshots"` | Directory for copied writable snapshots |
| **ESP** | `esp.uuid` | `""` | Specific ESP UUID (highest priority) |
| | `esp.auto_detect` | `true` | Enable automatic ESP detection |
| | `esp.mount_point` | `""` | Manual ESP path (lowest priority) |
| **rEFInd** | `refind.config_path` | `"/EFI/refind/refind.conf"` | Path to main rEFInd config |
| **Behavior** | `behavior.exit_on_snapshot_boot` | `true` | Prevent operation when booted from snapshot |
| | `behavior.cleanup_old_snapshots` | `true` | Clean up old writable snapshots |
| **Display** | `display.local_time` | `false` | Display times in local time instead of UTC |
| **Logging** | `log_level` | `"info"` | Log verbosity: `trace`, `debug`, `info`, `warn`, `error` |
| **Kernel** | `kernel.stale_snapshot_action` | `"delete"` | Action for stale snapshots: `delete`, `warn`, `disable`, `fallback` |
| | `kernel.boot_image_patterns` | *(built-in)* | Custom boot image patterns (see config file) |
| **Advanced** | `advanced.naming.rwsnap_format` | `"2006-01-02_15-04-05"` | Timestamp format for writable snapshot filenames |
| | `advanced.naming.menu_format` | `"2006-01-02T15:04:05Z"` | Timestamp format for menu entry titles |

For the full annotated configuration file, see [`configs/refind-btrfs-snapshots.yaml`](../configs/refind-btrfs-snapshots.yaml).

## Kernel Detection & Staleness

### The Problem

On systems where `/boot` resides on a separate partition (typically the ESP), kernel images are not captured by btrfs snapshots. After a kernel upgrade, older snapshots may reference kernel modules that no longer exist on disk, resulting in unbootable snapshot entries.

This only applies to **ESP-mode** snapshots. **Btrfs-mode** snapshots contain their own kernel and are immune to this problem — see [Boot Mode Detection](#boot-mode-detection).

### How Staleness Checking Works

During generation, for ESP-mode snapshots:

1. **Scan**: Discovers boot images on the ESP (kernels, initramfs, fallback initramfs, microcode) using configurable glob patterns
2. **Inspect**: Reads kernel binary headers (bzImage format) to extract the exact kernel version string
3. **Group**: Assembles boot images into "boot sets" by kernel name (e.g., `linux`, `linux-lts`, `linux-zen`)
4. **Check**: For each snapshot, enumerates `/lib/modules/` inside the snapshot and compares against the boot set's expected kernel version

### Staleness Match Methods

The checker uses a three-tier strategy (best available wins):

| Method | Source | Reliability |
|--------|--------|-------------|
| **Binary header** | Reads kernel version from bzImage header, matches against snapshot `/lib/modules/<version>/` | Highest — exact version match |
| **Pkgbase** | Reads `/lib/modules/<version>/pkgbase` in the snapshot, matches against boot set kernel name | High — Arch Linux specific |
| **Assumed fresh** | Neither method available | Lowest — assumes bootable with a warning |

### Stale Snapshot Actions

Configure via `kernel.stale_snapshot_action` in your config file:

| Action | Behaviour |
|--------|-----------|
| `delete` (default) | Skips the boot entry entirely — stale snapshots won't appear in the menu |
| `warn` | Logs a warning, generates the boot entry normally |
| `disable` | Generates the boot entry with a `disabled` directive (visible but not bootable) |
| `fallback` | Uses the fallback initramfs; auto-downgrades to `disable` if no fallback exists |

### Boot Image Patterns

Built-in defaults cover most distributions:

- **Arch Linux**: `vmlinuz-*`, `initramfs-*.img`, `initramfs-*-fallback.img`
- **Debian/Ubuntu**: `vmlinuz-*`, `initrd.img-*`
- **Generic**: `vmlinuz`, `vmlinuz.efi`, `bzImage`, `initrd.img`, `initrd`, `initramfs.img`
- **Microcode**: `intel-ucode.img`, `amd-ucode.img`

For non-standard naming, override patterns in the config file:

```yaml
kernel:
  boot_image_patterns:
    - glob: "vmlinuz-*"
      role: kernel
      strip_prefix: "vmlinuz-"
    - glob: "initramfs-*.img"
      role: initramfs
      strip_prefix: "initramfs-"
      strip_suffix: ".img"
    - glob: "initramfs-*-fallback.img"
      role: fallback_initramfs
      strip_prefix: "initramfs-"
      strip_suffix: "-fallback.img"
```

Each pattern supports:
- `glob` — filename glob to match
- `role` — one of `kernel`, `initramfs`, `fallback_initramfs`, `microcode`
- `strip_prefix` / `strip_suffix` — removed from the filename to derive the kernel name
- `kernel_name` — explicit override when stripping isn't possible (e.g., generic `vmlinuz`)

## Include File Management

### Understanding Include Files

When `refind_linux.conf` updates aren't suitable (e.g., custom kernel configurations, multiple boot sets), the tool generates a `refind-btrfs-snapshots.conf` include file. This provides maximum flexibility while keeping your main rEFInd configuration clean.

> **Note:** `refind_linux.conf` only supports ESP-mode operation since it relies on rEFInd's auto-detection of kernel paths. If you have btrfs-mode snapshots, use the include file approach (`-g` flag) which can emit the `volume`, `loader`, and `initrd` overrides that btrfs-mode requires.

### Generated Include File Structure

```bash
# /boot/efi/EFI/refind/refind-btrfs-snapshots.conf
#
# Generated by refind-btrfs-snapshots

menuentry "Arch Linux" {
    disabled
    icon     /EFI/refind/icons/os_arch.png
    loader   /boot/vmlinuz-linux
    initrd   /boot/initramfs-linux.img
    options  "quiet splash rw rootflags=subvol=/@ root=UUID=..."

    # ESP-mode snapshot (kernel on ESP):
    submenuentry "Arch Linux (2025-01-15T10:00:00Z)" {
        options "quiet splash rw rootflags=subvol=/@/.snapshots/42/snapshot root=UUID=..."
    }

    # Btrfs-mode snapshot (kernel inside snapshot):
    submenuentry "Arch Linux (2025-02-14T10:00:00Z)" {
        volume  ARCH_ROOT
        loader  /@/.snapshots/73/snapshot/boot/vmlinuz-linux
        initrd  /@/.snapshots/73/snapshot/boot/initramfs-linux.img
        options "quiet splash rw rootflags=subvol=/@/.snapshots/73/snapshot root=UUID=..."
    }
}
```

**Setup:**

Add this line to your `refind.conf`:
```
include refind-btrfs-snapshots.conf
```

**Placement tips:**
- Place after your main entries for snapshots to appear at bottom
- Place before main entries for snapshots to appear at top
- Use rEFInd's `default_selection` to control which entry boots by default

## Systemd Integration

### Automatic Snapshot Menu Generation

Enable path-based monitoring to automatically regenerate boot entries when snapshots change:

```bash
sudo systemctl enable --now refind-btrfs-snapshots.path
```

### How It Works

1. **Path Monitoring**: The `.path` unit watches `/.snapshots` via inotify
2. **Trigger Throttling**: Waits 10 seconds after the last change to avoid excessive regeneration
3. **Service Execution**: Triggers the `.service` unit which runs `generate -g -y`
4. **Automatic Include**: Forces include file generation and auto-approves changes

### Multiple Snapshot Directories

To monitor additional snapshot locations:

```bash
sudo systemctl edit refind-btrfs-snapshots.path
```

```ini
[Path]
PathChanged=/run/timeshift/backup/timeshift-btrfs/snapshots
PathChanged=/custom/snapshots
```

### Service Customization

```bash
sudo systemctl edit refind-btrfs-snapshots.service
```

```ini
[Service]
ExecStart=
ExecStart=/usr/bin/refind-btrfs-snapshots --config /etc/custom-config.yaml generate -g -y -n 10
```

## Integration Examples

### Snapper

```yaml
snapshot:
  search_directories: ["/.snapshots"]
  writable_method: "toggle"
  selection_count: 0
  max_depth: 3

behavior:
  cleanup_old_snapshots: true

display:
  local_time: false

advanced:
  naming:
    menu_format: "2006-01-02T15:04:05Z"
```

### Timeshift

```yaml
snapshot:
  search_directories:
    - "/run/timeshift/backup/timeshift-btrfs/snapshots"
  writable_method: "copy"
  selection_count: 5
  destination_dir: "/timeshift-rwsnaps"

esp:
  mount_point: "/boot/efi"

display:
  local_time: true

advanced:
  naming:
    menu_format: "btrfs snapshot: YYYY/MM/DD-HH:mm"
```

Remember to configure the systemd path unit to monitor Timeshift's snapshot directory.

### Custom Snapshot Manager

```yaml
snapshot:
  search_directories:
    - "/snapshots/system"
    - "/snapshots/home"
  writable_method: "copy"
  selection_count: 3
  destination_dir: "/boot-snapshots"

behavior:
  cleanup_old_snapshots: true
  exit_on_snapshot_boot: false

advanced:
  naming:
    rwsnap_format: "2006-01-02_15-04-05"
    menu_format: "snapshot-YYYY-MM-DD_HH-mm"
```

## Time and Format Handling

### UTC Time Parsing

- **Snapper**: Times in `info.xml` are assumed UTC when no timezone is specified
- **Display**: Shown in UTC (default) or local time via `--local-time` flag or config
- **Menu entries**: Use ISO8601 format by default (`2025-06-14T10:00:02Z`)

### Timestamp Format Configuration

```yaml
advanced:
  naming:
    rwsnap_format: "2006-01-02_15-04-05"     # For writable snapshot filenames
    menu_format: "2006-01-02T15:04:05Z"      # For menu entry titles
```

**Template placeholders** (for `menu_format`):
`YYYY` (4-digit year), `YY` (2-digit year), `MM` (month), `DD` (day), `HH` (hour), `mm` (minute), `ss` (second)

**Examples:**
```yaml
menu_format: "2006-01-02T15:04:05Z"                   # "2025-06-14T17:32:09Z"
menu_format: "Jan 02, 2006 15:04"                     # "Jun 14, 2025 17:32"
menu_format: "btrfs snapshot: YYYY/MM/DD-HH:mm"       # "btrfs snapshot: 2025/06/14-17:32"
```

## Troubleshooting

### ESP Not Detected

```bash
# Debug ESP detection
sudo refind-btrfs-snapshots generate --dry-run --log-level debug

# Force specific ESP
sudo refind-btrfs-snapshots generate --esp-path /boot/efi
```

### Snapshots Not Found

```bash
# List detected snapshots with debug info
sudo refind-btrfs-snapshots list snapshots --log-level debug

# Check search directories
btrfs subvolume list /
```

### Stale Snapshot Entries

If snapshots are marked stale or entries missing after a kernel upgrade:

```bash
# Check boot images and their versions
sudo refind-btrfs-snapshots list bootsets --show-images

# Debug staleness detection
sudo refind-btrfs-snapshots generate --dry-run --log-level debug
# Look for "stale" or "modules" messages in output
```

Configure the action:
```yaml
kernel:
  stale_snapshot_action: "delete"  # or "warn", "disable", "fallback"
```

### Permission Errors

The tool requires root access to read btrfs subvolume information and write to the ESP:

```bash
sudo refind-btrfs-snapshots generate
```

### Debug Mode

```bash
sudo refind-btrfs-snapshots generate --log-level debug --dry-run
```

Shows: ESP detection, snapshot discovery, boot image scanning, kernel version inspection, boot mode detection per snapshot, staleness results, configuration resolution, and entry generation.

## Development

### Building from Source

```bash
git clone https://github.com/jmylchreest/refind-btrfs-snapshots.git
cd refind-btrfs-snapshots
go build -o refind-btrfs-snapshots

# With version info
go build -ldflags "-X github.com/jmylchreest/refind-btrfs-snapshots/cmd.Version=v1.0.0" \
  -o refind-btrfs-snapshots
```

### Testing

```bash
go test ./...                                    # All tests
go test -race -coverprofile=coverage.out ./...   # With coverage
go tool cover -html=coverage.out                 # View coverage report
```

### Contributing

1. Fork the repository
2. Create a feature branch: `git checkout -b feature-name`
3. Make changes and add tests
4. Ensure tests pass: `go test ./...`
5. Submit a pull request
