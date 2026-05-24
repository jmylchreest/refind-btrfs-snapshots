# Usage Examples

This document provides practical examples of using refind-btrfs-snapshots in various scenarios.

## Table of Contents

- [Basic Usage](#basic-usage)
- [Configuration Examples](#configuration-examples)
- [Integration Examples](#integration-examples)
- [Advanced Scenarios](#advanced-scenarios)
- [Troubleshooting Examples](#troubleshooting-examples)

## Basic Usage

### First Time Setup

```bash
# 1. Install the application
sudo make install-system

# 2. Check your current setup
sudo refind-btrfs-snapshots list

# 3. Generate boot entries (dry run first)
sudo refind-btrfs-snapshots generate --dry-run

# 4. Generate actual boot entries
sudo refind-btrfs-snapshots generate
```

### Daily Usage

```bash
# Check available snapshots
sudo refind-btrfs-snapshots list

# Generate boot menu with latest 3 snapshots
sudo refind-btrfs-snapshots generate --count 3

# Force generation even if booted from snapshot
sudo refind-btrfs-snapshots generate --force
```

## Configuration Examples

### Snapper Integration

Configuration for systems using Snapper:

```yaml
# /etc/refind-btrfs.conf
snapshot:
  search_directories:
    - "/.snapshots"
  max_depth: 3
  selection_count: 5
  destination_dir: "/root/.refind-btrfs"
  create_writable: true

refind:
  config_path: "/EFI/refind/refind.conf"
  include_submenus: true
  output_dir: "btrfs-snapshot-stanzas"

esp:
  auto_detect: true
  mount_point: "/boot/efi"
```

Usage:
```bash
# Generate boot entries for Snapper snapshots
sudo refind-btrfs-snapshots generate

# List only bootable snapshots
sudo refind-btrfs-snapshots list
```

### Timeshift Integration

Configuration for systems using Timeshift:

```yaml
# /etc/refind-btrfs.conf
snapshot:
  search_directories:
    - "/run/timeshift/backup/timeshift-btrfs/snapshots"
  max_depth: 3
  selection_count: 5
  destination_dir: "/root/.refind-btrfs"
  create_writable: true

behavior:
  exit_on_snapshot_boot: true
  cleanup_old_snapshots: true
```

Usage:
```bash
# Generate with Timeshift snapshots
sudo refind-btrfs-snapshots --config /etc/refind-btrfs.conf generate

# List all available Timeshift snapshots
sudo refind-btrfs-snapshots list --all
```

### Custom Snapshot Directories

For systems with custom snapshot locations:

```yaml
snapshot:
  search_directories:
    - "/snapshots"
    - "/backup/btrfs-snapshots"
    - "/.snapshots"
  max_depth: 4
  selection_count: 10
```

Usage:
```bash
# Override search directories temporarily
sudo refind-btrfs-snapshots list --search-dirs "/custom/snapshots,/another/location"

# Generate with custom directories
sudo refind-btrfs-snapshots generate --count 7
```

## Integration Examples

### Automated with Systemd

Set up automatic generation after snapshot creation:

```bash
# Install the service
sudo cp systemd/refind-btrfs.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable refind-btrfs.service

# Run manually
sudo systemctl start refind-btrfs.service

# Check status
sudo systemctl status refind-btrfs.service

# View logs
sudo journalctl -u refind-btrfs.service -f
```

### Cron Integration

Add to root's crontab for regular updates:

```bash
# Edit root crontab
sudo crontab -e

# Add entries for different schedules:

# Every night at 2 AM
0 2 * * * /usr/local/bin/refind-btrfs-snapshots generate >/dev/null 2>&1

# After each reboot
@reboot /usr/local/bin/refind-btrfs-snapshots generate

# Every hour (for active development)
0 * * * * /usr/local/bin/refind-btrfs-snapshots generate --count 3
```

### Snapper Hook Integration

Create a Snapper hook to automatically update boot entries:

```bash
# Create hook script
sudo tee /etc/snapper/scripts/refind-btrfs-hook.sh > /dev/null << 'EOF'
#!/bin/bash
# Snapper hook to update rEFInd boot entries

SNAPPER_TYPE="$1"
SNAPPER_CONFIG="$2"
SNAPPER_NUM1="$3"
SNAPPER_NUM2="$4"

# Only run for root config and post-create events
if [[ "$SNAPPER_CONFIG" == "root" ]] && [[ "$SNAPPER_TYPE" == "post" ]]; then
    /usr/local/bin/refind-btrfs-snapshots generate --count 5 || true
fi
EOF

sudo chmod +x /etc/snapper/scripts/refind-btrfs-hook.sh
```

## Advanced Scenarios

### Multiple ESP Configurations

For systems with multiple ESPs or custom ESP locations:

```bash
# Specify ESP manually
sudo refind-btrfs-snapshots generate --esp-path /boot/efi

# Use specific ESP UUID in config
esp:
  auto_detect: false
  uuid: "1234-5678"
  mount_point: "/custom/efi"
```

### Encrypted Root with Separate Boot

Configuration for LUKS-encrypted root with separate /boot:

```yaml
snapshot:
  search_directories:
    - "/.snapshots"
  create_writable: true
  destination_dir: "/root/.refind-btrfs"

refind:
  config_path: "/boot/efi/EFI/refind/refind.conf"
  include_submenus: true

esp:
  mount_point: "/boot/efi"
```

Example rEFInd entry that works with encryption:
```
menuentry "Arch Linux" {
    icon /EFI/refind/icons/os_arch.png
    volume ARCH
    loader /vmlinuz-linux
    initrd /initramfs-linux.img
    options "cryptdevice=/dev/sda2:cryptroot root=/dev/mapper/cryptroot rootflags=subvol=@ rw"
}
```

### Custom Icon Support (Future Feature)

Configuration for custom snapshot icons:

```yaml
advanced:
  icon:
    enabled: true
    custom_path: "/boot/efi/EFI/refind/icons/snapshot.png"
    embed_btrfs_logo: true
```

### Read-Only Snapshot Handling

For systems preferring to modify snapshot read-only flags:

```yaml
snapshot:
  create_writable: false  # Don't create new snapshots
  selection_count: 3

behavior:
  cleanup_old_snapshots: false  # Don't clean up since we're not creating new ones
```

Usage:
```bash
# This will modify read-only flags instead of creating new snapshots
sudo refind-btrfs-snapshots generate
```

## Troubleshooting Examples

### Debug Mode

Enable detailed logging for troubleshooting:

```bash
# Enable debug logging
sudo refind-btrfs-snapshots --log-level debug generate

# Very verbose output
sudo refind-btrfs-snapshots --log-level trace list --all

# Debug with dry run
sudo refind-btrfs-snapshots --log-level debug generate --dry-run --verbose
```

### ESP Detection Issues

When ESP auto-detection fails:

```bash
# Manually check ESP candidates
sudo lsblk -o NAME,SIZE,TYPE,MOUNTPOINT,UUID,FSTYPE,PARTTYPE

# Force specific ESP
sudo refind-btrfs-snapshots generate --esp-path /boot/efi

# Use ESP UUID in config
esp:
  auto_detect: false
  uuid: "A1B2-C3D4"
```

### Snapshot Not Found

When snapshots aren't detected:

```bash
# Check what directories are being searched
sudo refind-btrfs-snapshots --log-level debug list

# Search in all directories
sudo refind-btrfs-snapshots list --all --show-size

# Override search locations
sudo refind-btrfs-snapshots list --search-dirs "/.snapshots,/snapshots,/timeshift"
```

### Permission Issues

Handling permission-related problems:

```bash
# Check ESP permissions
sudo ls -la /boot/efi/EFI/refind/

# Test ESP write access
sudo touch /boot/efi/test && sudo rm /boot/efi/test

# Check if ESP is mounted read-only
mount | grep efi

# Remount ESP read-write if needed
sudo mount -o remount,rw /boot/efi
```

### Configuration Validation

Validate your configuration before running:

```bash
# Test configuration syntax
sudo refind-btrfs-snapshots --config /etc/refind-btrfs.conf list --dry-run

# Check if all required directories exist
sudo refind-btrfs-snapshots --log-level debug generate --dry-run

# Validate rEFInd config parsing
sudo refind-btrfs-snapshots --log-level debug generate --dry-run --verbose
```

### Recovery Scenarios

When things go wrong:

```bash
# Remove generated configurations
sudo rm -rf /boot/efi/EFI/refind/btrfs-snapshot-stanzas/

# Remove include directives from main config
sudo sed -i '/include btrfs-snapshot-stanzas/d' /boot/efi/EFI/refind/refind.conf

# Clean up old writable snapshots
sudo btrfs subvolume list /root/.refind-btrfs/
sudo btrfs subvolume delete /root/.refind-btrfs/rwsnap_*

# Regenerate from scratch
sudo refind-btrfs-snapshots generate --force
```

## Output Examples

### Successful Generation

```
$ sudo refind-btrfs-snapshots generate
INFO[0000] Starting rEFInd btrfs snapshot generation
INFO[0001] Using configured ESP path                    path=/boot/efi
INFO[0001] Found root btrfs filesystem                  device=/dev/nvme0n1p2 subvolume=@ uuid=a1b2c3d4-e5f6-7890-abcd-ef1234567890
INFO[0002] Selected snapshots for processing            selected=5 total=12
INFO[0003] Creating writable snapshot                    source=/.snapshots/123/snapshot
INFO[0004] Updated snapshot fstab                        path=/.snapshots/123/snapshot/etc/fstab
INFO[0005] Parsed rEFInd configuration                   config_path=/boot/efi/EFI/refind/refind.conf entries=3
INFO[0005] Found suitable boot entries                   entries=1
INFO[0006] Generating snapshot config                    entry="Arch Linux" snapshots=5
INFO[0006] Added include directive                       directive="include btrfs-snapshot-stanzas/arch_vmlinuz-linux.conf"
INFO[0006] Successfully generated rEFInd snapshot configurations
```

### List Output

```
$ sudo refind-btrfs-snapshots list
PATH                            CREATED              ID    READ-ONLY
/.snapshots/123/snapshot        2024-01-15 10:30:45  567   Yes
/.snapshots/122/snapshot        2024-01-15 09:15:22  566   Yes
/.snapshots/121/snapshot        2024-01-15 08:00:10  565   Yes
/.snapshots/120/snapshot        2024-01-14 22:45:33  564   Yes
/.snapshots/119/snapshot        2024-01-14 20:30:15  563   Yes
```

### JSON Output

```bash
$ sudo refind-btrfs-snapshots list --format json
{
  "snapshots": [
    {
      "path": "/.snapshots/123/snapshot",
      "id": 567,
      "created": "2024-01-15T10:30:45Z",
      "is_readonly": true,
      "original_path": "@"
    },
    {
      "path": "/.snapshots/122/snapshot", 
      "id": 566,
      "created": "2024-01-15T09:15:22Z",
      "is_readonly": true,
      "original_path": "@"
    }
  ]
}
```
