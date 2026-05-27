package bls

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Documented contract:
//   - WriteEntry emits a BLS Type #1 .conf body parseable by Parse().
//     Round-trip equality on the spec-defined fields.
//   - Keys appear in a stable canonical order so diffs against the file
//     on disk are deterministic.
//   - Multi-value keys (initrd, options, devicetree-overlay) emit one line
//     per value.
//   - EntryFilename returns "<prefix><id>.conf" — Entry.ID is the basis,
//     never a user-supplied title (which may contain spaces / specials).

func TestWriteEntry_RoundTripsThroughParser(t *testing.T) {
	in := &Entry{
		Title:        "Arch Linux (snapshot 2025-02-14T10:00:00Z)",
		Version:      "6.19.0-2-cachyos",
		MachineID:    "abcdef0123456789abcdef0123456789",
		Sort:         "bls-btrfs-snapshots-256",
		Linux:        "/vmlinuz-linux-cachyos",
		Initrd:       []string{"/amd-ucode.img", "/initramfs-linux-cachyos.img"},
		Options:      []string{"root=UUID=test-uuid rw quiet rootflags=subvol=/@/.snapshots/73/snapshot,subvolid=256"},
		Architecture: "x64",
	}

	var buf bytes.Buffer
	require.NoError(t, WriteEntry(&buf, in))

	out, err := Parse(&buf)
	require.NoError(t, err)

	assert.Equal(t, in.Title, out.Title)
	assert.Equal(t, in.Version, out.Version)
	assert.Equal(t, in.MachineID, out.MachineID)
	assert.Equal(t, in.Sort, out.Sort)
	assert.Equal(t, in.Linux, out.Linux)
	assert.Equal(t, in.Architecture, out.Architecture)
	assert.Equal(t, in.Initrd, out.Initrd)
	assert.Equal(t, in.OptionsString(), out.OptionsString())
}

func TestWriteEntry_CanonicalKeyOrder(t *testing.T) {
	// Same logical content emitted in canonical order: title, version,
	// machine-id, sort-key, architecture, linux, initrd*, options, then
	// extras (devicetree, devicetree-overlay*, efi, custom). Stable
	// order means diff-on-disk is deterministic across runs.
	e := &Entry{
		Architecture: "x64",
		Options:      []string{"root=UUID=x rw"},
		Initrd:       []string{"/initrd.img"},
		Linux:        "/vmlinuz",
		MachineID:    "m",
		Sort:         "s",
		Title:        "T",
		Version:      "V",
	}
	var buf bytes.Buffer
	require.NoError(t, WriteEntry(&buf, e))

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	// Find the index of each key; assert ascending.
	indexOf := func(key string) int {
		for i, l := range lines {
			if strings.HasPrefix(l, key+" ") || strings.HasPrefix(l, key+"\t") {
				return i
			}
		}
		return -1
	}
	wantOrder := []string{"title", "version", "machine-id", "sort-key", "architecture", "linux", "initrd", "options"}
	prev := -1
	for _, key := range wantOrder {
		idx := indexOf(key)
		require.GreaterOrEqual(t, idx, 0, "missing key %q in output:\n%s", key, buf.String())
		assert.Greater(t, idx, prev, "key %q out of order (idx=%d, previous=%d)\noutput:\n%s", key, idx, prev, buf.String())
		prev = idx
	}
}

func TestWriteEntry_MultipleInitrds(t *testing.T) {
	e := &Entry{
		Title:  "Linux",
		Linux:  "/vmlinuz",
		Initrd: []string{"/intel-ucode.img", "/amd-ucode.img", "/initramfs-linux.img"},
	}
	var buf bytes.Buffer
	require.NoError(t, WriteEntry(&buf, e))

	body := buf.String()
	assert.Equal(t, 3, strings.Count(body, "\ninitrd ")+strings.Count(body, "^initrd "),
		"expected 3 initrd lines in:\n%s", body)
}

func TestWriteEntry_DeterministicOutput(t *testing.T) {
	// Identical input → identical bytes, across runs. Important for the
	// patch/diff machinery — drift here causes spurious diffs.
	e := &Entry{
		Title:   "T",
		Version: "V",
		Linux:   "/vmlinuz",
		Initrd:  []string{"/a.img", "/b.img"},
		Options: []string{"root=UUID=x rw"},
	}
	var a, b bytes.Buffer
	require.NoError(t, WriteEntry(&a, e))
	require.NoError(t, WriteEntry(&b, e))
	assert.Equal(t, a.String(), b.String())
}

func TestWriteEntry_OmitsEmptyFields(t *testing.T) {
	// A minimal Entry should produce only the keys that have values —
	// no empty `version` or `machine-id` lines polluting the file.
	e := &Entry{
		Title: "Minimal",
		Linux: "/vmlinuz",
	}
	var buf bytes.Buffer
	require.NoError(t, WriteEntry(&buf, e))

	out := buf.String()
	assert.NotContains(t, out, "version")
	assert.NotContains(t, out, "machine-id")
	assert.NotContains(t, out, "sort-key")
	assert.NotContains(t, out, "architecture")
	assert.NotContains(t, out, "initrd")
	assert.NotContains(t, out, "options")
	assert.Contains(t, out, "title Minimal")
	assert.Contains(t, out, "linux /vmlinuz")
}

func TestEntryFilename(t *testing.T) {
	cases := []struct {
		prefix string
		id     string
		want   string
	}{
		{"bls-btrfs-snapshots-", "256", "bls-btrfs-snapshots-256.conf"},
		{"", "arch-rolling", "arch-rolling.conf"},
		{"bls-btrfs-snapshots-", "snapshot 2025", "bls-btrfs-snapshots-snapshot-2025.conf"},
	}
	for _, c := range cases {
		got := EntryFilename(c.prefix, c.id)
		assert.Equal(t, c.want, got, "prefix=%q id=%q", c.prefix, c.id)
	}
}
