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

// trackedKeys lists every config key actually consulted by the codebase.
// Anything new added here must also be added to the koanf Config struct
// during the migration, and the parity snapshot will detect the drift.
var trackedKeys = []string{
	"advanced.naming.menu_format",
	"advanced.naming.rwsnap_format",
	"behavior.cleanup_old_snapshots",
	"behavior.exit_on_snapshot_boot",
	"display.local_time",
	"dry_run",
	"esp.auto_detect",
	"esp.mount_point",
	"esp.uuid",
	"force",
	"generate_include",
	"kernel.boot_image_patterns",
	"kernel.stale_snapshot_action",
	"list.format",
	"list.show_all",
	"list.show_size",
	"log_level",
	"refind.config_path",
	"snapshot.destination_dir",
	"snapshot.max_depth",
	"snapshot.search_directories",
	"snapshot.selection_count",
	"snapshot.writable_method",
	"yes",
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

// snapshotJSON dumps all tracked keys as deterministic JSON.
// Keys are sorted; values use json.Marshal for stable formatting.
func snapshotJSON(t *testing.T) string {
	t.Helper()

	type entry struct {
		Key   string
		Value any
	}
	entries := make([]entry, 0, len(trackedKeys))
	for _, k := range trackedKeys {
		entries = append(entries, entry{Key: k, Value: viper.Get(k)})
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
