# Wishlist

Planned features and capabilities that aren't built yet. Items here are **not commitments** — they capture intent, surface design tradeoffs, and let us iterate on shape before code. Anything implementation-ready graduates out of this file and into an issue or PR.

## Table of Contents

- [Hardware-backed signing keys (peseal)](#hardware-backed-signing-keys-peseal)
- [PCR signing for TPM-sealed disks](#pcr-signing-for-tpm-sealed-disks)
- [Open questions](#open-questions)

---

## Hardware-backed signing keys (peseal)

`peseal` ships with software-key signing only — `pkg/authenticode.Signer` holds an `*rsa.PrivateKey` loaded from PEM. For users on enterprise SB setups, YubiKey-via-PKCS#11, or TPM2-stored keys, that's not enough.

See [`cmd/peseal/TODO.md`](../cmd/peseal/TODO.md) for the full roadmap. Short version: the `Signer.key` field becoming `crypto.Signer` is the enabling refactor (go-uefi already accepts the interface), and per-backend wrappers come after:

- **TPM2** via `github.com/google/go-tpm` — pure Go, covers the modal Tumbleweed/sdbootutil audience.
- **PKCS#11** via `github.com/ThalesGroup/crypto11` or similar — requires CGO; gated behind a build tag to keep the default release CGO-free.
- **PCR-bound policy auth** — TPM2 keys that only sign when the machine is in a specific measured-boot state.

The thorny operational issue is auth-material handling for non-interactive .service usage. The right answer is systemd `LoadCredentialEncrypted=` for PIN-bearing backends (PKCS#11, PIV) and PCR-policy-bound TPM2 keys for the "no secret at all" path. See [`cmd/peseal/TODO.md`](../cmd/peseal/TODO.md) for the full threat-model framing.

## PCR signing for TPM-sealed disks

Authenticode signing (which peseal does) authenticates the UKI's bytes to the firmware. PCR signing is a separate concern: it signs predictions of what TPM PCR values **will** be when this specific UKI boots, so systemd-stub can unseal TPM-bound LUKS keys.

The signing primitive is the same; the hard part is generating correct PCR predictions, because that requires reimplementing systemd-stub's measurement logic (which lives in C and changes across systemd versions).

Three realistic paths, in order of preference:

1. **Shell out to `systemd-measure`** — pragmatic, breaks the pure-Go ethos. ~3 days of work, low maintenance.
2. **Wait for upstream** — a Go library wrapping systemd-stub's measurement logic might emerge. Indefinite timeline.
3. **Reimplement in pure Go** — 2-4 weeks of work plus ongoing maintenance to track systemd-stub changes upstream. Hard to justify for an audience that mostly already has systemd-measure installed.

Defer until demonstrated demand. Document the `systemd-measure` recipe as an external step users can wire into their `peseal.path` chain or via a wrapper script.

## Open questions

- **Stub override for UKI clones.** `pkg/uki` round-trips whatever stub the source UKI was built with, preserving the original security profile. If a future use case wants to *change* the stub during cloning (e.g., swap in a shim-aware variant), we'd need section-level surgery beyond `.cmdline` rewrite. No concrete request yet.
- **Per-snapshot ESP usage cap.** Each clone is ~70 MB. `snapshot.selection_count` becomes load-bearing on UKI-only systems. Worth a soft warning when planned writes would consume >X% of the ESP's free space, configurable via something like `uki.max_esp_usage_percent`.
- **`.ucode` standalone microcode section.** Source UKIs today concatenate microcode into the single `.initrd` section; `pkg/uki` preserves that verbatim. If a vendor adopts the UAPI-listed `.ucode` standalone section, we'd need to confirm the round-trip still works.
- **Cert-chain validation in `peseal verify`.** Current implementation accepts a signature if any supplied root matches. Real chain validation (intermediates, EKU constraints, expiry) would use `crypto/x509.Verify`. Out of scope until someone hits a real chain.
