# peseal

Pure-Go Authenticode signer for PE32+ binaries — UKIs, EFI loaders, anything Secure Boot reads. Wraps [`go-uefi`](https://github.com/Foxboron/go-uefi) so emitted signatures are byte-for-byte compatible with `sbctl`'s output.

Standalone. No knowledge of snapshots or other binaries in this repo. The [`uki-btrfs-snapshots`](../uki-btrfs-snapshots/) sibling chains peseal via its `sign_command` config, but peseal can equally sign sdbootutil's UKIs, mkinitcpio's outputs, or anything else dropped on the ESP.

## Subcommands

```
peseal sign     [--key K] [--cert C] [--dry-run] [-y] [--no-skip-signed] FILE...
peseal verify   [--cert C] FILE...
peseal inspect  [--json] FILE...
peseal version
```

- **`sign`** — idempotent by default; files already valid for the configured cert are skipped. `--no-skip-signed` forces re-sign (will produce multi-sig PEs). `--dry-run` reports without writing.
- **`verify`** — exits non-zero if any file fails. `--cert` overrides config.
- **`inspect`** — prints signature status + PE header + section table; for UKIs, decodes `.cmdline`/`.uname`/`.osrel`/profiles. `--json` for structured output.

## Configuration

`/etc/peseal.yaml`:

```yaml
key_path: /etc/secureboot/db.key
cert_path: /etc/secureboot/db.crt

# Files to sign when `peseal sign` is invoked without explicit args
# (used by the .path-triggered .service unit). Globs supported.
paths:
  - /boot/efi/EFI/Linux/*.efi
  - /boot/EFI/Linux/*.efi

skip_already_signed: true
log_level: info
```

CLI `--config /path/to/file.yaml` overrides the default.

## Systemd integration (decoupled signing)

Shipped `peseal.path` watches both `/boot/efi/EFI/Linux` (Ubuntu/Fedora/Tumbleweed default) and `/boot/EFI/Linux` (Arch default). Whatever lands there triggers `peseal.service`, which runs `peseal sign --config /etc/peseal.yaml -y` against the configured paths.

```bash
# After dropping in your /etc/peseal.yaml with key_path + cert_path:
sudo systemctl enable --now peseal.path
```

Now any tool that writes to the ESP — `uki-btrfs-snapshots`, sdbootutil, mkinitcpio presets — produces UKIs that get signed automatically. Idempotency means a stray filesystem event doesn't double-sign.

## Inline chaining from uki-btrfs-snapshots

Alternative to the `.path`-driven mode: set `uki.sign_command: ["peseal", "sign", "{}"]` in `/etc/uki-btrfs-snapshots.yaml`. Each cloned UKI is signed during the same `uki-btrfs-snapshots.service` run that wrote it, before the binary exits. See the [uki-btrfs-snapshots README](../uki-btrfs-snapshots/) for per-distro `sign_command` templates covering peseal, sbctl, sbsign, pesign, and systemd-sbsign.

The two modes can coexist; a second sign run is a no-op due to idempotency.

## Per-distro key conventions

peseal doesn't care where your keys live, but these are the typical paths the rest of each distro's tooling expects:

| Distro | Key path | Cert path | Tool that manages them |
|---|---|---|---|
| Arch | `/usr/share/secureboot/keys/db/db.key` | `/usr/share/secureboot/keys/db/db.pem` | `sbctl` |
| Ubuntu | `/etc/secureboot/MOK.key` | `/etc/secureboot/MOK.crt` | manual + `mokutil` |
| Fedora | `/etc/pki/secureboot/db.key` | `/etc/pki/secureboot/db.crt` | `efikeygen` / `pesign` |
| Tumbleweed | `/etc/sdbootutil/keys/db.key` | `/etc/sdbootutil/keys/db.crt` | `sdbootutil` |

`sbctl`'s key store can be reused with peseal directly — point `key_path` + `cert_path` at the same files sbctl uses. Same Authenticode output either way (both wrap `go-uefi`).

## What peseal doesn't do (yet)

- **Hardware-backed keys** (TPM2, PKCS#11, YubiKey PIV)
- **PCR signing** for TPM-sealed disks
- **Cert-chain validation** (single-cert acceptance only — chains and EKU constraints out of scope)
- **Passphrase-encrypted keys** (rejected at load)

See [`TODO.md`](TODO.md) for the roadmap on hardware keys + PCR signing, including the `systemd LoadCredentialEncrypted=` answer for the auth-material problem.

## Quick verification recipe

```bash
# Make sure your key + cert pair correctly:
peseal sign --key /etc/secureboot/db.key --cert /etc/secureboot/db.crt /tmp/test.efi

# Confirm via inspect:
peseal inspect /tmp/test.efi
# Look for "Status: signed" and the expected Signer CN.
```

For man-page-level reference, `man peseal` after install or `make docs` from a source checkout.
