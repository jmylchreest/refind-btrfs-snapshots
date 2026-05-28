package kernel

import (
	"bytes"
	"debug/pe"
	"fmt"
	"strings"
)

// Section names defined by the systemd-stub UKI spec.
// https://uapi-group.org/specifications/specs/unified_kernel_image/
const (
	ukiSectionLinux   = ".linux"
	ukiSectionInitrd  = ".initrd"
	ukiSectionUname   = ".uname"
	ukiSectionOSRel   = ".osrel"
	ukiSectionCmdline = ".cmdline"
	ukiSectionProfile = ".profile"
)

// InspectUKI parses a UKI PE binary and extracts version + cmdline + (for
// multi-profile UKIs) the per-profile metadata. Rejects PE binaries without
// a .linux section so an EFI-stub-wrapped vmlinuz (also PE) isn't mistaken
// for a UKI.
//
// Multi-profile awareness: per the UAPI spec, repeated .profile sections act
// as separators. Sections appearing before the first .profile belong to the
// base; sections between .profile markers belong to that profile and may
// override the base. We walk in section order, tracking which profile (if
// any) we're currently inside.
func InspectUKI(path string) (*InspectedMetadata, error) {
	f, err := pe.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open UKI: %w", err)
	}
	defer f.Close()

	meta := &InspectedMetadata{Format: "uki"}
	var hasLinuxSection bool

	// -1 = currently in base sections; >=0 = index into meta.Profiles.
	currentProfile := -1

	for _, s := range f.Sections {
		name := strings.TrimRight(s.Name, "\x00")
		switch name {
		case ukiSectionLinux:
			hasLinuxSection = true

		case ukiSectionUname:
			// Per-profile .uname overrides are technically allowed but rare;
			// only the base value drives our staleness checking.
			if currentProfile == -1 {
				if v, err := readPESectionString(s); err == nil {
					meta.Version = strings.TrimSpace(v)
					meta.VersionFull = meta.Version
				}
			}

		case ukiSectionOSRel:
			if currentProfile == -1 {
				if v, err := readPESectionString(s); err == nil {
					fields := parseOSRelease(v)
					meta.OSReleaseID = fields["ID"]
					meta.OSReleasePrettyName = fields["PRETTY_NAME"]
				}
			}

		case ukiSectionCmdline:
			v, err := readPESectionString(s)
			if err != nil {
				continue
			}
			v = strings.TrimSpace(v)
			if currentProfile == -1 {
				meta.Cmdline = v
			} else {
				meta.Profiles[currentProfile].Cmdline = v
			}

		case ukiSectionProfile:
			meta.IsMultiProfile = true
			p := UKIProfile{
				Index:   len(meta.Profiles),
				Cmdline: meta.Cmdline, // inherit base; per-profile .cmdline below will override
			}
			if v, err := readPESectionString(s); err == nil {
				fields := parseOSRelease(v) // env-block format, same parser
				p.ID = fields["ID"]
				p.Title = fields["TITLE"]
			}
			meta.Profiles = append(meta.Profiles, p)
			currentProfile = p.Index
		}
	}

	if !hasLinuxSection {
		return nil, fmt.Errorf("not a UKI: PE has no %s section", ukiSectionLinux)
	}

	return meta, nil
}

func readPESectionString(s *pe.Section) (string, error) {
	data, err := s.Data()
	if err != nil {
		return "", err
	}
	if i := bytes.IndexByte(data, 0); i >= 0 {
		data = data[:i]
	}
	return string(data), nil
}

func parseOSRelease(content string) map[string]string {
	m := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		k := strings.TrimSpace(line[:eq])
		v := strings.TrimSpace(line[eq+1:])
		v = strings.Trim(v, `"'`)
		m[k] = v
	}
	return m
}
