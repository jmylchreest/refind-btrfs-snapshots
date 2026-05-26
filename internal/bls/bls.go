// Package bls parses Boot Loader Specification Type #1 entries.
// Spec: https://uapi-group.org/specifications/specs/boot_loader_specification/
package bls

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rs/zerolog/log"
)

// Entry is a parsed Type #1 boot entry. Unknown keys are preserved in Extra
// so round-trip writes don't drop vendor extensions.
type Entry struct {
	Path string
	ID   string

	Title             string
	Version           string
	MachineID         string
	Sort              string
	Linux             string
	Initrd            []string
	EFI               string
	Options           []string
	Devicetree        string
	DevicetreeOverlay []string
	Architecture      string

	Extra map[string]string
}

// Parse reads a single Type #1 entry from r. Keys are matched
// case-insensitively; initrd, options, and devicetree-overlay accumulate.
func Parse(r io.Reader) (*Entry, error) {
	e := &Entry{Extra: map[string]string{}}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := splitKeyValue(line)
		if !ok {
			continue
		}

		switch strings.ToLower(key) {
		case "title":
			e.Title = value
		case "version":
			e.Version = value
		case "machine-id":
			e.MachineID = value
		case "sort-key":
			e.Sort = value
		case "linux":
			e.Linux = value
		case "initrd":
			e.Initrd = append(e.Initrd, value)
		case "efi":
			e.EFI = value
		case "options":
			e.Options = append(e.Options, value)
		case "devicetree":
			e.Devicetree = value
		case "devicetree-overlay":
			e.DevicetreeOverlay = append(e.DevicetreeOverlay, value)
		case "architecture":
			e.Architecture = value
		default:
			e.Extra[key] = value
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read entry: %w", err)
	}
	return e, nil
}

func ParseFile(path string) (*Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	entry, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	entry.Path = path
	entry.ID = strings.TrimSuffix(filepath.Base(path), ".conf")
	return entry, nil
}

// ScanEntriesDir parses every *.conf in the given dirs. A single malformed
// file is logged and skipped so it can't blank out the whole result set.
func ScanEntriesDir(dirs ...string) []*Entry {
	var entries []*Entry

	for _, dir := range dirs {
		matches, err := filepath.Glob(filepath.Join(dir, "*.conf"))
		if err != nil {
			log.Trace().Err(err).Str("dir", dir).Msg("Skipping unreadable BLS entries directory")
			continue
		}
		sort.Strings(matches)

		for _, p := range matches {
			entry, err := ParseFile(p)
			if err != nil {
				log.Warn().Err(err).Str("path", p).Msg("Failed to parse BLS entry, skipping")
				continue
			}
			entries = append(entries, entry)
		}
	}

	log.Info().Int("entries", len(entries)).Msg("BLS Type #1 entry scan complete")
	return entries
}

// OptionsString joins the accumulated options lines with single spaces.
// The spec lets entries split options across lines for readability.
func (e *Entry) OptionsString() string {
	return strings.Join(e.Options, " ")
}

func splitKeyValue(line string) (string, string, bool) {
	idx := strings.IndexFunc(line, func(r rune) bool { return r == ' ' || r == '\t' })
	if idx < 0 {
		return "", "", false
	}
	key := line[:idx]
	value := strings.TrimLeft(line[idx+1:], " \t")
	return key, value, true
}
