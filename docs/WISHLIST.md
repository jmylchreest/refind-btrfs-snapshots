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
- Live UKI (kernel-install owned): ~70 MB — **untouched by us**
- Managed snapshot UKI (us): ~70 MB (clone of live UKI + 25 profile sections, ~50 KB metadata overhead)
- Clones: **4 × ~70 MB ≈ 280 MB**
- **Total ≈ 350 MB additional disk, 6 `.efi` files on the ESP** (1 live UKI + 1 managed snapshot UKI + 4 cloned UKIs).

For two-kernel systems: ~420 MB additional (one managed snapshot UKI per kernel + 4 shared clones). 8 `.efi` files total.

This comfortably fits a 512 MB ESP with room for other content. The pre-flight check makes "doesn't fit" a hard, actionable error rather than a silent overflow.

**Reconciliation, not append.** Every run computes the desired set from scratch — covering both clones AND managed snapshot UKIs — scans the ESP under the managed prefix, and reconciles:

1. **Desired set** is computed by joining the live system inventory with the snapshot module inventory:
   - **One managed snapshot UKI per kernel version that has any bootable snapshot.** A "kernel version" `<K>` qualifies for a managed UKI iff *either* the live system currently has kernel `<K>` installed *or* at least one retained snapshot has matching modules under `/lib/modules/<K>-*/`. This means a managed UKI persists as long as it's the boot path for at least one snapshot — even if the live system has since switched away from that kernel.
   - **Clones** for the eligible (non-stale, non-deleted) snapshots in the newest-N, named `uki-btrfs-snapshots-<snap-id>-<kernel>.efi` where `<kernel>` is the version whose managed UKI hosts the matching profile.
2. `to_delete = on_disk − desired` catches:
   - Clones that aged out of the newest-N window
   - Clones whose snapshot was pruned from btrfs (snapper rotation, manual delete)
   - Clones whose snapshot became stale relative to the current kernel (the existing `stale_snapshot_action=delete` flow excludes them from the eligible set; reconciliation removes them)
   - Managed snapshot UKIs for kernels that *no longer have any compatible snapshot* AND aren't installed on the live system anymore (true orphans — no data depends on them)
3. `to_add = desired − on_disk` catches:
   - Clones for new snapshots
   - Managed snapshot UKIs for newly-installed kernels
   - Managed snapshot UKIs whose underlying live UKI's mtime changed (kernel upgrade) — refresh from the new live UKI
4. **Deletions are applied first**, freeing their space.
5. **Then the pre-flight ESP free-space check runs against the additions only.**
6. Refuse only if, *even after pruning aged-out files*, the new additions won't fit. Error: `ESP has X MB free after pruning, need Y MB for new artefacts. Reduce uki.clone.recent or free non-managed ESP files.`

**Retention is principled, not opinionated.** The rule "a managed UKI exists iff at least one bootable snapshot needs it" handles every interesting case naturally:

| Situation | Result |
|---|---|
| Live kernel A installed + snapshots compatible with A | Keep managed UKI; refresh on `kernel-install` updates via `.path` trigger |
| Live kernel A uninstalled, but snapshots from before the uninstall still have `/lib/modules/<A>/` | **Keep** — those snapshots are still bootable via this UKI, which is now the *only* copy of kernel A on the system |
| Live kernel A uninstalled AND no remaining snapshot has compatible modules | Remove — no data depends on it anymore |
| Live kernel A installed + brand-new system, no snapshots yet | Keep — kernel is installed |

**Bound on proliferation.** Each managed UKI only contains profiles for snapshots whose modules match its embedded kernel — a snapshot doesn't appear in two managed UKIs. So total profile count across all managed UKIs ≤ total eligible snapshot count. The disk cost scales with *distinct kernel versions* seen across the snapshot window, not with snapshot count. A user keeping 30 days of daily snapshots through 1-2 kernel upgrades has 1-2 managed UKIs (~70-140 MB), not one per snapshot.

**The historical kernel preservation property is a side-effect.** When kernel A is uninstalled from the live system, our managed UKI for A becomes the **only** remaining copy of that kernel binary on disk — it's preserved inside the `.linux` PE section. This makes the managed UKI the authoritative recovery path for any snapshot that needs that kernel, until the last such snapshot is pruned. That's the right behaviour: the artefact's lifetime is bound to the data that depends on it.

If a user wants more aggressive cleanup (e.g., test systems where stale kernels shouldn't accumulate), `uki.retain_only_installed_kernels: true` reverts to "managed UKI exists iff its kernel is currently installed on the live system" — orphans get removed even when snapshots reference them. Default is `false` (the data-driven policy above).

This means the clone set **always tracks the newest N**, even on a tight ESP — there's no scenario where the binary silently degrades by refusing-and-doing-nothing while the safety net staleness grows. The only failure case is "even after pruning, the new set won't fit", which is the right time to refuse loudly.

**Surfacing staleness and orphans.** The `status` command knows the desired set (from config + snapshot inventory + installed kernels) and the actual on-disk set. If they diverge, status reports:

- `cloned: 4 expected, 3 present (1 missing; last attempt refused due to ESP full)` — when reconciliation couldn't apply
- `managed_uki: linux-cachyos missing (live UKI updated 2 days ago, regeneration failed)` — when a kernel-upgrade refresh got stuck
- `retained_kernel: linux-cachyos (uninstalled from live system) — 4 snapshots still depend on it` — when a managed UKI is kept under the data-driven retention rule

The refusal reason and orphan state are recorded in a small state file under `/var/lib/uki-btrfs-snapshots/` so they survive until the next successful run.

If the default is still too aggressive: setting `modes: [multi-profile]` alone drops to ~0 MB additional (in-place only); setting `clone.recent: 2` halves the clone cost; setting `modes: []` opts out entirely.

The implementation note: mode 2's managed UKI is a separate file from the kernel-install-owned live UKI. We never modify the live UKI in place; this avoids the kernel-install clobber race entirely. On kernel upgrades, our `.path` unit (watching the live UKI's mtime in addition to `/.snapshots/`) regenerates the managed snapshot UKI from the new live UKI. No "repair" step is needed because we always rebuild from scratch.

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

**Output:** a separate managed UKI per kernel at `<esp>/EFI/Linux/<prefix><kernel>.efi`, cloned from the live UKI and extended with one `.profile` section per eligible snapshot.

**We do not modify the kernel-install-managed live UKI.** That file (`<esp>/EFI/Linux/<kernel>.efi`) is treated as read-only input; we only ever read from it to source the kernel/initrd/osrel/sbat sections. The managed snapshot UKI is a separate file owned entirely by this binary.

**Profile ordering:**

| Profile | Cmdline target | Why |
|---|---|---|
| `@0` (default) | **Most recent eligible snapshot** | Direct firmware boot of the snapshot UKI (`BootXXXX` entry) selects this — gives users a useful "panic-rollback" target without needing sd-stub's `@N` selector. The live UKI already handles "boot live root"; duplicating that as profile 0 would waste the slot. |
| `@1`..`@N` | Each older eligible snapshot, newest-first | Selectable via sd-stub `@N` prefix from systemd-boot or hand-written refind entries. |

Each profile carries its own `.cmdline` section plus `.profile` metadata per [UAPI spec](https://uapi-group.org/specifications/specs/unified_kernel_image/):
- `ID=snap-<subvolid>` — used as the `@<ID>` selector
- `TITLE=Linux-cachyos (2026-05-27T01:00:00Z)` — same title format the bls binary uses, honouring `advanced.naming.menu_format` + `display.local_time`

The PE format defined by the UAPI spec: repeated `.profile` sections act as separators. Sections appearing before the first `.profile` are the base; sections between `.profile` markers belong to that profile. sd-stub measures only base + selected profile into PCR 12.

**Build:**

```bash
# 1. For each profile (newest snapshot first), build a "mini-UKI" carrying
#    only that profile's metadata and rewritten cmdline.
ukify build \
  --profile="ID=snap-16403 TITLE=Linux-cachyos (2026-05-27T20:00Z)" \
  --cmdline="<rewritten for snap 16403>" \
  --output=tmp/profile-0.efi
# … repeat for each snapshot, ordering = newest-first so profile @0 == newest

# 2. Build the managed snapshot UKI by joining all per-snapshot profiles onto
#    a base assembled from the live UKI's sections (linux, initrd, osrel, uname,
#    sbat extracted via objcopy).
ukify build \
  --linux=tmp/linux --initrd=tmp/initrd \
  --os-release=@tmp/osrel --uname="$(tr -d '\0' < tmp/uname)" \
  --sbat=@tmp/sbat \
  --cmdline="<empty or trivial — profiles override>" \
  --join-profile=tmp/profile-0.efi \
  --join-profile=tmp/profile-1.efi \
  ... \
  --output=<esp>/EFI/Linux/<prefix><kernel>.efi
```

**Selection at boot:**

- **Direct firmware boot:** if the firmware launches the managed snapshot UKI as a `BootXXXX` entry, profile `@0` (newest snapshot) is selected by default. Users wanting older snapshots without booting a different image use sd-stub or chainload via systemd-boot.
- **systemd-boot:** auto-discovers the managed snapshot UKI; with native profile-menu synthesis (UAPI spec "may", depends on systemd-boot version) each profile becomes a menu entry. For deterministic menu population, write BLS `.conf` entries with `options @<ID> ` per profile (mirrors the bls binary's pattern).
- **rEFInd:** treats the managed snapshot UKI as another EFI binary; per-profile selection needs hand-written `refind.conf` entries with `options "@<ID> "`.

**Costs/constraints:**

| Concern | Mitigation |
|---|---|
| One extra UKI-sized file per kernel (~70 MB each) on the ESP | This is the cost of clean ownership separation from kernel-install. Accept and document. |
| Snapshot UKI must stay in sync with live UKI's kernel/initrd after kernel updates | `.path` unit also watches mtime on `<esp>/EFI/Linux/<kernel>.efi`; when it changes (kernel-install ran), regenerate the snapshot UKI. No "repair" needed — just regenerate fresh. |
| Only useful if the boot path actually passes `@N` (for profiles > 0) | Profile `@0 = newest snapshot` ensures direct firmware boot does something useful even without `@N` support. Log a notice if firmware-direct-boot is the active path, recommending also enabling `clone` for older snapshots. |
| Requires ukify ≥ 256 for `--join-profile` | Detect at startup. |
| Per-snapshot BLS entries with `options @<ID>` still useful for systemd-boot's menu | Optional; write them alongside if the user wants explicit menu entries beyond what systemd-boot may auto-synthesize. |
| Number of joined profiles bounded by `uki.multi_profile.recent` | Default unbounded — profiles are cheap (~few KB each). |

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

- **Both modes enabled by default**, gated only by package install (≈280 MB ESP footprint for the default single-kernel + 25-snapshot + `clone.recent: 4` setup). Reasoning: this is the binary's *whole job*, and the user opted in by installing it; the bls binary's `write_entries: false` precedent isn't a fit here. Users wanting to tune down: set `modes: [clone]`, `modes: [multi-profile]`, `clone.recent: 2`, or `modes: []` to opt out entirely.
- **Pre-flight space check operates against additions only**, after pruning aged-out clones (`desired − on_disk` and `on_disk − desired` computed each run; deletions applied first). Refusal happens only if even the post-pruning projection won't fit. This prevents the rollback safety net from silently degrading over time when the ESP is tight.
- **Mode selection:** explicit list config (`uki.modes: [clone, multi-profile]`) rather than auto-detect. Auto-detecting "your boot path uses direct firmware boot" is fragile (would need to walk EFI `BootXXXX`/`BootOrder` vars). Static config is simpler and lets users layer the two modes for belt-and-braces setups.
- **`uki.clone.recent` default of 4:** chosen for ~280 MB headroom on a 512 MB ESP (the smaller-end size that still occurs in the wild). Yields 5 `.efi` files total on a single-kernel system (1 live multi-profile UKI + 4 clones). Worth surfacing actual snapshot+UKI sizes during dry-run so users see what they'd be committing to before raising the cap.
- **Cleanup model:** mode 1 cleans by prefix-match (same as bls binary). Mode 2 has one UKI to rewrite; cleanup means stripping removed profiles — `ukify build` rebuild from scratch is simpler than surgical removal.
- **kernel-install coexistence:** mode 2's separate managed snapshot UKI sidesteps the clobber problem entirely — kernel-install owns the live UKI; we own the snapshot UKI; the two never overlap. On kernel updates, `kernel-install` regenerates the live UKI as normal, and our `.path` unit (watching live-UKI mtime in addition to `/.snapshots/`) regenerates the snapshot UKI from the new kernel/initrd. Mode 1's clones also need refresh after kernel upgrades — same `.path` trigger handles both.
- **Stub override:** ukify defaults to `/usr/lib/systemd/boot/efi/linuxx64.efi.stub`. Some users build UKIs with a custom stub (shim-aware variants). Detect the source UKI's stub and reuse it.
- **Cross-arch:** stub is per-arch. amd64 binary already builds; aarch64 needs `linuxaa64.efi.stub`. Runtime picks the stub matching the running architecture.
- **Naming:** managed prefix for mode 1 — `uki-btrfs-snapshots-` to keep clones clearly separable from user-managed UKIs.
- **Reference implementations to watch:**
  - [`hariganti/uki-btrfs`](https://github.com/hariganti/uki-btrfs) — Python tool for multi-profile UKIs, still WIP (no functional code at time of writing).
  - [`openSUSE/sdbootutil`](https://github.com/openSUSE/sdbootutil) — production tool for BLS-on-snapper, uses unsigned-cmdline mode (different tradeoff).
  - [CachyOS proof-of-concept](https://discuss.cachyos.org/t/proof-of-concept-full-btrfs-system-rollbacks-with-systemd-boot-using-uki-with-secureboot-enabled/15541) — explicitly noted as weakening Secure Boot guarantees.
