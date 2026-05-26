package btrfs

import "github.com/jmylchreest/refind-btrfs-snapshots/internal/esp"

// deviceIdentifiers returns the DeviceIdentifiers for this filesystem.
func (f *Filesystem) deviceIdentifiers() *esp.DeviceIdentifiers {
	return &esp.DeviceIdentifiers{
		UUID:      f.UUID,
		PartUUID:  f.PartUUID,
		Label:     f.Label,
		PartLabel: f.PartLabel,
		Device:    f.Device,
	}
}

// GetBestIdentifier returns the best available identifier for the filesystem (UUID > PartUUID > Label > PartLabel > Device)
func (f *Filesystem) GetBestIdentifier() string {
	return f.deviceIdentifiers().GetBestIdentifier()
}

// GetIdentifierType returns the type of the best available identifier
func (f *Filesystem) GetIdentifierType() string {
	return f.deviceIdentifiers().GetIdentifierType()
}

// MatchesDevice checks if a device specification matches this filesystem using any available identifier
func (f *Filesystem) MatchesDevice(device string) bool {
	return f.deviceIdentifiers().Matches(device)
}
