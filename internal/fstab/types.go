// Package fstab parses and updates Linux fstab files, with snapshot-aware
// helpers that rewrite root-subvolume mount entries to point at a btrfs
// snapshot instead of the live subvolume.
package fstab

// Entry represents a single fstab entry
type Entry struct {
	Device     string `json:"device"`
	Mountpoint string `json:"mountpoint"`
	FSType     string `json:"fstype"`
	Options    string `json:"options"`
	Dump       string `json:"dump"`
	Pass       string `json:"pass"`
	Original   string `json:"original"`
}

// Fstab represents a parsed fstab file, preserving the original line ordering
// (including comments and blank lines) for round-trip output.
type Fstab struct {
	Entries []*Entry `json:"entries"`
	Lines   []string `json:"lines"`
}

// Manager handles fstab operations
type Manager struct{}

// NewManager creates a new fstab manager
func NewManager() *Manager {
	return &Manager{}
}
