package uki

import "strings"

// Profile describes one profile in a multi-profile UKI. Profiles are
// delimited by repeated .profile sections (UAPI spec); each profile's
// effective cmdline is the .cmdline section appearing between its .profile
// marker and the next, falling back to the base .cmdline (the one before
// the first .profile) when no per-profile override is supplied.
type Profile struct {
	Index   int
	ID      string
	Title   string
	Cmdline string
}

// Cmdline returns the base (pre-profile) kernel command line, with the
// UAPI-mandated trailing NUL stripped. Returns "" if no .cmdline section
// exists, or if .cmdline only appears under a .profile marker (i.e. no
// base cmdline).
func (img *Image) Cmdline() string {
	for _, s := range img.sections {
		if s.Name == SectionProfile {
			return ""
		}
		if s.Name == SectionCmdline {
			return trimSectionString(s.Data)
		}
	}
	return ""
}

// SetCmdline sets the base .cmdline section, appending the UAPI-required
// trailing NUL byte. Replaces an existing .cmdline in place; appends if
// absent.
func (img *Image) SetCmdline(s string) {
	img.SetSection(SectionCmdline, append([]byte(s), 0))
}

// Uname returns the kernel version string from .uname (NUL/whitespace
// trimmed). Empty if absent.
func (img *Image) Uname() string {
	if s := img.Section(SectionUname); s != nil {
		return trimSectionString(s.Data)
	}
	return ""
}

// OSRelease returns the parsed os-release key/value pairs from .osrel.
// Values are stripped of surrounding single or double quotes. Lines
// without an '=' are skipped. Returns an empty map if .osrel is absent.
func (img *Image) OSRelease() map[string]string {
	if s := img.Section(SectionOSRel); s != nil {
		return parseOSRelease(trimSectionString(s.Data))
	}
	return map[string]string{}
}

// Profiles returns per-profile metadata for multi-profile UKIs. Returns
// an empty slice for single-profile UKIs (no .profile sections present).
// Each profile's Cmdline is the per-profile override if one was supplied,
// otherwise the base cmdline (Image.Cmdline()).
func (img *Image) Profiles() []Profile {
	var (
		out         []Profile
		baseCmdline = img.Cmdline()
		current     = -1 // -1 == base region; 0+ == out[current]
	)
	for _, s := range img.sections {
		switch s.Name {
		case SectionProfile:
			p := Profile{Index: len(out), Cmdline: baseCmdline}
			fields := parseOSRelease(trimSectionString(s.Data))
			p.ID = fields["ID"]
			p.Title = fields["TITLE"]
			out = append(out, p)
			current = p.Index
		case SectionCmdline:
			if current >= 0 {
				out[current].Cmdline = trimSectionString(s.Data)
			}
		}
	}
	return out
}

func trimSectionString(data []byte) string {
	s := string(data)
	if i := strings.IndexByte(s, 0); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
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
