package kernel

import (
	"bytes"
	"debug/pe"
	"fmt"
	"strings"
)

// UKI PE section names defined by the systemd-stub specification.
// See: https://uapi-group.org/specifications/specs/unified_kernel_image/
const (
	ukiSectionLinux   = ".linux"
	ukiSectionInitrd  = ".initrd"
	ukiSectionUname   = ".uname"
	ukiSectionOSRel   = ".osrel"
	ukiSectionCmdline = ".cmdline"
)

// InspectUKI parses a Unified Kernel Image (PE/EFI binary) and extracts
// metadata from the standard systemd-stub sections (.osrel, .uname, .cmdline).
//
// Returns an error when the file is not a PE binary, or is a PE without the
// mandatory .linux section (e.g., an EFI-stub-wrapped vmlinuz which is not a UKI).
func InspectUKI(path string) (*InspectedMetadata, error) {
	f, err := pe.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open UKI: %w", err)
	}
	defer f.Close()

	meta := &InspectedMetadata{Format: "uki"}
	var hasLinuxSection bool

	for _, s := range f.Sections {
		name := strings.TrimRight(s.Name, "\x00")
		switch name {
		case ukiSectionLinux:
			hasLinuxSection = true
		case ukiSectionUname:
			if v, err := readPESectionString(s); err == nil {
				meta.Version = strings.TrimSpace(v)
				meta.VersionFull = meta.Version
			}
		case ukiSectionCmdline:
			if v, err := readPESectionString(s); err == nil {
				meta.Cmdline = strings.TrimSpace(v)
			}
		case ukiSectionOSRel:
			if v, err := readPESectionString(s); err == nil {
				fields := parseOSRelease(v)
				meta.OSReleaseID = fields["ID"]
				meta.OSReleasePrettyName = fields["PRETTY_NAME"]
			}
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
