# Wishlist

Planned features and capabilities that aren't built yet. Items here are **not commitments** — they capture intent, surface design tradeoffs, and let us iterate on shape before code. Anything implementation-ready graduates out of this file and into an issue or PR.

## Table of Contents

- [`uki-btrfs-snapshots` binary (phase 3)](#uki-btrfs-snapshots-binary-phase-3)
  - [Intention](#intention)
  - [Outcome](#outcome)
  - [Approach](#approach)
  - [Secure Boot](#secure-boot)
  - [Open questions](#open-questions)

---

## `uki-btrfs-snapshots` binary (phase 3)

A third sibling to `refind-btrfs-snapshots` and `bls-btrfs-snapshots` that makes btrfs snapshots bootable when the kernel is delivered as a UKI (Unified Kernel Image).

### Intention

A UKI bundles kernel + initramfs + cmdline + os-release into a single signed PE binary that the firmware (or systemd-boot) can chainload directly. The cmdline lives **inside** the signed image as the `.cmdline` PE section, so neither the rEFInd nor the bls binary can make UKI-booting systems snapshot-bootable: they emit external bootloader config that proposes a cmdline, but the UKI ignores it. The cmdline that runs is whatever was baked into the UKI at build time.

To make a snapshot bootable on a UKI-only system, we have to **produce a UKI per snapshot** with a per-snapshot cmdline embedded.

### Outcome

Per (snapshot × UKI source), emit one cloned UKI to `<esp>/EFI/Linux/<prefix><snap-id>-<src-name>.efi` with:

- `.linux` — same kernel as the source UKI (we don't rebuild kernels)
- `.initrd` — same initramfs blob (microcode already concatenated if the source had it)
- `.osrel` — same os-release
- `.uname` — same kernel version string
- `.cmdline` — **rewritten** with `rootflags=subvol=<snap-path>,subvolid=<snap-id>` per snapshot
- `.sbat` — same (revocation metadata; doesn't depend on cmdline)

Existing snapshot-fstab alignment (`internal/snapshotfs.UpdateFstabs`) applies unchanged.

The binary follows the same shell as `bls-btrfs-snapshots`: cobra root + `generate`/`version` subcommands, opt-in `uki.write_entries` gate, dry-run / `--yes` / `--force` flags, the shared `cliconfig` + `internal/version` plumbing, the `internal/bootloader.Generator` interface, snapshot fstab alignment via the shared `snapshotfs` helper.

### Approach

Three editing strategies were tested empirically:

| Strategy | Result | Verdict |
|---|---|---|
| `objcopy --update-section .cmdline=newfile` | Truncates: new content cut to old section size. PE sections have fixed allocated size and can't grow in-place. | Unusable — our rewritten cmdline is almost always longer than the source's |
| `objcopy --remove-section .cmdline` + `--add-section` | Section size correct, but objcopy warns `section below image base`; PE layout integrity uncertain without a real boot test | Risky |
| Extract sections via objcopy + rebuild via `ukify build` | Clean output, sha256-stable, valid UKI by construction (ukify handles section layout, alignment, signing path) | **Recommended** |

Recommended flow per (snapshot × source UKI):

```bash
# 1. Pull the immutable sections out of the source.
objcopy -O binary --only-section=.linux  source.efi tmp/linux
objcopy -O binary --only-section=.initrd source.efi tmp/initrd
objcopy -O binary --only-section=.osrel  source.efi tmp/osrel
objcopy -O binary --only-section=.uname  source.efi tmp/uname
objcopy -O binary --only-section=.sbat   source.efi tmp/sbat   # if present

# 2. Compute the per-snapshot cmdline using the same rewriter the bls binary uses.
NEW_CMDLINE="$(internal/bls/snapshot.go:rewriteCmdline equivalent for UKI)"

# 3. Rebuild via ukify, passing through the extracted sections and the new cmdline.
ukify build \
  --linux=tmp/linux \
  --initrd=tmp/initrd \
  --os-release=@tmp/osrel \
  --uname="$(tr -d '\0' < tmp/uname)" \
  --sbat=@tmp/sbat \
  --cmdline="$NEW_CMDLINE" \
  --output=<esp>/EFI/Linux/<prefix><snap-id>-<src-name>.efi
```

`ukify` ships with systemd (≥ v253). Detect at runtime; refuse cleanly with a pointer to the distro package (`systemd-ukify` on Arch, etc.) if missing.

Practical concerns to surface in v1:

| Concern | Mitigation |
|---|---|
| **Disk usage** — each UKI clone is a full ~70 MB. 25 snapshots × ~70 MB ≈ 1.75 GB on the ESP. | `snapshot.selection_count` becomes load-bearing. Detect ESP free space before applying and refuse if insufficient. Surface the per-snapshot size in dry-run output. |
| **Runtime dependency on `ukify`** | Detect at startup, error early with an actionable message. |
| **Build cost** — ~2–3 s per UKI (compression dominates) | Progress logs per snapshot. Total for 25 snapshots: under a minute. Acceptable for a hourly-triggered service. |
| **Microcode** | Source UKIs already concatenate microcode into the single `.initrd` section. Pass the extracted blob back to ukify verbatim — no microcode-specific handling needed. |

### Secure Boot

**ukify natively handles Secure Boot signing** — we don't reimplement anything. Relevant flags:

| ukify flag | Purpose |
|---|---|
| `--secureboot-private-key=PATH` | Private key for the PE signature |
| `--secureboot-certificate=PATH` | Cert for the PE signature |
| `--signtool={sbsign,pesign,systemd-sbsign}` | Choose the signing backend |
| `--sign-kernel` / `--no-sign-kernel` | Sign the embedded kernel separately (some SB shim configurations want this) |
| `--pcr-private-key=PATH` | Private key for PCR signing (TPM2 measured boot) |
| `--pcr-public-key=PATH` / `--pcr-certificate=PATH` | Verification material for PCR sigs |
| `--measure` / `--no-measure` | Whether to compute PCR measurements during build |
| `--sign-profile ID` | Which PCR profile to sign |

So once we plumb config keys for these (`uki.signing.key_path`, `uki.signing.cert_path`, `uki.signing.signtool`, `uki.pcr.key_path`, etc.) and forward them as ukify flags, Secure Boot support is essentially free.

**Phased rollout:**

- **v1 (initial release):** detect Secure Boot via `/sys/firmware/efi/efivars/SecureBoot-*`; if enabled and signing config is absent, refuse cleanly with a message pointing at the config knobs that will become available in v2. Don't silently ship unsigned UKIs the firmware will reject.
- **v2:** wire the signing config through. The user provides their existing SB key/cert (the same ones their `kernel-install` setup uses), and the per-snapshot UKIs get signed identically.
- **v3 (if anyone asks):** PCR signing for measured-boot setups. Note: changing the cmdline changes the PCR measurements, so TPM-sealed disks would fail to unlock for a snapshot-booted system unless the user's PCR policy explicitly enrolls the snapshot UKIs. This is fiddly and probably wants opt-in plus clear docs.

### Open questions

- **Cleanup model:** how aggressively do we remove orphan UKI clones? The bls binary cleans up by prefix-match. Same pattern here, but with ~70 MB per file the consequences of leaving orphans around are different from the bls binary's ~1 KB `.conf` files.
- **Naming:** does `bls-btrfs-snapshots-` style prefix apply, or do we use `uki-btrfs-snapshots-` to keep them clearly separable from any user-managed UKIs?
- **Stub override:** ukify defaults to `/usr/lib/systemd/boot/efi/linuxx64.efi.stub`. Some users build UKIs with a custom stub (e.g., shim-aware variants). Detect the source UKI's stub and reuse it, or always use the system stub? Probably reuse — preserves whatever security profile the original was built with.
- **Cross-arch:** the systemd stub is per-arch. For aarch64 boxes we'd need `linuxaa64.efi.stub`. The matrix is already arch-aware (we build the binary for amd64/arm64); the runtime just picks the stub matching the running architecture.
- **Coexistence with `kernel-install`:** distro `kernel-install` plugins regenerate the primary UKI on every kernel rebuild. Our managed clones (with the managed prefix) won't conflict with the primary, but we need to re-trigger after a kernel rebuild. The systemd `.path` unit watching `/.snapshots/` doesn't catch kernel rebuilds — we'd want a second trigger watching `/boot/EFI/Linux/` for source UKI changes. Worth thinking about before phase 3 ships.
