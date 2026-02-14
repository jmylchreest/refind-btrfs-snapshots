# rEFInd Btrfs Snapshots

Automatically generate rEFInd boot menu entries for btrfs snapshots.

Discovers your btrfs snapshots, detects each snapshot's boot mode (ESP or btrfs), manages kernel staleness, and generates rEFInd configuration — all with a single command or automatically via systemd.

For full documentation, see the [Usage Guide](docs/USAGE.md).

## Installation

### Arch Linux (AUR)

```bash
yay -S refind-btrfs-snapshots-bin
```

### From GitHub Releases

```bash
# Download latest release (amd64)
curl -L -o refind-btrfs-snapshots \
  "https://github.com/jmylchreest/refind-btrfs-snapshots/releases/latest/download/refind-btrfs-snapshots-linux-amd64"

# For arm64:
# curl -L -o refind-btrfs-snapshots \
#   "https://github.com/jmylchreest/refind-btrfs-snapshots/releases/latest/download/refind-btrfs-snapshots-linux-arm64"

chmod +x refind-btrfs-snapshots
sudo mv refind-btrfs-snapshots /usr/bin/

# Install configuration
sudo curl -L -o /etc/refind-btrfs-snapshots.yaml \
  "https://github.com/jmylchreest/refind-btrfs-snapshots/raw/main/configs/refind-btrfs-snapshots.yaml"

# Install systemd units
sudo curl -L -o /usr/lib/systemd/system/refind-btrfs-snapshots.service \
  "https://github.com/jmylchreest/refind-btrfs-snapshots/releases/latest/download/refind-btrfs-snapshots.service"
sudo curl -L -o /usr/lib/systemd/system/refind-btrfs-snapshots.path \
  "https://github.com/jmylchreest/refind-btrfs-snapshots/releases/latest/download/refind-btrfs-snapshots.path"
```

### From Source

```bash
git clone https://github.com/jmylchreest/refind-btrfs-snapshots.git
cd refind-btrfs-snapshots
go build -o refind-btrfs-snapshots
sudo cp refind-btrfs-snapshots /usr/bin/
sudo cp configs/refind-btrfs-snapshots.yaml /etc/refind-btrfs-snapshots.yaml
sudo cp systemd/*.{service,path} /etc/systemd/system/
```

## Setup

### 1. Generate boot entries

```bash
# Preview what will happen
sudo refind-btrfs-snapshots generate --dry-run

# Generate for real (creates refind-btrfs-snapshots.conf include file)
sudo refind-btrfs-snapshots generate -g -y
```

### 2. Include in rEFInd config

Add this line to your `refind.conf`:

```
include refind-btrfs-snapshots.conf
```

### 3. Enable automatic updates

```bash
sudo systemctl enable --now refind-btrfs-snapshots.path
```

This watches `/.snapshots` and regenerates boot entries automatically when snapshots change.

## Configuration

Edit `/etc/refind-btrfs-snapshots.yaml`. The defaults work for most Snapper setups. Key options:

| Option | Default | Description |
|--------|---------|-------------|
| `snapshot.search_directories` | `["/.snapshots"]` | Where to look for snapshots |
| `snapshot.selection_count` | `0` | How many snapshots to include (0 = all) |
| `esp.auto_detect` | `true` | Auto-detect the EFI System Partition |
| `kernel.stale_snapshot_action` | `"warn"` | What to do with stale snapshots: `warn`, `disable`, `delete`, `fallback` |

See the [annotated config file](configs/refind-btrfs-snapshots.yaml) for all options, or the [Usage Guide](docs/USAGE.md#configuration-reference) for full documentation.

## FAQ

**Q: I have `/boot` on a separate ESP partition. Will snapshots break after a kernel upgrade?**

Not silently. The tool detects this (ESP mode) and checks whether each snapshot's kernel modules match the kernel on the ESP. If they don't match, the configured `stale_snapshot_action` controls what happens — warn, disable the entry, remove it, or switch to the fallback initramfs.

**Q: I have `/boot` as part of my btrfs root subvolume. Does staleness checking apply?**

No. The tool detects this (btrfs mode) and knows the snapshot contains its own kernel and initramfs. Staleness is impossible — the kernel and modules are always in sync within the snapshot. Boot entries use rEFInd's btrfs driver to load the kernel directly from inside the snapshot.

**Q: My ESP is mounted at `/boot/efi`, not `/boot`. Does that affect boot mode detection?**

No. Boot mode is determined by whether `/boot` itself is a separate non-btrfs mount in the snapshot's fstab. An ESP at `/boot/efi` means `/boot` is still part of the btrfs root, so snapshots are btrfs mode. An ESP at `/boot` means `/boot` is separate, so snapshots are ESP mode.

**Q: Does this work with `refind_linux.conf`?**

Only for ESP-mode snapshots. `refind_linux.conf` relies on rEFInd's auto-detected kernel paths and can only override `options` — it can't set the `volume`, `loader`, and `initrd` overrides that btrfs-mode entries need. Use the include file approach (`generate -g`) instead, which handles both modes.

**Q: Can a single boot menu contain both ESP-mode and btrfs-mode snapshots?**

Yes. If your system transitioned between boot configurations over time, older snapshots retain their original mode. Each snapshot's own `/etc/fstab` determines its mode independently.

## Links

- [Usage Guide](docs/USAGE.md) — full documentation
- [Configuration File](configs/refind-btrfs-snapshots.yaml) — annotated defaults
- [Issue Tracker](https://github.com/jmylchreest/refind-btrfs-snapshots/issues)
- [Releases](https://github.com/jmylchreest/refind-btrfs-snapshots/releases)
- [AUR Package](https://aur.archlinux.org/packages/refind-btrfs-snapshots-bin)
