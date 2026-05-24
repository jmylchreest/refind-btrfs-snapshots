// Package btrfs provides discovery and manipulation of btrfs filesystems
// and snapshots for the refind-btrfs-snapshots tool.
package btrfs

import (
	"encoding/xml"
	"time"
)

// Filesystem represents a btrfs filesystem
type Filesystem struct {
	UUID       string      `json:"uuid"`
	PartUUID   string      `json:"partuuid,omitempty"`
	Label      string      `json:"label,omitempty"`
	PartLabel  string      `json:"partlabel,omitempty"`
	Device     string      `json:"device"`
	MountPoint string      `json:"mountpoint"`
	Subvolume  *Subvolume  `json:"subvolume,omitempty"`
	Snapshots  []*Snapshot `json:"snapshots,omitempty"`
}

// Subvolume represents a btrfs subvolume
type Subvolume struct {
	ID          uint64    `json:"id"`
	Path        string    `json:"path"`
	ParentID    uint64    `json:"parent_id"`
	Generation  uint64    `json:"generation"`
	CreatedTime time.Time `json:"created_time"`
	IsSnapshot  bool      `json:"is_snapshot"`
	IsReadOnly  bool      `json:"is_readonly"`
}

// Snapshot represents a btrfs snapshot
type Snapshot struct {
	*Subvolume
	OriginalPath   string    `json:"original_path"`
	FilesystemPath string    `json:"filesystem_path"` // Path on filesystem for btrfs commands and file access
	SnapshotTime   time.Time `json:"snapshot_time"`
	Description    string    `json:"description,omitempty"`
	SnapperNum     int       `json:"snapper_num,omitempty"`
	SnapperType    string    `json:"snapper_type,omitempty"`
}

// SnapperInfo represents the snapper info.xml file structure
type SnapperInfo struct {
	XMLName     xml.Name `xml:"snapshot"`
	Type        string   `xml:"type"`
	Num         int      `xml:"num"`
	Date        string   `xml:"date"`
	Description string   `xml:"description"`
	Cleanup     string   `xml:"cleanup"`
}

// MountInfo represents a mounted filesystem
type MountInfo struct {
	Device     string
	Mountpoint string
	Fstype     string
	UUID       string
	PartUUID   string
	Label      string
	PartLabel  string
}
