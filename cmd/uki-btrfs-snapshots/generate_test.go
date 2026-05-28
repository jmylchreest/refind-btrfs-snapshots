package main

import (
	"testing"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/stretchr/testify/assert"
)

func TestFilterUKIBootSets_KeepsOnlyUKILayout(t *testing.T) {
	sets := []*kernel.BootSet{
		{KernelName: "linux", Layout: kernel.LayoutSplit, Kernel: &kernel.BootImage{Path: "/vmlinuz-linux"}},
		{KernelName: "linux-zen", Layout: kernel.LayoutUKI, UKI: &kernel.BootImage{Path: "/EFI/Linux/linux-zen.efi"}},
		{KernelName: "linux-bls", Layout: kernel.LayoutBLS, Kernel: &kernel.BootImage{Path: "/vmlinuz-bls"}},
	}
	out := filterUKIBootSets(sets, "uki-btrfs-snapshots-")
	if assert.Len(t, out, 1) {
		assert.Equal(t, kernel.LayoutUKI, out[0].Layout)
	}
}

func TestFilterUKIBootSets_DropsOwnManagedPrefix(t *testing.T) {
	// Never clone our own previous output as a source — that would
	// recursively double the clone count on every run.
	sets := []*kernel.BootSet{
		{Layout: kernel.LayoutUKI, UKI: &kernel.BootImage{Path: "/EFI/Linux/linux.efi"}},
		{Layout: kernel.LayoutUKI, UKI: &kernel.BootImage{Path: "/EFI/Linux/uki-btrfs-snapshots-100-linux.efi"}},
	}
	out := filterUKIBootSets(sets, "uki-btrfs-snapshots-")
	if assert.Len(t, out, 1) {
		assert.Equal(t, "/EFI/Linux/linux.efi", out[0].UKI.Path)
	}
}

func TestFilterUKIBootSets_NilUKIIgnored(t *testing.T) {
	sets := []*kernel.BootSet{
		{Layout: kernel.LayoutUKI, UKI: nil},
	}
	assert.Empty(t, filterUKIBootSets(sets, ""))
}
