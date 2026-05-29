# uki-btrfs-snapshots

Clones source Unified Kernel Images per btrfs snapshot, rewriting each clone's `.cmdline` PE section so it boots its snapshot's subvolume. Optional `sign_command` exec re-signs each clone immediately after writing.

## Signing options

Clones are unsigned out of the box. On Secure Boot-enforcing systems you need to either:

- **Inline signing** via `uki.sign_command` — execs a signer per clone during the same `generate` run.
- **Decoupled signing** via [`peseal`](../peseal/) — `peseal.path` watches the output directory and signs whatever lands there independently of which tool wrote it.

Decoupled is the recommended default — works for sdbootutil's primary UKIs and our clones equally. Inline is useful when you want the signing step visible in the same service run that wrote the clones.

### Per-distro `sign_command` templates

| Distro | Recommended | Template |
|---|---|---|
| Arch | `sbctl` or `peseal` | `["sbctl", "sign", "-s", "{}"]` |
| Ubuntu | `sbsign` (sbsigntools) | `["sbsign", "--key", "/etc/secureboot/MOK.key", "--cert", "/etc/secureboot/MOK.crt", "--output", "{}", "{}"]` |
| Fedora | `pesign` wrapper or `sbsign` | `["/usr/local/sbin/sign-uki-pesign", "{}"]` (script wraps pesign's two-file dance) |
| Tumbleweed | `sbsign` or `systemd-sbsign` | `["sbsign", "--key", "/etc/sdbootutil/keys/db.key", "--cert", "/etc/sdbootutil/keys/db.crt", "--output", "{}", "{}"]` |
| Any (peseal installed) | `peseal` | `["peseal", "sign", "{}"]` |

The CLI also accepts a shell-quoted string form via `--sign-command`:

```bash
sudo uki-btrfs-snapshots generate --sign-command "peseal sign {}"
```

Same parsing as the YAML string form — split via shellwords.

#### Fedora pesign wrapper

pesign can't sign-in-place safely, so write a small wrapper at `/usr/local/sbin/sign-uki-pesign`:

```sh
#!/bin/sh
pesign --sign --certificate uki-signing --in="$1" --out="$1.tmp"
mv "$1.tmp" "$1"
```

`chmod +x` and reference it from `sign_command`. The wrapper isolates the temp-file dance from peseal's argv-based interface.

### `{}` substitution semantics

Exact-token match per argv element. `{}` becomes the clone's absolute path; `{}.sig`, `out={}`, and any other partial form pass through unchanged. If your tool needs an output-with-extension idiom, wrap it in a script as above.

## Operational notes

- **Dry-run** does not exec the sign command. `runner.Command` honours the runner's dry-run flag and just logs what would be exec'd.
- **Per-clone failures aggregate.** If 2 of 5 clones fail to sign, the other 3 still get written + signed, and the binary exits non-zero with all five outcomes visible in the log.
- **Idempotency depends on your signer.** `peseal` and `sbctl` skip files already signed by the configured cert. `sbsign` and `pesign` will overwrite the signature blob on each invocation (idempotent in content, but updates the file mtime — which can re-trigger `peseal.path` if both are configured).
- **Order matters with sdbootutil.** If both sdbootutil and `uki-btrfs-snapshots` write to `/EFI/Linux/`, run sdbootutil first so the source UKI exists before clones are derived from it.

See [`docs/USAGE.md`](../../docs/USAGE.md) for the broader configuration reference.
