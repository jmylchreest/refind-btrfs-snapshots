package esp

import (
	"strings"
)

// DeviceSpec represents a parsed device specification
type DeviceSpec struct {
	Type  string // "UUID", "PARTUUID", "LABEL", "PARTLABEL", "DEVICE"
	Value string
}

// ParseDeviceSpec parses a device specification string into its components
func ParseDeviceSpec(device string) *DeviceSpec {
	prefixes := []string{"UUID=", "PARTUUID=", "LABEL=", "PARTLABEL="}

	for _, prefix := range prefixes {
		if strings.HasPrefix(device, prefix) {
			return &DeviceSpec{
				Type:  strings.TrimSuffix(prefix, "="),
				Value: strings.TrimPrefix(device, prefix),
			}
		}
	}

	// Default to device path
	return &DeviceSpec{
		Type:  "DEVICE",
		Value: device,
	}
}

// String returns the string representation of the device specification
func (d *DeviceSpec) String() string {
	if d.Type == "DEVICE" {
		return d.Value
	}
	return d.Type + "=" + d.Value
}

// DeviceIdentifiers holds various ways to identify a device
type DeviceIdentifiers struct {
	UUID      string
	PartUUID  string
	Label     string
	PartLabel string
	Device    string
}

// MatchesSpec checks if these identifiers match the given device specification
func (d *DeviceIdentifiers) MatchesSpec(spec *DeviceSpec) bool {
	switch spec.Type {
	case "UUID":
		return d.UUID != "" && d.UUID == spec.Value
	case "PARTUUID":
		return d.PartUUID != "" && d.PartUUID == spec.Value
	case "LABEL":
		return d.Label != "" && d.Label == spec.Value
	case "PARTLABEL":
		return d.PartLabel != "" && d.PartLabel == spec.Value
	case "DEVICE":
		return d.Device == spec.Value
	default:
		return false
	}
}

// Matches checks if these identifiers match the given device string
func (d *DeviceIdentifiers) Matches(device string) bool {
	spec := ParseDeviceSpec(device)
	return d.MatchesSpec(spec)
}

// GetBestIdentifier returns the best available identifier (UUID > PartUUID > Label > PartLabel > Device)
func (d *DeviceIdentifiers) GetBestIdentifier() string {
	if d.UUID != "" {
		return d.UUID
	}
	if d.PartUUID != "" {
		return d.PartUUID
	}
	if d.Label != "" {
		return d.Label
	}
	if d.PartLabel != "" {
		return d.PartLabel
	}
	return d.Device
}

// GetIdentifierType returns the type of the best available identifier
func (d *DeviceIdentifiers) GetIdentifierType() string {
	if d.UUID != "" {
		return "UUID"
	}
	if d.PartUUID != "" {
		return "PARTUUID"
	}
	if d.Label != "" {
		return "LABEL"
	}
	if d.PartLabel != "" {
		return "PARTLABEL"
	}
	return "DEVICE"
}
