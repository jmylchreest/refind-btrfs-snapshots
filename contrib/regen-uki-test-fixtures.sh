#!/usr/bin/env bash
# Regenerate the committed UKI test fixtures under pkg/uki/testdata/.
#
# These fixtures back pkg/uki's parsing/round-trip tests (and internal/kernel
# InspectUKI's fixture tests, which use them through pkg/uki). They're
# committed binaries; regenerating is only needed when test expectations
# change.
#
# Requires `ukify` (systemd-ukify package on Arch). Skips with a clear message
# and exits 0 if `ukify` isn't on PATH — safe to invoke from Makefile / CI
# without breaking environments that lack the tool. CI never regenerates; it
# runs the tests against the committed binaries.
#
# Differs from contrib/make-test-fixtures.sh, which builds a full fake ESP
# (UKI + BLS entries + bls.conf etc.) for kernel-spy end-to-end testing.

set -euo pipefail

OUT_DIR="$(cd "$(dirname "$0")/.." && pwd)/pkg/uki/testdata"

if ! command -v ukify >/dev/null 2>&1; then
    echo "ukify not found on PATH — skipping fixture regeneration."
    echo "Install systemd-ukify (Arch: 'pacman -S systemd-ukify') and re-run."
    exit 0
fi

mkdir -p "$OUT_DIR"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# Dummy payloads — content doesn't matter for PE-parsing tests.
printf 'fake kernel content' > "$TMP/linux"
printf 'fake initrd content' > "$TMP/initrd"
cat > "$TMP/osrel" <<'EOF'
NAME="Test Distro"
ID=test
PRETTY_NAME="Test Distro"
VERSION_ID=1.0
EOF

# Single-profile fixture
ukify build \
    --linux="$TMP/linux" --initrd="$TMP/initrd" \
    --os-release="@$TMP/osrel" --uname=6.19.0-test \
    --cmdline="root=UUID=fixture-uuid rw quiet" \
    --output="$OUT_DIR/uki-single-profile.efi" 2>&1 \
    | grep -v "is not a valid PE" || true

# Multi-profile fixture: build two per-snapshot mini-UKIs, then join into a
# base UKI. ukify auto-emits a "main" profile @0 for the base cmdline.
cat > "$TMP/profile-100.meta" <<'EOF'
ID=snapshot-100
TITLE=Snapshot 100
EOF
cat > "$TMP/profile-200.meta" <<'EOF'
ID=snapshot-200
TITLE=Snapshot 200
EOF

ukify build --profile="@$TMP/profile-100.meta" \
    --cmdline="root=UUID=fixture-uuid rw rootflags=subvol=/snap-100,subvolid=100" \
    --output="$TMP/profile-100.efi" 2>&1 | grep -v "is not a valid PE" || true

ukify build --profile="@$TMP/profile-200.meta" \
    --cmdline="root=UUID=fixture-uuid rw rootflags=subvol=/snap-200,subvolid=200" \
    --output="$TMP/profile-200.efi" 2>&1 | grep -v "is not a valid PE" || true

ukify build \
    --linux="$TMP/linux" --initrd="$TMP/initrd" \
    --os-release="@$TMP/osrel" --uname=6.19.0-test \
    --cmdline="root=UUID=fixture-uuid rw" \
    --join-profile="$TMP/profile-100.efi" \
    --join-profile="$TMP/profile-200.efi" \
    --output="$OUT_DIR/uki-multi-profile.efi" 2>&1 \
    | grep -v "is not a valid PE" || true

echo
echo "Regenerated fixtures:"
ls -l "$OUT_DIR/uki-"*.efi
