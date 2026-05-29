# Test fixtures — UKI parsing

These `.efi` files are committed UKIs (Unified Kernel Images) used by
`InspectUKI` tests. They're real `ukify`-built PE32+ binaries with dummy
payloads — content meaningful for *parsing* tests, not for actually booting.

| File | Purpose |
|---|---|
| `uki-single-profile.efi` | Legacy single-profile UKI: base `.cmdline` only, no `.profile` sections |
| `uki-multi-profile.efi`  | Multi-profile UKI: 3 profiles (ukify's auto-emitted `main` + two per-snapshot variants) |

Each file is ~100 KB; the bulk (~102 KB) is the embedded `systemd-stub`
(`/usr/lib/systemd/boot/efi/linuxx64.efi.stub`) — the actual EFI program a
real boot would execute. The variable sections (`.linux`, `.initrd`,
`.cmdline`, `.osrel`, `.uname`, `.sbat`, `.profile`) carry small dummy or
test-specific content; see `inspect_uki_test.go` for the exact strings each
test asserts on.

## Regenerating

```sh
make uki-fixtures
```

Requires `systemd-ukify` on the host. The target prints a clear skip message
and exits 0 if `ukify` isn't found, so the regen step is safe to invoke from
environments without it — CI doesn't regenerate, it just runs the tests
against the committed binaries.
