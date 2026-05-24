// Koanf parity test: runs the new typed loader against the same fixtures as
// cmd/parity_test.go and asserts byte-identical output against the goldens
// captured from the legacy viper-based code. Any drift = blocker.
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
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

const fixturesRoot = "../../cmd/testdata/parity"

type keyKind int

const (
	kindString keyKind = iota
	kindBool
	kindInt
	kindStringSlice
	kindAny
)

// trackedKeys must mirror the list in cmd/parity_test.go exactly. Each entry
// maps a dotted config key to a typed accessor over the loaded *Config and
// the accessor kind (for JSON shape). Kept here as a local mapping so this
// test stays decoupled from cmd/.
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

	flags := buildFlagSet(t, filepath.Join(dir, "flags.txt"))

	cfg, err := Load(cfgFile, flags)
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

// buildFlagSet constructs a pflag.FlagSet pre-populated with the same flags
// the real cobra commands declare, then marks the ones in flags.txt as set.
// Mirrors the viper parity harness's flag-override mechanism.
func buildFlagSet(t *testing.T, path string) *pflag.FlagSet {
	t.Helper()
	flags := pflag.NewFlagSet("parity", pflag.ContinueOnError)
	flags.String("log_level", "", "")
	flags.String("refind.config_path", "", "")
	flags.String("esp.mount_point", "", "")
	flags.Int("snapshot.selection_count", 0, "")
	flags.Bool("dry_run", false, "")
	flags.Bool("force", false, "")
	flags.Bool("generate_include", false, "")
	flags.Bool("yes", false, "")
	flags.Bool("display.local_time", false, "")
	flags.Bool("list.show_all", false, "")
	flags.Bool("list.show_size", false, "")
	flags.String("list.format", "", "")

	overrides := readKV(t, path)
	for k, v := range overrides {
		if f := flags.Lookup(k); f != nil {
			if err := f.Value.Set(v); err != nil {
				t.Fatalf("set flag %s=%s: %v", k, v, err)
			}
			f.Changed = true
		}
	}
	return flags
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
