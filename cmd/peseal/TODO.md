# peseal: roadmap

Tracked work not yet shipped. Items here are intent, not commitments. See [`docs/WISHLIST.md`](../../docs/WISHLIST.md) for cross-binary scope.

## Hardware-backed signing keys

`pkg/authenticode.Signer` currently holds an `*rsa.PrivateKey` directly. The underlying `go-uefi` library already takes `crypto.Signer` (an interface), so the refactor is mechanical: change the field type, keep the existing PEM/file constructors, add per-backend constructors.

### Slicing

1. **Refactor `Signer.key` to `crypto.Signer`.** Zero behaviour change; opens the door. ~50 LOC + ensure existing tests pass unchanged.
2. **TPM2 backend** via `github.com/google/go-tpm`. Pure Go (talks `/dev/tpmrm0` ioctls directly). Covers the modal Tumbleweed / sdbootutil audience. Persistent-handle keys with empty policy or password auth.
3. **PKCS#11 backend** via `github.com/ThalesGroup/crypto11` or similar. Requires CGO — gated behind a `+cgo` build tag so the default release binary stays CGO-free. Covers enterprise HSM and YubiKey-via-opensc setups.
4. **TPM2 PCR-bound policy auth.** Key only usable when the machine is in a specific measured-boot state. The TPM does the authentication; no PIN/password required. Genuinely security-meaningful — see "Auth material" section below.

### Auth material — the underrated problem

When `peseal.service` is `.path`-triggered and runs non-interactively as root, the PIN/password for the hardware key has to come from somewhere. Options ranked worst → best:

| Source | Notes |
|---|---|
| Plaintext in `peseal.yaml` | Defeats the purpose of hardware keys — the secret is now on disk |
| `Environment=PIN=...` in the .service unit | Same problem; unit file is plaintext |
| Kernel keyring (`keyctl`) | Plausible but awkward — something has to put the secret there at boot |
| **systemd `LoadCredentialEncrypted=`** | **Recommended for PKCS#11 / PIV.** Encrypted-at-rest under a TPM-bound system key, decrypted by systemd at service-start, mounted into a service-only tmpfs at `$CREDENTIALS_DIRECTORY/<name>`. Plaintext exists only in-memory only while the service runs. |
| **TPM2 with empty-policy key** | **Recommended for TPM2.** No auth material at all — access controlled by TPM ownership. Common for SB signing in the Tumbleweed/sdbootutil world. |
| **TPM2 with PCR-bound policy auth** | **Strongest.** The TPM physically refuses to sign unless the machine boots into the expected PCR state. No password to leak, no secret on disk. |

#### How `LoadCredentialEncrypted` integrates

The shipped `.service` unit gains:

```ini
[Service]
LoadCredentialEncrypted=peseal-pin:/etc/credstore.encrypted/peseal.pin
```

One-time admin setup:

```bash
echo -n "<pin>" | sudo systemd-creds encrypt --name=peseal-pin - /etc/credstore.encrypted/peseal.pin
```

peseal's config gains a resolution chain — `pin` literal (dev/test only) → `pin_env` → `pin_file`, with `pin_file: ${CREDENTIALS_DIRECTORY}/peseal-pin` as the shipped default:

```yaml
signing:
  backend: pkcs11
  pkcs11:
    module: /usr/lib/opensc-pkcs11.so
    token_label: "Code Signing"
    key_label: "uki-signing"
    pin_file: ${CREDENTIALS_DIRECTORY}/peseal-pin
```

Implementation effort: ~30 LOC + documentation. Zero new dependencies (systemd-creds ships with systemd ≥ 250).

### Threat model framing

Hardware keys don't protect against compromised root. A live attacker with root can still invoke peseal against arbitrary input or attach to the service process. What hardware keys actually buy:

- Stolen disk → can't extract the signing key (lives in hardware)
- Compromised filesystem snapshot → same
- `/etc` backup grabbed by an insider → same
- Live root for some window → game over either way

The strongest configuration in this ecosystem is TPM2-with-PCR-bound policy auth: an attacker who boots a rescue ISO can't use the key even with root, because the TPM enforces the policy. This protects against the "live root from a different boot context" class — meaningfully better than plain hardware keys.

Documentation should be explicit about which threat model each configuration protects against, so users don't reach for `LoadCredentialEncrypted` when PCR-bound policy auth would actually solve their problem.

### Effort estimate

| Step | Scope | Effort | Risk |
|---|---|---|---|
| 1 | `Signer.key` → `crypto.Signer` refactor | ~1 day | Trivial |
| 2 | TPM2 backend + tests against `go-tpm-tools/simulator` | ~1 week | Low |
| 3 | PKCS#11 + `+cgo` build tag + tests against `softhsm2` | ~1 week | Medium (CGO ergonomics) |
| 4 | TPM2 PCR-bound policy auth + `LoadCredentialEncrypted` wiring | ~1 week | Low (libraries exist) + extensive docs |

Total: ~3-4 weeks for full coverage. Steps 1+2 alone deliver the most-asked-for use case.

## PCR signing for TPM-sealed disks

Different problem from PE Authenticode signing. The Authenticode signature authenticates the UKI's bytes to the firmware; the PCR signature authenticates **predictions of PCR values** that systemd-stub uses to unseal TPM-bound LUKS keys.

The hard part is generating correct predictions: knowing how the systemd-stub measures specific UKI sections into specific PCRs, in what order, with what hash chaining. This logic lives in the systemd-stub C source and changes between versions.

### Three paths

| Path | Pure Go | Effort | Maintenance |
|---|---|---|---|
| A. Shell out to `systemd-measure` | No | ~3 days | Low |
| B. Reimplement systemd-measure in Go | Yes | 2-4 weeks | High — track upstream systemd |
| C. Wait for upstream Go library | Yes | 0 | None — but indefinite wait |

**Recommendation:** path A if/when there's demonstrated demand. The reimplementation cost in (B) is hard to justify for an audience that overlaps almost entirely with "users who already have systemd-measure installed."

Document the `systemd-measure` recipe as an external step users can wire into their `.path` chain or via a wrapper script. Defer in-binary support.

## Other items

- **Audit logging** — structured per-sign log lines suitable for SIEM ingestion (cert fingerprint, file path, timestamp, exit status). Currently zerolog at info level; would want a dedicated JSON sink optional config.
- **Cert-chain validation in `Verify`** — current implementation accepts a signature if any supplied root matches. Real chain validation (intermediates, EKU constraints, expiry) would use `crypto/x509.Verify`. Out of scope until someone hits a real chain.
- **Passphrase-encrypted keys** — currently rejected at load. Implementing properly means handling PKCS#8 PBES2 / PKCS#5 v2 + an interactive prompt OR a passphrase file. Same `LoadCredentialEncrypted` story as PINs.
- **Multi-signer** — sign with N keys, producing a multi-signature PE. Authenticode supports it; no current ask, no clear use case beyond cert rotation.
