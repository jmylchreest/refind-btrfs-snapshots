package uki

import (
	"testing"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/stretchr/testify/assert"
)

func snap(id uint64, path string) *btrfs.Snapshot {
	return &btrfs.Snapshot{Subvolume: &btrfs.Subvolume{ID: id, Path: path}}
}

func TestRewriteCmdline(t *testing.T) {
	tests := []struct {
		name string
		base string
		snap *btrfs.Snapshot
		want string
	}{
		{
			name: "empty_base_returns_empty",
			base: "",
			snap: snap(256, "@/.snapshots/1/snapshot"),
			want: "",
		},
		{
			name: "preserves_bare_at_prefix",
			base: "root=UUID=x rw rootflags=subvol=@,subvolid=5",
			snap: snap(256, "@/.snapshots/1/snapshot"),
			want: "root=UUID=x rw rootflags=subvol=@/.snapshots/1/snapshot,subvolid=256",
		},
		{
			name: "preserves_slash_at_prefix",
			base: "root=UUID=x rw rootflags=subvol=/@,subvolid=5",
			snap: snap(256, "@/.snapshots/1/snapshot"),
			want: "root=UUID=x rw rootflags=subvol=/@/.snapshots/1/snapshot,subvolid=256",
		},
		{
			name: "adds_rootflags_when_missing",
			base: "root=UUID=x rw quiet",
			snap: snap(42, "@/.snapshots/9/snapshot"),
			want: "root=UUID=x rw quiet rootflags=subvol=@/.snapshots/9/snapshot,subvolid=42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rewriteCmdline(tt.base, tt.snap)
			assert.Equal(t, tt.want, got)
		})
	}
}
