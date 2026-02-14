# rEFInd Btrfs Snapshots

**Automatically generate rEFInd boot menu entries for btrfs snapshots with intelligent ESP detection and seamless configuration management.**

`refind-btrfs-snapshots` is a Go-based tool that bridges the gap between btrfs snapshot functionality and the rEFInd boot manager. It automatically discovers your btrfs snapshots, manages ESP (EFI System Partition) detection, and generates appropriate boot entries while preserving your existing rEFInd configuration.

## Project Overview

### What It Does

- **Automatic Snapshot Discovery**: Scans configured directories (like `/.snapshots`) to find btrfs snapshots
- **Intelligent ESP Detection**: Automatically locates your EFI System Partition using multiple detection methods
- **Kernel Staleness Detection**: Detects when a snapshot's kernel modules no longer match the running kernel (e.g., after a kernel upgrade) and takes configurable action (warn, disable, delete, or fallback)
- **Boot Image Scanning**: Automatically discovers kernels, initramfs, fallback initramfs, and microcode images with support for Arch, Debian/Ubuntu, Fedora, Gentoo, and generic naming conventions
- **Flexible Boot Entry Generation**: Creates boot entries via `refind_linux.conf` updates or standalone include files
- **Snapshot Management**: Handles read-only snapshots by either toggling flags or creating writable copies
- **Safety Features**: Prevents accidental operation when booted from snapshots
- **Systemd Integration**: Automatic menu regeneration when snapshots change
- **Configuration Flexibility**: Supports multiple snapshot managers (Snapper, Timeshift, custom)
- **UTC Time Handling**: Properly parses and displays snapshot times from info.xml files

### How It Works

1. **Detection Phase**: Discovers btrfs volumes, snapshots, and ESP location
2. **Kernel Scan Phase**: Scans the ESP for boot images, groups them into boot sets by kernel name, inspects kernel binaries for version info, and checks each snapshot for matching kernel modules
3. **Analysis Phase**: Determines optimal configuration method (refind_linux.conf vs include files)
4. **Generation Phase**: Creates boot entries with proper kernel parameters and initrd paths, applying staleness actions (warn/disable/delete/fallback) as configured
5. **Validation Phase**: Shows unified diff of all changes before applying
6. **Application Phase**: Updates configuration files atomically

## Installation

### From GitHub Releases (Recommended)

```bash
# Download latest release (amd64)
curl -L -o refind-btrfs-snapshots \
  "https://github.com/jmylchreest/refind-btrfs-snapshots/releases/latest/download/refind-btrfs-snapshots-linux-amd64"

# For arm64 systems:
# curl -L -o refind-btrfs-snapshots \
#   "https://github.com/jmylchreest/refind-btrfs-snapshots/releases/latest/download/refind-btrfs-snapshots-linux-arm64"

# Make executable and install
chmod +x refind-btrfs-snapshots
sudo mv refind-btrfs-snapshots /usr/bin/

# Install configuration file
sudo mkdir -p /etc
sudo curl -L -o /etc/refind-btrfs-snapshots.yaml \
  "https://github.com/jmylchreest/refind-btrfs-snapshots/raw/main/configs/refind-btrfs-snapshots.yaml"

# Install systemd units (optional)
sudo curl -L -o /usr/lib/systemd/system/refind-btrfs-snapshots.service \
  "https://github.com/jmylchreest/refind-btrfs-snapshots/releases/latest/download/refind-btrfs-snapshots.service"
sudo curl -L -o /usr/lib/systemd/system/refind-btrfs-snapshots.path \
  "https://github.com/jmylchreest/refind-btrfs-snapshots/releases/latest/download/refind-btrfs-snapshots.path"
```

### From Source

```bash
# Clone and build
git clone https://github.com/jmylchreest/refind-btrfs-snapshots.git
cd refind-btrfs-snapshots
go build -o refind-btrfs-snapshots

# Install
sudo cp refind-btrfs-snapshots /usr/bin/
sudo cp configs/refind-btrfs-snapshots.yaml /etc/refind-btrfs-snapshots.yaml
sudo cp systemd/*.{service,path} /etc/systemd/system/
```

### Arch Linux (AUR)

```bash
# Using yay or another AUR helper
yay -S refind-btrfs-snapshots-bin

# Or manually
git clone https://aur.archlinux.org/refind-btrfs-snapshots-bin.git
cd refind-btrfs-snapshots-bin
makepkg -si
```

## Quick Start

```bash
# Test configuration (dry run)
sudo refind-btrfs-snapshots generate --dry-run

# Generate boot entries
sudo refind-btrfs-snapshots generate

# List available snapshots
sudo refind-btrfs-snapshots list snapshots

# List detected volumes
sudo refind-btrfs-snapshots list volumes

# Enable automatic updates
sudo systemctl enable --now refind-btrfs-snapshots.path
```

## Configuration

### Configuration File Locations

Configuration files are searched in the following order of preference:

1. `--config` flag path (highest priority)
2. `/etc/refind-btrfs-snapshots.yaml` (recommended location)
3. Built-in defaults (lowest priority)

### EFI System Partition (ESP) Detection Priority

The ESP detection system uses a three-tier priority system to ensure reliable boot environment setup:

#### 1. UUID-Based Detection (Highest Priority)

When `esp.uuid` is configured, this method takes absolute precedence:

```yaml
esp:
  uuid: "A1B2-C3D4" # Specific ESP UUID
```

- **Use Case**: Multiple ESPs or specific ESP targeting
- **Reliability**: Highest - immune to mount point changes
- **Configuration**: Set via `esp.uuid` in config file

#### 2. Automatic Detection (Default)

When `esp.auto_detect: true` (default) and no UUID is specified:

```yaml
esp:
  auto_detect: true # Default setting
```

- **Method**: Scans `/proc/mounts` and `/sys/block` for ESP characteristics
- **Detection Criteria**:
  - VFAT filesystem with ESP partition type (EF00)
  - Common ESP mount points (`/boot/efi`, `/efi`, `/boot`)
  - Presence of `/EFI` directory structure
- **Use Case**: Standard single-ESP systems
- **Reliability**: High - works in most standard configurations

#### 3. Manual Mount Point (Lowest Priority)

When auto-detection is disabled and no UUID is set:

```yaml
esp:
  auto_detect: false
  mount_point: "/boot/efi" # Manual ESP path
```

- **Use Case**: Non-standard ESP locations or troubleshooting
- **Reliability**: Medium - requires manual maintenance
- **Configuration**: Set via `esp.mount_point` when `auto_detect` is false

### Configuration Options Summary

| Category                | Option                              | Default                      | Description                                              |
| ----------------------- | ----------------------------------- | ---------------------------- | -------------------------------------------------------- |
| **Snapshot Management** |                                     |                              |                                                          |
|                         | `snapshot.selection_count`         | `0`                          | Number of snapshots to include (0 = all)                |
|                         | `snapshot.search_directories`      | `["/.snapshots"]`            | Directories to scan for snapshots                       |
|                         | `snapshot.max_depth`               | `3`                          | Maximum search depth in snapshot directories            |
|                         | `snapshot.writable_method`         | `"toggle"`                   | Method for writable snapshots: `toggle` or `copy`       |
|                         | `snapshot.destination_dir`         | `"/.refind-btrfs-snapshots"` | Directory for copied writable snapshots                 |
| **ESP Configuration**   |                                     |                              |                                                          |
|                         | `esp.uuid`                          | `""`                         | Specific ESP UUID (highest priority)                    |
|                         | `esp.auto_detect`                   | `true`                       | Enable automatic ESP detection                          |
|                         | `esp.mount_point`                   | `""`                         | Manual ESP path (lowest priority)                       |
| **rEFInd Integration**  |                                     |                              |                                                          |
|                         | `refind.config_path`                | `"/EFI/refind/refind.conf"`  | Path to main rEFInd config                              |
| **Behavior Controls**   |                                     |                              |                                                          |
|                         | `behavior.exit_on_snapshot_boot`    | `true`                       | Prevent operation when booted from snapshot             |
|                         | `behavior.cleanup_old_snapshots`    | `true`                       | Clean up old writable snapshots                         |
| **Display**             |                                     |                              |                                                          |
|                         | `display.local_time`                | `false`                      | Display times in local time instead of UTC              |
| **Logging**             |                                     |                              |                                                          |
|                         | `log_level`                         | `"info"`                     | Log verbosity: `trace`, `debug`, `info`, `warn`, `error` |
| **Kernel Detection**    |                                     |                              |                                                          |
|                         | `kernel.stale_snapshot_action`      | `"warn"`                     | Action for stale snapshots: `warn`, `disable`, `delete`, `fallback` |
|                         | `kernel.boot_image_patterns`        | *(built-in defaults)*        | Custom boot image patterns (see config file for format) |
| **Advanced Options**    |                                     |                              |                                                          |
|                         | `advanced.naming.rwsnap_format`     | `"2006-01-02_15-04-05"`      | Timestamp format for writable snapshot filenames       |
|                         | `advanced.naming.menu_format`       | `"2006-01-02T15:04:05Z"`     | Timestamp format for menu entry titles                  |

For complete configuration reference, see [`configs/refind-btrfs-snapshots.yaml`](configs/refind-btrfs-snapshots.yaml).

## Commands

### Root Command

```bash
refind-btrfs-snapshots [command]
```

**Global Flags:**

- `--config string` - Config file path (default: `/etc/refind-btrfs-snapshots.yaml`)
- `--local-time` - Display times in local time instead of UTC
- `--log-level string` - Log level: `trace`, `debug`, `info`, `warn`, `error`, `fatal`, `panic` (default: `info`)

### `generate`

Generate rEFInd boot entries for btrfs snapshots.

```bash
sudo refind-btrfs-snapshots generate [flags]
```

**Flags:**

- `--config-path, -c` - Path to rEFInd main config file
- `--count, -n` - Number of snapshots to include (0 = all snapshots)
- `--dry-run` - Show what would be done without making changes
- `--esp-path, -e` - Path to ESP mount point
- `--force` - Force generation even if booted from snapshot
- `--generate-include, -g` - Force generation of refind-btrfs-snapshots.conf for inclusion
- `--update-refind-conf` - Update main rEFInd config file
- `--yes, -y` - Automatically approve all changes without prompting

**Examples:**

```bash
# Preview changes for top 5 snapshots
sudo refind-btrfs-snapshots generate --dry-run --count 5

# Generate include file and auto-approve
sudo refind-btrfs-snapshots generate -g -y

# Use custom config with debug logging
sudo refind-btrfs-snapshots generate -c /path/to/refind.conf --log-level debug

# Force operation even if booted from snapshot
sudo refind-btrfs-snapshots generate --force --dry-run
```

### `list`

List btrfs volumes and snapshots. Requires a subcommand.

```bash
sudo refind-btrfs-snapshots list [command] [flags]
```

**Subcommands:**

- `volumes` - List all btrfs filesystems/volumes
- `snapshots` - List all snapshots for detected volumes

**Flags:**

- `--all` - Show all snapshots, including non-bootable ones
- `--format, -f` - Output format: `table`, `json`, `yaml` (default: `table`)
- `--search-dirs` - Override snapshot search directories
- `--show-size` - Calculate and show snapshot sizes (slower)

**Examples:**

```bash
# List all detected volumes
sudo refind-btrfs-snapshots list volumes

# List bootable snapshots in table format
sudo refind-btrfs-snapshots list snapshots

# List all snapshots (including non-bootable) with sizes in JSON
sudo refind-btrfs-snapshots list snapshots --all --show-size -f json

# Use custom search directories
sudo refind-btrfs-snapshots list snapshots --search-dirs "/.snapshots,/timeshift/snapshots"
```

### `version`

Show version information.

```bash
refind-btrfs-snapshots version
```

## Time and Format Handling

### UTC Time Parsing

The tool correctly handles snapshot timestamps from various snapshot managers:

- **Snapper**: Times in `info.xml` files are assumed to be in UTC when no timezone is specified
- **Display**: Times can be shown in UTC (default) or local time using `--local-time` flag or config
- **Menu Entries**: Use ISO8601 format by default (`2025-06-14T10:00:02Z`)

### Timestamp Format Configuration

Configure how timestamps appear in different contexts:

```yaml
advanced:
  naming:
    # For writable snapshot filenames (filesystem-safe)
    rwsnap_format: "2006-01-02_15-04-05"

    # For menu entry titles (supports templates and Go formats)
    menu_format: "2006-01-02T15:04:05Z"
```

**Template Placeholders** (for `menu_format`):
- `YYYY` - 4-digit year
- `YY` - 2-digit year
- `MM` - 2-digit month
- `DD` - 2-digit day
- `HH` - 2-digit hour
- `mm` - 2-digit minute
- `ss` - 2-digit second

**Examples:**
```yaml
# Go time formats
menu_format: "2006-01-02T15:04:05Z"        # → "2025-06-14T17:32:09Z"
menu_format: "Jan 02, 2006 15:04"          # → "Jun 14, 2025 17:32"

# Template formats
menu_format: "btrfs snapshot: YYYY/MM/DD-HH:mm"  # → "btrfs snapshot: 2025/06/14-17:32"
menu_format: "snapshot-YYYY-MM-DD"               # → "snapshot-2025-06-14"
```

## Kernel Detection & Staleness

### The Problem

On systems where `/boot` resides on a separate partition (typically the ESP), kernel images are not captured by btrfs snapshots. After a kernel upgrade, older snapshots may reference kernel modules that no longer exist on disk, resulting in unbootable snapshot entries.

### How It Works

During generation, refind-btrfs-snapshots performs the following pipeline:

1. **Scan**: Discovers boot images on the ESP (kernels, initramfs, fallback initramfs, microcode) using configurable glob patterns
2. **Inspect**: Reads kernel binary headers (bzImage format) to extract the exact kernel version string
3. **Group**: Assembles boot images into "boot sets" by kernel name (e.g., `linux`, `linux-lts`, `linux-zen`)
4. **Check**: For each snapshot, enumerates `/lib/modules/` inside the snapshot and compares against the boot set's expected kernel version

### Staleness Match Methods

The checker uses a three-tier strategy (best available wins):

| Method | Source | Reliability |
|--------|--------|-------------|
| **Binary header** | Reads kernel version from bzImage header, matches against snapshot `/lib/modules/<version>/` directories | Highest — exact version match |
| **Pkgbase** | Reads `/lib/modules/<version>/pkgbase` in the snapshot, matches against boot set kernel name | High — Arch Linux specific |
| **Assumed fresh** | Neither method available | Lowest — assumes snapshot is bootable with a logged warning |

### Stale Snapshot Actions

Configure via `kernel.stale_snapshot_action` in your config file:

| Action | Behaviour |
|--------|-----------|
| `warn` (default) | Logs a warning, generates the boot entry normally |
| `disable` | Generates the boot entry with a `disabled` directive (visible in rEFInd but not bootable) |
| `delete` | Skips the boot entry entirely — it will not appear in the menu |
| `fallback` | Uses the fallback initramfs instead of the primary one; auto-downgrades to `disable` if no fallback exists |

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

When `refind_linux.conf` updates aren't suitable (e.g., custom kernel configurations), the tool generates a `refind-btrfs-snapshots.conf` include file. This provides maximum flexibility while keeping your main configuration clean.

### Generated Include File Structure

```bash
# Example: /boot/efi/EFI/refind/refind-btrfs-snapshots.conf
#
# Generated by refind-btrfs-snapshots
# WARNING - Submenu options will be overwritten automatically,
# but menuentry attributes will be maintained.
#
# To enable snapshot booting, add this line to your refind.conf:
#   include refind-btrfs-snapshots.conf

menuentry "Arch Linux" {
    disabled
    icon     /EFI/refind/icons/os_arch.png
    loader   /boot/vmlinuz-linux
    initrd   /boot/initramfs-linux.img
    options  quiet splash rw rootflags=subvol=/@ cryptdevice=UUID=0197662d-7906-7913-ade5-1d0f76c4f9a2:luks-0197662d-7906-7913-ade5-1d0f76c4f9a2 root=/dev/mapper/luks-0197662d-7906-7913-ade5-1d0f76c4f9a2

    # Snapshot submenus will be automatically generated below:
    submenuentry "Arch Linux (2025-06-14T10:00:02Z)" {
        options quiet splash rw rootflags=subvol=/@/.snapshots/8/snapshot,subvolid=275 cryptdevice=UUID=0197662d-7906-7913-ade5-1d0f76c4f9a2:luks-0197662d-7906-7913-ade5-1d0f76c4f9a2 root=/dev/mapper/luks-0197662d-7906-7913-ade5-1d0f76c4f9a2
    }
    submenuentry "Arch Linux (2025-06-14T09:00:01Z)" {
        options quiet splash rw rootflags=subvol=/@/.snapshots/7/snapshot,subvolid=274 cryptdevice=UUID=0197662d-7906-7913-ade5-1d0f76c4f9a2:luks-0197662d-7906-7913-ade5-1d0f76c4f9a2 root=/dev/mapper/luks-0197662d-7906-7913-ade5-1d0f76c4f9a2
    }
}
```

**Placement Tips:**

- Place after your main entries for snapshots to appear at bottom
- Place before main entries for snapshots to appear at top
- Use rEFInd's `default_selection` to control which entry boots by default

## Systemd Integration

### Automatic Snapshot Menu Generation

Enable path-based monitoring to automatically regenerate boot entries when snapshots change:

```bash
# Install and enable systemd units
sudo systemctl enable refind-btrfs-snapshots.path
sudo systemctl start refind-btrfs-snapshots.path

# Check status
sudo systemctl status refind-btrfs-snapshots.path
```

### How It Works

1. **Path Monitoring**: The `.path` unit monitors `/.snapshots` using inotify
2. **Trigger Delay**: Waits 10 seconds after last change to avoid excessive regeneration
3. **Service Execution**: Triggers the `.service` unit which runs generation with `-g -y` flags
4. **Automatic Include**: Forces include file generation and auto-approves changes

### Multiple Snapshot Directories

To monitor additional snapshot locations:

```bash
# Edit the path unit
sudo systemctl edit refind-btrfs-snapshots.path
```

Add content:

```ini
[Path]
# Monitor additional directories
PathChanged=/run/timeshift/backup/timeshift-btrfs/snapshots
PathChanged=/custom/snapshots
```

### Service Customization

Customize the generation behavior:

```bash
# Edit the service unit
sudo systemctl edit refind-btrfs-snapshots.service
```

Example customizations:

```ini
[Service]
# Change to limit snapshots and use custom config
ExecStart=
ExecStart=/usr/bin/refind-btrfs-snapshots generate -g -y -n 10 -c /etc/custom-config.yaml

# Add environment variables
Environment=REFIND_BTRFS_SNAPSHOTS_LOG_LEVEL=debug
```

## Integration Examples

### Snapper Integration

```yaml
snapshot:
  search_directories: ["/.snapshots"]
  writable_method: "toggle"
  selection_count: 0 # Include all snapshots
  max_depth: 3

behavior:
  cleanup_old_snapshots: true

display:
  local_time: false # Use UTC for consistency

advanced:
  naming:
    menu_format: "2006-01-02T15:04:05Z" # ISO8601 format
```

**Snapper Workflow:**

1. Snapper creates snapshots in `/.snapshots` with UTC timestamps
2. Systemd path unit detects changes
3. Boot entries are automatically generated with proper time parsing
4. Snapshots appear in rEFInd menu with consistent timestamps

### Timeshift Integration

```yaml
snapshot:
  search_directories:
    - "/run/timeshift/backup/timeshift-btrfs/snapshots"
  writable_method: "copy"
  selection_count: 5 # Limit to 5 most recent
  destination_dir: "/timeshift-rwsnaps"

esp:
  mount_point: "/boot/efi" # Common Ubuntu ESP location

display:
  local_time: true # Show times in user's timezone

advanced:
  naming:
    menu_format: "btrfs snapshot: YYYY/MM/DD-HH:mm"
```

**Timeshift Workflow:**

1. Configure systemd path to monitor Timeshift snapshot directory
2. Timeshift creates snapshots during scheduled backups
3. Tool automatically generates boot entries with user-friendly timestamps
4. Recent snapshots available for recovery

### Custom Snapshot Manager Integration

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
  exit_on_snapshot_boot: false # Allow nested snapshot operations

advanced:
  naming:
    rwsnap_format: "2006-01-02_15-04-05"
    menu_format: "snapshot-YYYY-MM-DD_HH-mm"

display:
  local_time: false
```

## Development

### Building from Source

```bash
# Clone repository
git clone https://github.com/jmylchreest/refind-btrfs-snapshots.git
cd refind-btrfs-snapshots

# Build binary
go build -o refind-btrfs-snapshots

# Run tests
go test ./...

# Build with version information
go build -ldflags "-X github.com/jmylchreest/refind-btrfs-snapshots/cmd.Version=v1.0.0" \
  -o refind-btrfs-snapshots
```

### Testing

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test -race -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Run specific test packages
go test ./internal/refind
go test ./internal/esp
go test ./internal/params
```

### Contributing

1. Fork the repository
2. Create a feature branch: `git checkout -b feature-name`
3. Make changes and add tests
4. Ensure tests pass: `go test ./...`
5. Submit a pull request

## Troubleshooting

### Common Issues

**ESP Not Detected:**

```bash
# Check ESP detection manually
sudo refind-btrfs-snapshots generate --dry-run --log-level debug

# Force specific ESP
sudo refind-btrfs-snapshots generate --esp-path /boot/efi
```

**Snapshots Not Found:**

```bash
# List detected snapshots with debug info
sudo refind-btrfs-snapshots list snapshots --log-level debug

# Check search directories
btrfs subvolume list /
```

**Permission Errors:**

```bash
# Ensure running as root
sudo refind-btrfs-snapshots generate

# Check ESP mount permissions
ls -la /boot/efi/EFI/refind/
```

**Stale Snapshot Entries:**

If snapshots are being marked stale or entries are missing after a kernel upgrade:

```bash
# Check what boot images are detected and their versions
sudo refind-btrfs-snapshots generate --dry-run --log-level debug

# Look for "stale" or "modules" messages in debug output
# The debug log shows: scan results, boot sets, kernel versions,
# snapshot module versions, and match method used

# To keep entries visible but non-bootable:
# Set in /etc/refind-btrfs-snapshots.yaml:
#   kernel:
#     stale_snapshot_action: "disable"

# To silently remove stale entries:
#   kernel:
#     stale_snapshot_action: "delete"
```

**Time Display Issues:**

```bash
# Check time parsing with local time
sudo refind-btrfs-snapshots list snapshots --local-time

# Force UTC display
sudo refind-btrfs-snapshots list snapshots
```

### Debug Mode

Enable detailed logging for troubleshooting:

```bash
sudo refind-btrfs-snapshots generate --log-level debug --dry-run
```

This will show:
- ESP detection process
- Snapshot discovery details
- Boot image scanning and kernel version inspection
- Staleness check results per snapshot (match method, module versions)
- Time parsing and formatting
- Configuration resolution
- Boot entry generation logic

## Links

- **GitHub Repository**: https://github.com/jmylchreest/refind-btrfs-snapshots
- **Issue Tracker**: https://github.com/jmylchreest/refind-btrfs-snapshots/issues
- **Releases**: https://github.com/jmylchreest/refind-btrfs-snapshots/releases
- **AUR Package**: https://aur.archlinux.org/packages/refind-btrfs-snapshots-bin
