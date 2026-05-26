#!/usr/bin/env bash
# make-test-fixtures.sh — build a UKI and a fake ESP with a BLS Type #1
# entry that kernel-spy can be pointed at for end-to-end testing.
#
# Spec: https://uapi-group.org/specifications/specs/boot_loader_specification/
#
# Usage:
#   contrib/make-test-fixtures.sh [--kernel PATH] [--initrd PATH] [--out DIR]
#
# Defaults autodetect a kernel/initramfs in /boot (Arch layout) and write
# fixtures to a tempdir under /tmp. The temp directory is printed at the end
# so it can be passed to kernel-spy, e.g.:
#
#   eval "$(contrib/make-test-fixtures.sh --print-eval)"
#   ./kernel-spy --esp="$KERNEL_SPY_ESP" "$KERNEL_SPY_UKI_DIR"
#
# Requirements:
#   - ukify  (systemd >= 253)
#   - A bzImage kernel and an initramfs (typically /boot/vmlinuz-* and /boot/initramfs-*.img)

set -euo pipefail

print_eval=0
kernel=""
initrd=""
out=""

usage() {
    sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'
    exit "${1:-0}"
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --kernel)     kernel="$2"; shift 2 ;;
        --initrd)     initrd="$2"; shift 2 ;;
        --out)        out="$2"; shift 2 ;;
        --print-eval) print_eval=1; shift ;;
        -h|--help)    usage 0 ;;
        *) echo "unknown arg: $1" >&2; usage 1 ;;
    esac
done

# Autodetect kernel and initramfs if not provided.
if [[ -z "$kernel" ]]; then
    kernel=$(ls -1 /boot/vmlinuz-* 2>/dev/null | head -1 || true)
fi
if [[ -z "$initrd" ]]; then
    initrd=$(ls -1 /boot/initramfs-*.img 2>/dev/null | grep -v fallback | head -1 || true)
fi

if [[ -z "$kernel" || ! -r "$kernel" ]]; then
    echo "error: could not find a readable kernel (tried /boot/vmlinuz-*); pass --kernel" >&2
    exit 1
fi
if [[ -z "$initrd" || ! -r "$initrd" ]]; then
    echo "error: could not find a readable initramfs (tried /boot/initramfs-*.img); pass --initrd" >&2
    exit 1
fi
if ! command -v ukify >/dev/null; then
    echo "error: ukify not found in PATH (install systemd-ukify or systemd >=253)" >&2
    exit 1
fi

# Determine the kernel release for .uname / staleness checks. Prefer the
# version embedded in the bzImage (the kernel package suffix is not the
# release on most distros), and fall back to the filename otherwise.
kver=$(strings "$kernel" 2>/dev/null \
    | grep -m1 -E '^[0-9]+\.[0-9]+\.[0-9]+.*\(.+\)' \
    | awk '{print $1}' || true)
if [[ -z "$kver" ]]; then
    kver=$(basename "$kernel" | sed 's/^vmlinuz-//')
fi

if [[ -z "$out" ]]; then
    out=$(mktemp -d -t kernel-spy-fixtures.XXXXXX)
fi
mkdir -p "$out/uki" "$out/esp/loader/entries" "$out/esp/boot" "$out/esp/EFI/Linux"

uki="$out/uki/linux-test-${kver}.efi"
echo "Building UKI: $uki" >&2
ukify build \
    --linux="$kernel" \
    --initrd="$initrd" \
    --cmdline='root=UUID=test-fixture rw quiet' \
    --uname="$kver" \
    --os-release="ID=test
PRETTY_NAME=\"kernel-spy test fixture\"
VERSION_ID=fixture" \
    --output="$uki" >&2

# Also drop a copy of the UKI into <esp>/EFI/Linux so the standard scan
# locations pick it up when --esp points at the fake ESP.
cp "$uki" "$out/esp/EFI/Linux/linux-test-${kver}.efi"

# Mirror the live kernel/initramfs into the fake ESP's /boot tree, and write
# a BLS Type #1 entry that references them.
cp "$kernel" "$out/esp/boot/$(basename "$kernel")"
cp "$initrd" "$out/esp/boot/$(basename "$initrd")"

entry="$out/esp/loader/entries/test-${kver}.conf"
cat >"$entry" <<EOF
title    Test fixture (BLS Type #1)
version  ${kver}
linux    /boot/$(basename "$kernel")
initrd   /boot/$(basename "$initrd")
options  root=UUID=test-fixture rw quiet
EOF

cat <<EOF >&2

Fixtures ready at: $out
  UKI standalone:    $uki
  Fake ESP (UKI + BLS):
    --esp=$out/esp
    BLS entry:        $entry
    UKI in ESP:       $out/esp/EFI/Linux/linux-test-${kver}.efi

Try kernel-spy with both layouts:
  kernel-spy --esp=$out/esp                       # picks up UKI + BLS
  kernel-spy $out/uki                             # just the standalone UKI
EOF

if [[ "$print_eval" == "1" ]]; then
    echo "KERNEL_SPY_OUT=$out"
    echo "KERNEL_SPY_ESP=$out/esp"
    echo "KERNEL_SPY_UKI=$uki"
    echo "KERNEL_SPY_UKI_DIR=$out/uki"
    echo "export KERNEL_SPY_OUT KERNEL_SPY_ESP KERNEL_SPY_UKI KERNEL_SPY_UKI_DIR"
fi
