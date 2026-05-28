package kernel

import (
	"fmt"

	"github.com/jmylchreest/refind-btrfs-snapshots/pkg/uki"
)

// InspectUKI parses a UKI PE binary into its base metadata plus any per-
// profile overrides. Rejects PE binaries without a .linux section so an
// EFI-stub-wrapped vmlinuz isn't mistaken for a UKI. Backed by pkg/uki.
func InspectUKI(path string) (*InspectedMetadata, error) {
	img, err := uki.ParseFile(path)
	if err != nil {
		return nil, fmt.Errorf("open UKI: %w", err)
	}

	meta := &InspectedMetadata{
		Format:  "uki",
		Version: img.Uname(),
		Cmdline: img.Cmdline(),
	}
	meta.VersionFull = meta.Version

	osrel := img.OSRelease()
	meta.OSReleaseID = osrel["ID"]
	meta.OSReleasePrettyName = osrel["PRETTY_NAME"]

	profiles := img.Profiles()
	if len(profiles) > 0 {
		meta.IsMultiProfile = true
		meta.Profiles = make([]UKIProfile, len(profiles))
		for i, p := range profiles {
			meta.Profiles[i] = UKIProfile{
				Index:   p.Index,
				ID:      p.ID,
				Title:   p.Title,
				Cmdline: p.Cmdline,
			}
		}
	}

	return meta, nil
}
