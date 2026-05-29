package uki

import (
	"bytes"
	"os"
	"testing"

	"github.com/jmylchreest/refind-btrfs-snapshots/pkg/uki"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../../pkg/uki/testdata/uki-single-profile.efi")
	require.NoError(t, err)
	return b
}

func TestCloneWithCmdline_RewritesCmdline(t *testing.T) {
	src := loadFixture(t)

	out, err := CloneWithCmdline(src, "root=UUID=x rw rootflags=subvol=@/.snapshots/1/snapshot,subvolid=256")
	require.NoError(t, err)

	parsed, err := uki.Parse(bytes.NewReader(out))
	require.NoError(t, err)
	assert.Equal(t, "root=UUID=x rw rootflags=subvol=@/.snapshots/1/snapshot,subvolid=256", parsed.Cmdline())
}

func TestCloneWithCmdline_PreservesOtherSections(t *testing.T) {
	src := loadFixture(t)
	srcImg, err := uki.Parse(bytes.NewReader(src))
	require.NoError(t, err)

	out, err := CloneWithCmdline(src, "new cmdline")
	require.NoError(t, err)
	outImg, err := uki.Parse(bytes.NewReader(out))
	require.NoError(t, err)

	for _, name := range []string{uki.SectionLinux, uki.SectionInitrd, uki.SectionOSRel, uki.SectionUname} {
		srcSec := srcImg.Section(name)
		outSec := outImg.Section(name)
		if srcSec == nil {
			continue
		}
		require.NotNil(t, outSec, "section %s missing from clone", name)
		assert.Equal(t, srcSec.Data, outSec.Data, "section %s mutated", name)
	}
}

func TestCloneWithCmdline_RejectsNonUKI(t *testing.T) {
	// Empty / garbage input must error out, not silently produce something.
	_, err := CloneWithCmdline([]byte("not a PE binary"), "x")
	assert.Error(t, err)
}
