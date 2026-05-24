// Parity harness for the viper → koanf migration: captures resolved viper
// state for fixtures under testdata/parity/ and diffs against goldens.
// Update goldens with: UPDATE_GOLDEN=1 go test ./cmd/ -run TestConfigParity
package cmd

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

	"github.com/spf13/viper"
)

// keyKind is the typed accessor used for a config key, chosen to match what
// the application actually calls (viper.GetBool, GetString, GetInt, GetStringSlice,
// or untyped Get). Using typed getters in the snapshot makes the goldens reflect
// what the application sees, not viper's internal nil-for-unset representation.
type keyKind int

const (
	kindString keyKind = iota
	kindBool
	kindInt
	kindStringSlice
	kindAny
)

// trackedKeys lists every config key consulted by the codebase, with the
// accessor type used to read it. Keep this in sync with the Config struct.
var trackedKeys = []struct {
	Name string
	Kind keyKind
}{
	{"advanced.naming.menu_format", kindString},
	{"advanced.naming.rwsnap_format", kindString},
	{"behavior.cleanup_old_snapshots", kindBool},
	{"behavior.exit_on_snapshot_boot", kindBool},
	{"display.local_time", kindBool},
	{"dry_run", kindBool},
	{"esp.auto_detect", kindBool},
	{"esp.mount_point", kindString},
	{"esp.uuid", kindString},
	{"force", kindBool},
	{"generate_include", kindBool},
	{"kernel.boot_image_patterns", kindAny},
	{"kernel.stale_snapshot_action", kindString},
	{"list.format", kindString},
	{"list.show_all", kindBool},
	{"list.show_size", kindBool},
	{"log_level", kindString},
	{"refind.config_path", kindString},
	{"snapshot.destination_dir", kindString},
	{"snapshot.max_depth", kindInt},
	{"snapshot.search_directories", kindStringSlice},
	{"snapshot.selection_count", kindInt},
	{"snapshot.writable_method", kindString},
	{"yes", kindBool},
}

func TestConfigParity(t *testing.T) {
	root := "testdata/parity"
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			t.Skip("no parity fixtures present")
		}
		t.Fatalf("read fixtures dir: %v", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		t.Run(name, func(t *testing.T) {
			runParityCase(t, filepath.Join(root, name))
		})
	}
}

func runParityCase(t *testing.T, dir string) {
	t.Helper()

	saved := captureEnv()
	originalCfgFile := cfgFile
	t.Cleanup(func() {
		restoreEnv(saved)
		cfgFile = originalCfgFile
		viper.Reset()
	})

	viper.Reset()
	clearTrackedEnv()

	// Env from fixture (KEY=VALUE per line, # comments allowed, blanks ignored)
	if env := readKV(t, filepath.Join(dir, "env.txt")); env != nil {
		for k, v := range env {
			if err := os.Setenv(k, v); err != nil {
				t.Fatalf("setenv %s: %v", k, err)
			}
		}
	}

	// Config file from fixture (optional)
	cfgFile = ""
	if cfgPath := filepath.Join(dir, "config.yaml"); fileExists(cfgPath) {
		cfgFile = cfgPath
	}

	initConfig()

	// Flag overrides from fixture (simulates resolved cobra flag state).
	// Format matches viper key names exactly, e.g. "dry_run=true".
	if flags := readKV(t, filepath.Join(dir, "flags.txt")); flags != nil {
		for k, v := range flags {
			viper.Set(k, coerceFlagValue(v))
		}
	}

	actual := snapshotJSON(t)

	goldenPath := filepath.Join(dir, "expected.json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, []byte(actual), 0644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("updated golden: %s", goldenPath)
		return
	}

	expected, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("missing golden %s (re-run with UPDATE_GOLDEN=1): %v", goldenPath, err)
	}
	if string(expected) != actual {
		t.Errorf("parity drift in %s\n--- expected\n%s\n--- actual\n%s", dir, expected, actual)
	}
}

// snapshotJSON dumps all tracked keys using typed accessors and serializes
// as deterministic JSON. Keys sorted; uses the same typed getters the
// application uses, so the snapshot reflects what runtime code sees.
func snapshotJSON(t *testing.T) string {
	t.Helper()

	type entry struct {
		Key   string
		Value any
	}
	entries := make([]entry, 0, len(trackedKeys))
	for _, k := range trackedKeys {
		entries = append(entries, entry{Key: k.Name, Value: typedGet(k.Name, k.Kind)})
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

func typedGet(name string, kind keyKind) any {
	switch kind {
	case kindString:
		return viper.GetString(name)
	case kindBool:
		return viper.GetBool(name)
	case kindInt:
		return viper.GetInt(name)
	case kindStringSlice:
		return viper.GetStringSlice(name)
	case kindAny:
		return viper.Get(name)
	default:
		return nil
	}
}

// coerceFlagValue stores bools as typed values so viper.Get returns them
// in the same shape BindPFlag would, not as raw strings.
func coerceFlagValue(s string) any {
	switch strings.ToLower(s) {
	case "true":
		return true
	case "false":
		return false
	}
	return s
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

// captureEnv snapshots all REFIND_BTRFS_SNAPSHOTS_* env vars so a test
// case's modifications can be rolled back regardless of test ordering.
func captureEnv() map[string]string {
	saved := make(map[string]string)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "REFIND_BTRFS_SNAPSHOTS_") {
			k, v, _ := strings.Cut(kv, "=")
			saved[k] = v
		}
	}
	return saved
}

func restoreEnv(saved map[string]string) {
	clearTrackedEnv()
	for k, v := range saved {
		_ = os.Setenv(k, v)
	}
}

func clearTrackedEnv() {
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "REFIND_BTRFS_SNAPSHOTS_") {
			k, _, _ := strings.Cut(kv, "=")
			_ = os.Unsetenv(k)
		}
	}
}
