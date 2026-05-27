package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeEntry(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}

func TestExtractSourceEntries_ExistingBLSEntries(t *testing.T) {
	dir := t.TempDir()
	writeEntry(t, dir, "arch.conf", `title    Arch Linux
linux    /vmlinuz-linux
initrd   /intel-ucode.img
initrd   /initramfs-linux.img
options  root=UUID=abc rw quiet
`)
	writeEntry(t, dir, "bls-btrfs-snapshots-existing.conf", `title    Managed
linux    /vmlinuz-linux
options  root=UUID=abc
`)
	writeEntry(t, dir, "windows.conf", `title    Windows
efi      /EFI/Microsoft/Boot/bootmgfw.efi
`)

	srcs := extractSourceEntries(dir, "bls-btrfs-snapshots-", nil)

	require.Len(t, srcs, 1, "managed-prefix entries filtered; entries without linux= skipped")
	assert.Equal(t, "Arch Linux", srcs[0].Title)
	assert.Equal(t, "/vmlinuz-linux", srcs[0].Loader)
	assert.Equal(t, []string{"/intel-ucode.img", "/initramfs-linux.img"}, srcs[0].Initrd)
	assert.Equal(t, "root=UUID=abc rw quiet", srcs[0].Options)
}

func TestExtractSourceEntries_FallbackFromBootSets(t *testing.T) {
	dir := t.TempDir() // empty, forces fallback

	t.Setenv("HOME", t.TempDir()) // belt-and-braces; readFallbackCmdline uses absolute paths
	bootSets := []*kernel.BootSet{
		{
			KernelName: "linux",
			Layout:     kernel.LayoutSplit,
			Kernel:     &kernel.BootImage{Path: "/vmlinuz-linux"},
			Initramfs:  &kernel.BootImage{Path: "/initramfs-linux.img"},
			Microcode:  []*kernel.BootImage{{Path: "/intel-ucode.img"}},
		},
		{
			KernelName: "linux-zen",
			Layout:     kernel.LayoutUKI, // must be skipped by fallback
			UKI:        &kernel.BootImage{Path: "/EFI/Linux/linux-zen.efi"},
		},
	}

	// The fallback reads /etc/kernel/cmdline then /proc/cmdline; we don't
	// control either from a test. Skip if neither is readable in this env.
	if _, errKern := os.Stat("/etc/kernel/cmdline"); errKern != nil {
		if _, errProc := os.Stat("/proc/cmdline"); errProc != nil {
			t.Skip("no system cmdline source available in test environment")
		}
	}

	srcs := extractSourceEntries(dir, "bls-btrfs-snapshots-", bootSets)

	require.Len(t, srcs, 1, "UKI BootSet must be excluded from fallback synthesis")
	assert.Equal(t, "/vmlinuz-linux", srcs[0].Loader)
	assert.Equal(t, []string{"/initramfs-linux.img", "/intel-ucode.img"}, srcs[0].Initrd)
	assert.NotEmpty(t, srcs[0].Options, "fallback cmdline must be non-empty")
}

func TestExtractSourceEntries_NoEntriesNoBootSets(t *testing.T) {
	dir := t.TempDir()
	srcs := extractSourceEntries(dir, "bls-btrfs-snapshots-", nil)
	assert.Empty(t, srcs, "no BLS entries and no BootSets → no sources")
}

func TestStripProcCmdline(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "strips_initrd_and_boot_image",
			in:   "BOOT_IMAGE=/vmlinuz-linux root=UUID=abc rw initrd=/initramfs-linux.img quiet",
			want: "root=UUID=abc rw quiet",
		},
		{
			name: "case_insensitive_keys",
			in:   "BOOT_IMAGE=/x initrd=/y Initrd=/z BOOT_image=/w root=UUID=abc",
			want: "root=UUID=abc",
		},
		{
			name: "preserves_other_tokens",
			in:   "root=UUID=abc rw quiet splash",
			want: "root=UUID=abc rw quiet splash",
		},
		{
			name: "empty_input",
			in:   "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, stripProcCmdline(tt.in))
		})
	}
}
