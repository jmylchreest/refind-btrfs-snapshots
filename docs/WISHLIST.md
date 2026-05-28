# Wishlist

Planned features and capabilities that aren't built yet. Items here are **not commitments** — they capture intent, surface design tradeoffs, and let us iterate on shape before code. Anything implementation-ready graduates out of this file and into an issue or PR.

## Table of Contents

- [`uki-btrfs-snapshots` binary](#uki-btrfs-snapshots-binary)
  - [The fundamental problem](#the-fundamental-problem)
  - [Two implementation modes](#two-implementation-modes)
    - [Config shape](#config-shape)
    - [Profile display names](#profile-display-names)
  - [Bootloader / firmware support matrix](#bootloader--firmware-support-matrix)
  - [Mode 1: Cloned UKIs per snapshot (default)](#mode-1-cloned-ukis-per-snapshot-default)
  - [Mode 2: Multi-profile UKI (opt-in)](#mode-2-multi-profile-uki-opt-in)
  - [Secure Boot](#secure-boot)
  - [Open questions](#open-questions)

---

## `uki-btrfs-snapshots` binary

A third sibling to `refind-btrfs-snapshots` and `bls-btrfs-snapshots` that makes btrfs snapshots bootable when the kernel is delivered as a UKI (Unified Kernel Image).

### The fundamental problem

A UKI bundles kernel + initramfs + cmdline + os-release into a single PE binary that the firmware (or a boot loader) chainloads. The cmdline lives **inside** the image as the `.cmdline` PE section. Under Secure Boot the embedded cmdline is authoritative — the boot loader cannot override it; without Secure Boot the systemd-stub *may* accept boot-loader-supplied cmdline, but relying on that defeats the security model anyway.

A snapshot-bootable cmdline is `rootflags=subvol=<snap-path>,subvolid=<snap-id>` — it must change per snapshot. So neither the rEFInd nor the bls binary makes UKI hosts snapshot-bootable: external `options=` strings are ignored. We need to put the snapshot-targeted cmdline **inside** a UKI that the boot path will actually execute.

### Two implementation modes

The community has two working answers; this binary supports both, **independently selectable as a list** so users can run either or both at once. They differ in storage cost and in which boot paths can consume them:

| Mode | Per-snapshot disk cost | Works under SB | Boot paths that can select per snapshot |
|---|---|---|---|
| **1. Cloned UKIs** (one .efi per snapshot) | ~70 MB each (kernel + initrd duplicated) | Yes (re-sign each clone) | Anything that can launch a `.efi` from the ESP — direct firmware boot, refind, systemd-boot, GRUB chainload, limine. **Universal.** |
| **2. Multi-profile UKI** (single .efi with N `.profile` sections) | ~few KB each (only `.cmdline` per profile; kernel/initrd shared) | Yes (single signed image) | Only paths that pass an `@N` profile prefix to the sd-stub. **Currently: systemd-boot with sd-stub; manually-crafted refind entries.** |

The two are not mutually exclusive. Plausible combinations:

- **`clone` only** — universal compatibility, accept disk cost. Most conservative default.
- **`multi-profile` only** — systemd-boot users on tight ESPs; cheapest per-snapshot.
- **Both** — multi-profile for the systemd-boot menu (cheap, many profiles), plus a small number of clones as a universal recovery fallback (e.g., last 3 known-good).

#### Config shape

```yaml
uki:
  # Which mode(s) to apply. Both are independent; both are enabled by default
  # so installing the binary gives you the full coverage matrix: multi-profile
  # for systemd-boot menu integration with sd-stub, plus 10 universal-fallback
  # clones for direct-firmware boot or systemd-boot menu recovery.
  modes: [clone, multi-profile]

  clone:
    # Cap on how many snapshots get cloned. Each clone is a full ~70 MB .efi
    # file (kernel + initrd duplicated; only .cmdline differs from the source
    # UKI). Pick whichever boot-set's kernel each cloned snapshot was taken
    # under — cloning is per (snapshot × associated kernel), NOT per snapshot
    # × every kernel. The binary runs a mandatory pre-flight ESP free-space
    # check and refuses to apply if the projected set wouldn't fit alongside
    # what's already on the ESP.
    # 0 = unlimited (inherits snapshot.selection_count — caller beware).
    recent: 4

  multi_profile:
    # Cap on profiles joined to the base UKI. One base UKI per kernel; each
    # carries one .profile section per eligible snapshot. Profiles are cheap
    # (~few KB each) so this defaults unlimited — the practical cap is
    # snapshot.selection_count.
    # 0 = inherit snapshot.selection_count.
    recent: 0

  # Signing: see Secure Boot section.
  signing:
    key_path: ""
    cert_path: ""
```

**Default footprint** for a single-kernel system with 25 selected snapshots:
- Multi-profile: **0 MB additional** — operates in-place on the live UKI, only adds ~50 KB of `.profile` sections (kernel + initrd remain a single shared copy)
- Clones: **4 × ~70 MB ≈ 280 MB** new files
- **Total ≈ 280 MB additional disk, 5 `.efi` files on the ESP** (1 live multi-profile UKI + 4 cloned UKIs).

For two-kernel systems: still ~280 MB (multi-profile is in-place on both live UKIs; 4 clones map to whichever kernel each clone's snapshot was taken under — typically the current kernel for recent snapshots). 6 `.efi` files total.

This comfortably fits a 512 MB ESP with room for other content. The pre-flight check makes "doesn't fit" a hard, actionable error rather than a silent overflow — the binary refuses to apply with `ESP has X MB free, need Y MB. Reduce uki.clone.recent or free space.`

If the default is still too aggressive: setting `modes: [multi-profile]` alone drops to ~0 MB additional (in-place only); setting `clone.recent: 2` halves the clone cost; setting `modes: []` opts out entirely.

The implementation note that makes mode 2 free: ukify's `--join-profile` lets us inject profile sections into the **existing** live UKI rather than write a parallel file. kernel-install will clobber those profile sections on every kernel upgrade, so the package ships a `90-uki-btrfs-snapshots.install` kernel-install hook that re-runs the join after each kernel update.

#### Profile display names

Each `.profile` section in a multi-profile UKI carries metadata fields from the [UAPI spec](https://uapi-group.org/specifications/specs/unified_kernel_image/):

| Field | Purpose |
|---|---|
| `ID=` | Brief 7-bit ASCII identifier — used as the value passed via `@<ID>` (or `@<index>`) to sd-stub at boot |
| `TITLE=` | Human-readable text for boot menu display |

So each snapshot's profile can carry its own title — we'd set `TITLE=Linux-cachyos (2026-05-27T16:00:00Z)` (same format as the bls binary's entry titles, honouring `advanced.naming.menu_format` + `display.local_time`). Where that title actually surfaces depends on the consumer:

- **systemd-boot** with native profile-menu synthesis: TITLE shows as a top-level menu entry. (Per the UAPI spec this is "may", not "does" — depends on systemd-boot version.)
- **systemd-boot** with hand-written BLS `.conf` per profile: the `.conf`'s own `title` line wins; the profile's TITLE is just metadata stored in the UKI.
- **refind** with manual `refind.conf` per profile: same — refind's menu name comes from its own config.

For mode 1 (clones), each clone is just a normal UKI; its "title" in a boot menu is whatever the surrounding BLS `.conf` or `refind.conf` entry declares.

### Bootloader / firmware support matrix

What "selects a per-snapshot variant" looks like for each consumer, today:

| Consumer | Cloned UKI | Multi-profile UKI |
|---|---|---|
| **UEFI firmware direct boot** (`efibootmgr` entry per UKI) | ✅ One `BootXXXX` per snapshot — clunky but works | ❌ Firmware has no `@N` selector; always boots profile @0 |
| **systemd-boot** (sd-stub ≥ 256) | ✅ One BLS `.conf` per UKI, auto-discovered from `<esp>/EFI/Linux/` | 🟡 [Native menu synthesis from profiles is documented as "may", not "does"](https://uapi-group.org/specifications/specs/unified_kernel_image/). Practical path today: write one BLS `.conf` per profile with `options @N …` |
| **rEFInd** | ✅ Autodetects `.efi` under `EFI/Linux/` | 🟡 Manual `refind.conf` entries per profile with `options "@N …"` — refind has no native profile awareness |
| **GRUB** (2.14+) | 🟡 Chainload via `chainloader` directive | ❌ Multi-profile support [explicitly not present per release notes](https://forum.manjaro.org/t/grub-2-14-uki-support/184899) |
| **limine** | 🟡 Manual `limine.conf` per UKI (no autodetect) | ❌ No profile awareness |

The realistic picture: **multi-profile is currently a systemd-boot story.** The matrix should improve over time as bootloaders adopt the spec; cloned UKIs remain the universal fallback indefinitely.

### Mode 1: Cloned UKIs per snapshot (default)

**Output:** per (snapshot × source UKI), emit `<esp>/EFI/Linux/<prefix><snap-id>-<src-name>.efi` containing:

- `.linux` — same kernel as source (we don't rebuild kernels)
- `.initrd` — same initramfs blob (microcode already concatenated)
- `.osrel`, `.uname`, `.sbat` — copied from source
- `.cmdline` — **rewritten** with `rootflags=subvol=…,subvolid=…` per snapshot

**Build:** three editing strategies were tested empirically — only one works cleanly:

| Strategy | Result |
|---|---|
| `objcopy --update-section .cmdline=newfile` | Truncates: new content cut to old section size. PE sections have fixed allocated size and can't grow in-place. |
| `objcopy --remove-section .cmdline` + `--add-section` | Warns about PE layout integrity; uncertain without a real boot test. |
| Extract sections via objcopy + rebuild via `ukify build` | Clean output, sha256-stable, valid by construction. **Recommended.** |

Pseudocode per (snapshot × source UKI):

```bash
mkdir tmp/
objcopy -O binary --only-section=.linux  source.efi tmp/linux
objcopy -O binary --only-section=.initrd source.efi tmp/initrd
objcopy -O binary --only-section=.osrel  source.efi tmp/osrel
objcopy -O binary --only-section=.uname  source.efi tmp/uname
objcopy -O binary --only-section=.sbat   source.efi tmp/sbat   # if present

ukify build \
  --linux=tmp/linux --initrd=tmp/initrd \
  --os-release=@tmp/osrel --uname="$(tr -d '\0' < tmp/uname)" \
  --sbat=@tmp/sbat \
  --cmdline="$(rewriteCmdline)" \
  --output=<esp>/EFI/Linux/<prefix><snap-id>-<src-name>.efi
```

**Costs/constraints:**

| Concern | Mitigation |
|---|---|
| Each clone is ~70 MB. 25 snapshots × ~70 MB ≈ 1.75 GB on the ESP. | `uki.clone.recent` caps the count separately from `snapshot.selection_count`; default is 4 (≈280 MB) — fits a 512 MB ESP with headroom. Pre-flight check: compute the required size and refuse if the ESP doesn't have headroom. |
| Runtime dep on `ukify` (systemd ≥ v253) | Detect at startup; error early with package hint. |
| Build cost ~2-3 s per UKI | Show per-snapshot progress logs. |
| Choosing which N to clone when `recent` < total snapshots | Newest-first by `SnapshotTime`. Same ordering the bls binary uses. |

### Mode 2: Multi-profile UKI (opt-in)

**Output:** one `<esp>/EFI/Linux/<src-name>.efi` containing:

- Base sections (`.linux`, `.initrd`, `.osrel`, `.uname`, `.sbat`) once
- One `.profile` section per snapshot, each carrying its own `.cmdline` (a few hundred bytes vs. ~70 MB)
- `.profile` metadata fields per [UAPI spec](https://uapi-group.org/specifications/specs/unified_kernel_image/):
  - `ID=<snap-id>`
  - `TITLE=<human-readable-snapshot-name>`

The PE format defined by the UAPI spec: repeated `.profile` sections act as separators. Sections appearing before the first `.profile` are the base; sections between `.profile` markers belong to that profile. sd-stub measures only base + selected profile into PCR 12.

**Build:** ukify natively supports this:

```bash
# 1. Build the base UKI as today (or take the system's existing UKI).
# 2. For each snapshot, build a "mini-UKI" containing only its profile metadata
#    and rewritten cmdline.
ukify build \
  --profile="ID=snap-N TITLE=Snapshot N (timestamp)" \
  --cmdline="<rewritten>" \
  --output=tmp/profile-N.efi

# 3. Join the per-snapshot profiles into the base UKI in one shot.
ukify build \
  --linux=… --initrd=… --cmdline=<live cmdline> \
  --join-profile=tmp/profile-1.efi \
  --join-profile=tmp/profile-2.efi \
  ... \
  --output=<esp>/EFI/Linux/<src-name>.efi
```

**Selection at boot:** sd-stub strips a leading `@N ` from its invocation parameters and loads the matching profile. For systemd-boot, write BLS `.conf` entries with `options @1 ` etc. — the rest of the cmdline is supplied by the profile's own `.cmdline`.

**Costs/constraints:**

| Concern | Mitigation |
|---|---|
| Only useful if the boot path actually passes `@N` | Detect: if firmware-direct-boot is the active path, log a clear warning and recommend enabling `clone` alongside (modes is a list — they're additive). |
| Replaces the original UKI in place — `kernel-install` may overwrite | Hook into `kernel-install` (provide a `90-uki-btrfs-snapshots.install` script) to re-join profiles after kernel upgrades. |
| Requires ukify ≥ 256 for `--join-profile` | Detect at startup. |
| Per-snapshot BLS entries with `options @N ` are still needed for systemd-boot's menu | Write them alongside, same as the bls binary's existing flow. |
| Number of joined profiles bounded by `uki.multi_profile.recent` | Default unbounded — profiles are cheap (~KB), no realistic limit. Caps are only useful if a user wants the menu trimmed. |

### Secure Boot

ukify natively handles SB signing for **both** modes. We just plumb config keys and forward as flags:

| ukify flag | Purpose |
|---|---|
| `--secureboot-private-key=PATH`, `--secureboot-certificate=PATH` | PE signature material |
| `--signtool={sbsign,pesign,systemd-sbsign}` | Signing backend |
| `--sign-kernel` / `--no-sign-kernel` | Sign the embedded kernel separately (some shim configurations want this) |
| `--pcr-private-key=PATH`, `--pcr-public-key=PATH`, `--pcr-certificate=PATH` | PCR signing for TPM2 measured boot |
| `--sign-profile=ID` | Sign PCR measurements for a specific profile (mode 2) |
| `--measure` / `--no-measure` | Whether to compute PCR measurements during build |

**Layered rollout:**

- **Initial:** detect Secure Boot via `/sys/firmware/efi/efivars/SecureBoot-*`; if enabled and signing config is absent, refuse cleanly. Don't silently ship unsigned UKIs the firmware will reject.
- **With signing:** wire the signing config through. User supplies their existing SB key/cert (the same ones their `kernel-install` setup uses); per-snapshot UKIs (mode 1) or the joined UKI (mode 2) get signed identically.
- **With PCR:** mode 2 has an edge here — sd-stub measures the selected profile into PCR 12, so the **same UKI** can have different PCR values per profile and unlock different TPM-sealed policies. Mode 1 is harder: each clone has different overall PCR measurements, so TPM-sealed disks need per-clone policy enrolment. Document the asymmetry; recommend mode 2 + PCR for TPM-sealed setups.

### Open questions

- **Both modes enabled by default**, gated only by package install (≈770 MB ESP footprint for the default single-kernel + 25-snapshot setup). Reasoning: this is the binary's *whole job*, and the user opted in by installing it; the bls binary's `write_entries: false` precedent isn't a fit here because the disk cost is large enough that silent overflow would be worse than an explicit pre-flight refusal. The pre-flight ESP free-space check is **mandatory**, not advisory — refuse to apply if projected size doesn't fit, with the exact numbers in the error message. Users wanting to tune down: set `modes: [clone]` or `modes: [multi-profile]` or `modes: []`.
- **Mode selection:** explicit list config (`uki.modes: [clone, multi-profile]`) rather than auto-detect. Auto-detecting "your boot path uses direct firmware boot" is fragile (would need to walk EFI `BootXXXX`/`BootOrder` vars). Static config is simpler and lets users layer the two modes for belt-and-braces setups.
- **`uki.clone.recent` default of 4:** chosen for ~280 MB headroom on a 512 MB ESP (the smaller-end size that still occurs in the wild). Yields 5 `.efi` files total on a single-kernel system (1 live multi-profile UKI + 4 clones). Worth surfacing actual snapshot+UKI sizes during dry-run so users see what they'd be committing to before raising the cap.
- **Cleanup model:** mode 1 cleans by prefix-match (same as bls binary). Mode 2 has one UKI to rewrite; cleanup means stripping removed profiles — `ukify build` rebuild from scratch is simpler than surgical removal.
- **kernel-install coexistence:** distro `kernel-install` plugins regenerate the primary UKI on kernel upgrades, which clobbers any joined profiles. We need a kernel-install hook in mode 2 (and to detect+re-emit clones in mode 1). The systemd `.path` unit watching `/.snapshots/` doesn't fire on kernel rebuilds; need a second trigger watching `/boot/EFI/Linux/`.
- **Stub override:** ukify defaults to `/usr/lib/systemd/boot/efi/linuxx64.efi.stub`. Some users build UKIs with a custom stub (shim-aware variants). Detect the source UKI's stub and reuse it.
- **Cross-arch:** stub is per-arch. amd64 binary already builds; aarch64 needs `linuxaa64.efi.stub`. Runtime picks the stub matching the running architecture.
- **Naming:** managed prefix for mode 1 — `uki-btrfs-snapshots-` to keep clones clearly separable from user-managed UKIs.
- **Reference implementations to watch:**
  - [`hariganti/uki-btrfs`](https://github.com/hariganti/uki-btrfs) — Python tool for multi-profile UKIs, still WIP (no functional code at time of writing).
  - [`openSUSE/sdbootutil`](https://github.com/openSUSE/sdbootutil) — production tool for BLS-on-snapper, uses unsigned-cmdline mode (different tradeoff).
  - [CachyOS proof-of-concept](https://discuss.cachyos.org/t/proof-of-concept-full-btrfs-system-rollbacks-with-systemd-boot-using-uki-with-secureboot-enabled/15541) — explicitly noted as weakening Secure Boot guarantees.
