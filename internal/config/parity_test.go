// Koanf parity test: runs the typed loader against fixtures in testdata/parity
// and asserts byte-identical output against goldens originally captured from
// the legacy viper-based code. Any drift = blocker.
package config

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const fixturesRoot = "testdata/parity"

type keyKind int

const (
	kindString keyKind = iota
	kindBool
	kindInt
	kindStringSlice
	kindAny
)

// trackedKeys lists every config key consulted by the codebase, with the
// accessor used to read it from a *Config. Keep this in sync with the Config
// struct — adding a key to Config means adding it here so parity coverage is
// complete.
var trackedKeys = []struct {
	Name   string
	Kind   keyKind
	Access func(*Config) any
}{
	{"advanced.naming.menu_format", kindString, func(c *Config) any { return c.Advanced.Naming.MenuFormat }},
	{"advanced.naming.rwsnap_format", kindString, func(c *Config) any { return c.Advanced.Naming.RwsnapFormat }},
	{"behavior.cleanup_old_snapshots", kindBool, func(c *Config) any { return c.Behavior.CleanupOldSnapshots }},
	{"behavior.exit_on_snapshot_boot", kindBool, func(c *Config) any { return c.Behavior.ExitOnSnapshotBoot }},
	{"display.local_time", kindBool, func(c *Config) any { return c.Display.LocalTime }},
	{"dry_run", kindBool, func(c *Config) any { return c.DryRun }},
	{"esp.auto_detect", kindBool, func(c *Config) any { return c.ESP.AutoDetect }},
	{"esp.mount_point", kindString, func(c *Config) any { return c.ESP.MountPoint }},
	{"esp.uuid", kindString, func(c *Config) any { return c.ESP.UUID }},
	{"force", kindBool, func(c *Config) any { return c.Force }},
	{"generate_include", kindBool, func(c *Config) any { return c.GenerateInclude }},
	{"kernel.boot_image_patterns", kindAny, func(c *Config) any { return bootPatternsAsMaps(c.Kernel.BootImagePatterns) }},
	{"kernel.stale_snapshot_action", kindString, func(c *Config) any { return c.Kernel.StaleSnapshotAction }},
	{"list.format", kindString, func(c *Config) any { return c.List.Format }},
	{"list.show_all", kindBool, func(c *Config) any { return c.List.ShowAll }},
	{"list.show_size", kindBool, func(c *Config) any { return c.List.ShowSize }},
	{"log_level", kindString, func(c *Config) any { return c.LogLevel }},
	{"refind.config_path", kindString, func(c *Config) any { return c.Refind.ConfigPath }},
	{"snapshot.destination_dir", kindString, func(c *Config) any { return c.Snapshot.DestinationDir }},
	{"snapshot.max_depth", kindInt, func(c *Config) any { return c.Snapshot.MaxDepth }},
	{"snapshot.search_directories", kindStringSlice, func(c *Config) any { return c.Snapshot.SearchDirectories }},
	{"snapshot.selection_count", kindInt, func(c *Config) any { return c.Snapshot.SelectionCount }},
	{"snapshot.writable_method", kindString, func(c *Config) any { return c.Snapshot.WritableMethod }},
	{"yes", kindBool, func(c *Config) any { return c.Yes }},
}

// bootPatternsAsMaps converts []PatternConfig into the untyped
// []map[string]any shape produced by viper.Get on YAML-loaded data, so
// JSON serialization matches the viper-captured baseline exactly.
func bootPatternsAsMaps(patterns []PatternConfig) any {
	if len(patterns) == 0 {
		return nil
	}
	out := make([]map[string]any, len(patterns))
	for i, p := range patterns {
		out[i] = map[string]any{
			"glob":         p.Glob,
			"role":         p.Role,
			"strip_prefix": p.StripPrefix,
			"strip_suffix": p.StripSuffix,
			"kernel_name":  p.KernelName,
		}
	}
	return out
}

func TestConfigParity_Koanf(t *testing.T) {
	entries, err := os.ReadDir(fixturesRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			t.Skipf("no parity fixtures at %s", fixturesRoot)
		}
		t.Fatalf("read fixtures dir: %v", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		t.Run(name, func(t *testing.T) {
			runKoanfParityCase(t, filepath.Join(fixturesRoot, name))
		})
	}
}

func runKoanfParityCase(t *testing.T, dir string) {
	t.Helper()

	clearTrackedEnv()
	t.Cleanup(clearTrackedEnv)

	if env := readKV(t, filepath.Join(dir, "env.txt")); env != nil {
		for k, v := range env {
			t.Setenv(k, v)
		}
	}

	cfgFile := ""
	if p := filepath.Join(dir, "config.yaml"); fileExists(p) {
		cfgFile = p
	}

	overrides := buildFlagOverrides(t, filepath.Join(dir, "flags.txt"))

	cfg, err := Load(cfgFile, overrides)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	actual := snapshotJSON(t, cfg)
	expected, err := os.ReadFile(filepath.Join(dir, "expected.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(expected) != actual {
		t.Errorf("parity drift in %s\n--- expected (viper baseline)\n%s\n--- actual (koanf)\n%s",
			dir, expected, actual)
	}
}

func snapshotJSON(t *testing.T, cfg *Config) string {
	t.Helper()

	type entry struct {
		Key   string
		Value any
	}
	entries := make([]entry, 0, len(trackedKeys))
	for _, k := range trackedKeys {
		entries = append(entries, entry{Key: k.Name, Value: k.Access(cfg)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })

	var buf bytes.Buffer
	buf.WriteString("{\n")
	for i, e := range entries {
		valueJSON, err := json.Marshal(e.Value)
		if err != nil {
			t.Fatalf("marshal %s: %v", e.Key, err)
		}
		fmt.Fprintf(&buf, "  %q: %s", e.Key, valueJSON)
		if i < len(entries)-1 {
			buf.WriteByte(',')
		}
		buf.WriteByte('\n')
	}
	buf.WriteString("}\n")
	return buf.String()
}

// buildFlagOverrides reads the fixture's flags.txt (already in koanf-key form)
// and produces a typed map suitable for passing as the Load flagOverrides arg.
// Booleans are coerced; everything else stays as a string.
func buildFlagOverrides(t *testing.T, path string) map[string]any {
	t.Helper()
	raw := readKV(t, path)
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		switch strings.ToLower(v) {
		case "true":
			out[k] = true
		case "false":
			out[k] = false
		default:
			if i, err := strconv.Atoi(v); err == nil {
				out[k] = i
			} else {
				out[k] = v
			}
		}
	}
	return out
}

func readKV(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	out := make(map[string]string)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("malformed line in %s: %q", path, line)
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return out
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func clearTrackedEnv() {
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "REFIND_BTRFS_SNAPSHOTS_") {
			k, _, _ := strings.Cut(kv, "=")
			_ = os.Unsetenv(k)
		}
	}
}
