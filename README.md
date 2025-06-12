# rEFInd Btrfs Snapshots

**Automatically generate rEFInd boot menu entries for btrfs snapshots with intelligent ESP detection and seamless configuration management.**

`refind-btrfs-snapshots` is a Go-based tool that bridges the gap between btrfs snapshot functionality and the rEFInd boot manager. It automatically discovers your btrfs snapshots, manages ESP (EFI System Partition) detection, and generates appropriate boot entries while preserving your existing rEFInd configuration.

## Project Overview

### What It Does

- **Automatic Snapshot Discovery**: Scans configured directories (like `/.snapshots`) to find btrfs snapshots
- **Intelligent ESP Detection**: Automatically locates your EFI System Partition using multiple detection methods
- **Flexible Boot Entry Generation**: Creates boot entries via `refind_linux.conf` updates or standalone include files
- **Snapshot Management**: Handles read-only snapshots by either toggling flags or creating writable copies
- **Safety Features**: Prevents accidental operation when booted from snapshots
- **Systemd Integration**: Automatic menu regeneration when snapshots change
- **Configuration Flexibility**: Supports multiple snapshot managers (Snapper, Timeshift, custom)

### How It Works

1. **Detection Phase**: Discovers btrfs volumes, snapshots, and ESP location
2. **Analysis Phase**: Determines optimal configuration method (refind_linux.conf vs include files)
3. **Generation Phase**: Creates boot entries with proper kernel parameters and initrd paths
4. **Validation Phase**: Shows unified diff of all changes before applying
5. **Application Phase**: Updates configuration files atomically

## Installation

### From GitHub Releases (Recommended)

```bash
# Download latest release
curl -L -o refind-btrfs-snapshots \
  "https://github.com/jmylchreest/refind-btrfs-snapshots/releases/latest/download/refind-btrfs-snapshots_linux_amd64"

# Make executable and install
chmod +x refind-btrfs-snapshots
sudo mv refind-btrfs-snapshots /usr/bin/

# Install configuration file
sudo mkdir -p /etc
sudo curl -L -o /etc/refind-btrfs-snapshots.conf \
  "https://github.com/jmylchreest/refind-btrfs-snapshots/raw/main/configs/refind-btrfs-snapshots.yaml"

# Install systemd units (optional)
sudo curl -L -o /etc/systemd/system/refind-btrfs-snapshots.service \
  "https://github.com/jmylchreest/refind-btrfs-snapshots/raw/main/systemd/refind-btrfs-snapshots.service"
sudo curl -L -o /etc/systemd/system/refind-btrfs-snapshots.path \
  "https://github.com/jmylchreest/refind-btrfs-snapshots/raw/main/systemd/refind-btrfs-snapshots.path"
```

### From Source

```bash
# Clone and build
git clone https://github.com/jmylchreest/refind-btrfs-snapshots.git
cd refind-btrfs-snapshots
go build -o refind-btrfs-snapshots

# Install
sudo cp refind-btrfs-snapshots /usr/bin/
sudo cp configs/refind-btrfs-snapshots.yaml /etc/refind-btrfs-snapshots.conf
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
sudo refind-btrfs-snapshots list

# Enable automatic updates
sudo systemctl enable --now refind-btrfs-snapshots.path
```

## Configuration

### Configuration File Locations

Configuration files are searched in the following order of preference:

1. `--config` flag path (highest priority)
2. `/etc/refind-btrfs-snapshots.conf`
3. `$HOME/.config/refind-btrfs-snapshots.yaml`
4. `./refind-btrfs-snapshots.yaml` (lowest priority)

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

| Category                | Option                             | Default                      | Description                                              |
| ----------------------- | ---------------------------------- | ---------------------------- | -------------------------------------------------------- |
| **Snapshot Management** |                                    |                              |                                                          |
|                         | `snapshot.selection_count`         | `0`                          | Number of snapshots to include (0 = all)                 |
|                         | `snapshot.search_directories`      | `["/.snapshots"]`            | Directories to scan for snapshots                        |
|                         | `snapshot.max_depth`               | `3`                          | Maximum search depth in snapshot directories             |
|                         | `snapshot.writable_method`         | `"toggle"`                   | Method for writable snapshots: `toggle` or `copy`        |
|                         | `snapshot.destination_dir`         | `"/.refind-btrfs-snapshots"` | Directory for copied writable snapshots                  |
| **ESP Configuration**   |                                    |                              |                                                          |
|                         | `esp.uuid`                         | `""`                         | Specific ESP UUID (highest priority)                     |
|                         | `esp.auto_detect`                  | `true`                       | Enable automatic ESP detection                           |
|                         | `esp.mount_point`                  | `""`                         | Manual ESP path (lowest priority)                        |
| **rEFInd Integration**  |                                    |                              |                                                          |
|                         | `refind.config_path`               | `"/EFI/refind/refind.conf"`  | Path to main rEFInd config                               |
| **Behavior Controls**   |                                    |                              |                                                          |
|                         | `behavior.exit_on_snapshot_boot`   | `true`                       | Prevent operation when booted from snapshot              |
|                         | `behavior.cleanup_old_snapshots`   | `true`                       | Clean up old writable snapshots                          |
| **Logging**             |                                    |                              |                                                          |
|                         | `log_level`                        | `"info"`                     | Log verbosity: `trace`, `debug`, `info`, `warn`, `error` |
| **Advanced Options**    |                                    |                              |                                                          |
|                         | `advanced.naming.timestamp_format` | `"2006-01-02_15-04-05"`      | Timestamp format for writable snapshots                  |

For complete configuration reference, see [`configs/refind-btrfs-snapshots.yaml`](configs/refind-btrfs-snapshots.yaml).

## Commands

### `generate`

Generate rEFInd boot entries for btrfs snapshots.

```bash
sudo refind-btrfs-snapshots generate [flags]
```

**Key Flags:**

- `--dry-run` - Preview changes without applying them
- `--count, -n` - Limit number of snapshots (0 = all)
- `--generate-include, -g` - Force generation of include file
- `--yes, -y` - Auto-approve changes without interactive confirmation
- `--config, -c` - Specify custom configuration file path
- `--log-level` - Set logging verbosity

**Examples:**

```bash
# Preview changes for top 5 snapshots
sudo refind-btrfs-snapshots generate --dry-run --count 5

# Generate include file and auto-approve
sudo refind-btrfs-snapshots generate -g -y

# Use custom config with debug logging
sudo refind-btrfs-snapshots generate -c /path/to/config.yaml --log-level debug
```

### `list`

List available btrfs snapshots with metadata.

```bash
sudo refind-btrfs-snapshots list [flags]
```

**Flags:**

- `--format, -f` - Output format: `table` (default), `json`, `yaml`
- `--show-size` - Calculate and display snapshot sizes (slower)

**Examples:**

```bash
# Standard table output
sudo refind-btrfs-snapshots list

# JSON output with sizes
sudo refind-btrfs-snapshots list -f json --show-size

# YAML output
sudo refind-btrfs-snapshots list -f yaml
```

## Include File Management

### Understanding Include Files

When `refind_linux.conf` updates aren't suitable (e.g., custom kernel configurations), the tool generates a `refind-btrfs-snapshots.conf` include file. This provides maximum flexibility while keeping your main configuration clean.

### Generated Include File Structure

```bash
# Example: /boot/efi/EFI/refind/refind-btrfs-snapshots.conf
#
# Generated by refind-btrfs-snapshots
# This file contains boot entries for btrfs snapshots
#
# To enable snapshot booting, add this line to your refind.conf:
#   include refind-btrfs-snapshots.conf
#

menuentry "Arch Linux" {
    disabled
    icon     /EFI/refind/icons/os_arch.png
    loader   /boot/vmlinuz-linux
    initrd   /boot/initramfs-linux.img
    options  quiet splash rw rootflags=subvol=@ cryptdevice=UUID=0197662d-7906-7913-ade5-1d0f76c4f9a2:luks-0197662d-7906-7913-ade5-1d0f76c4f9a2 root=/dev/mapper/luks-0197662d-7906-7913-ade5-1d0f76c4f9a2

    # Snapshot submenus will be automatically generated below:
    submenuentry "Arch Linux (2025-06-12_20-00-05)" {
        options quiet splash rw rootflags=subvol=@/.snapshots/390/snapshot,subvolid=1046 cryptdevice=UUID=0197662d-7906-7913-ade5-1d0f76c4f9a2:luks-0197662d-7906-7913-ade5-1d0f76c4f9a2 root=/dev/mapper/luks-0197662d-7906-7913-ade5-1d0f76c4f9a2
    }
    submenuentry "Arch Linux (2025-06-12_19-00-17)" {
        options quiet splash rw rootflags=subvol=@/.snapshots/389/snapshot,subvolid=1044 cryptdevice=UUID=0197662d-7906-7913-ade5-1d0f76c4f9a2:luks-0197662d-7906-7913-ade5-1d0f76c4f9a2 root=/dev/mapper/luks-0197662d-7906-7913-ade5-1d0f76c4f9a2
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
ExecStart=/usr/bin/refind-btrfs-snapshots generate -g -y -n 10 -c /etc/custom-config.conf

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
# Systemd path should monitor /.snapshots
```

**Snapper Workflow:**

1. Snapper creates snapshots in `/.snapshots`
2. Systemd path unit detects changes
3. Boot entries are automatically generated
4. Snapshots appear in rEFInd menu

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

# Update systemd path unit to monitor Timeshift directory
```

**Timeshift Workflow:**

1. Configure systemd path to monitor Timeshift snapshot directory
2. Timeshift creates snapshots during scheduled backups
3. Tool automatically generates boot entries
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
    timestamp_format: "2006-01-02_15:04:05"
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
# List detected snapshots
sudo refind-btrfs-snapshots list --log-level debug

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

### Debug Mode

Enable detailed logging for troubleshooting:

```bash
sudo refind-btrfs-snapshots generate --log-level debug --dry-run
```

## License

GPL-3.0 License - see [LICENSE](LICENSE) for details.

## Author

**John Mylchreest** <jmylchreest@gmail.com>

## Links

- **GitHub Repository**: https://github.com/jmylchreest/refind-btrfs-snapshots
- **Issue Tracker**: https://github.com/jmylchreest/refind-btrfs-snapshots/issues
- **Releases**: https://github.com/jmylchreest/refind-btrfs-snapshots/releases
- **AUR Package**: https://aur.archlinux.org/packages/refind-btrfs-snapshots-bin
