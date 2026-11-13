package btrfs

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubvolumePathFromMountInfo(t *testing.T) {
	mounts := `1 0 0:1 /@ / rw - btrfs /dev/test rw
2 1 0:1 / /mnt/top rw - btrfs /dev/test rw
3 1 0:1 /@copies /copies rw - btrfs /dev/test rw
4 1 0:1 /@/some\040dir /bind\040mount rw - btrfs /dev/test rw
5 1 0:2 / /tmp rw - tmpfs tmpfs rw
`
	for _, tc := range []struct{ path, want string }{
		{"/.copies/new", "@/.copies/new"},
		{"/mnt/top/copies/new", "copies/new"},
		{"/copies/new", "@copies/new"},
		{"/bind mount/new", "@/some dir/new"},
		{"/copies-other/new", "@/copies-other/new"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			got, err := subvolumePathFromMountInfo(tc.path, strings.NewReader(mounts))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
	_, err := subvolumePathFromMountInfo("/tmp/new", strings.NewReader(mounts))
	require.Error(t, err)
}
