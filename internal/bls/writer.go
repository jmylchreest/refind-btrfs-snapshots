package bls

import (
	"fmt"
	"io"
	"strings"
)

// WriteEntry serialises a Type #1 entry. Keys appear in canonical order so
// diffs against on-disk files are deterministic across runs.
func WriteEntry(w io.Writer, e *Entry) error {
	if err := writeKey(w, "title", e.Title); err != nil {
		return err
	}
	if err := writeKey(w, "version", e.Version); err != nil {
		return err
	}
	if err := writeKey(w, "machine-id", e.MachineID); err != nil {
		return err
	}
	if err := writeKey(w, "sort-key", e.Sort); err != nil {
		return err
	}
	if err := writeKey(w, "architecture", e.Architecture); err != nil {
		return err
	}
	if err := writeKey(w, "linux", e.Linux); err != nil {
		return err
	}
	for _, i := range e.Initrd {
		if err := writeKey(w, "initrd", i); err != nil {
			return err
		}
	}
	if err := writeKey(w, "efi", e.EFI); err != nil {
		return err
	}
	if err := writeKey(w, "devicetree", e.Devicetree); err != nil {
		return err
	}
	for _, d := range e.DevicetreeOverlay {
		if err := writeKey(w, "devicetree-overlay", d); err != nil {
			return err
		}
	}
	for _, o := range e.Options {
		if err := writeKey(w, "options", o); err != nil {
			return err
		}
	}
	for k, v := range e.Extra {
		if err := writeKey(w, k, v); err != nil {
			return err
		}
	}
	return nil
}

func writeKey(w io.Writer, key, value string) error {
	if value == "" {
		return nil
	}
	_, err := fmt.Fprintf(w, "%s %s\n", key, value)
	return err
}

// EntryFilename returns "<prefix><sanitised-id>.conf". Spaces in id become
// dashes so filenames stay shell-safe; the spec doesn't forbid spaces but
// they cause friction with most tooling.
func EntryFilename(prefix, id string) string {
	sanitised := strings.Map(func(r rune) rune {
		switch r {
		case ' ':
			return '-'
		default:
			return r
		}
	}, id)
	return prefix + sanitised + ".conf"
}
