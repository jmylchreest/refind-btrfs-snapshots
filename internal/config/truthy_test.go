package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Truthy accepts a permissive set of common boolean spellings so config
// files written by humans (yes/no/on/off) work the same as strict YAML
// (true/false). Empty string is treated as false.

func TestTruthy_UnmarshalText_Permissive(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"true", true},
		{"True", true},
		{"TRUE", true},
		{"yes", true},
		{"Yes", true},
		{"on", true},
		{"1", true},
		{"enabled", true},
		{"enable", true},

		{"false", false},
		{"False", false},
		{"no", false},
		{"off", false},
		{"0", false},
		{"disabled", false},
		{"disable", false},
		{"", false},

		{"  true  ", true},  // whitespace tolerated
		{"  no  ", false},
	}
	for _, c := range cases {
		var got Truthy
		err := got.UnmarshalText([]byte(c.in))
		require.NoError(t, err, "input=%q", c.in)
		assert.Equal(t, c.want, got.IsTrue(), "input=%q", c.in)
	}
}

func TestTruthy_UnmarshalText_Rejects(t *testing.T) {
	var got Truthy
	err := got.UnmarshalText([]byte("maybe"))
	assert.Error(t, err)
}

func TestTruthy_IsTrue(t *testing.T) {
	var f Truthy
	assert.False(t, f.IsTrue())
	t2 := Truthy(true)
	assert.True(t, t2.IsTrue())
}

// End-to-end: a YAML file with each truthy spelling resolves through the
// koanf loader to the right boolean value.
func TestLoad_TruthyAcceptsManyForms(t *testing.T) {
	cases := []struct {
		yamlValue string
		want      bool
	}{
		{"true", true},
		{"yes", true},
		{"on", true},
		{"enabled", true},
		{"false", false},
		{"no", false},
		{"off", false},
		{"disabled", false},
	}
	for _, c := range cases {
		t.Run(c.yamlValue, func(t *testing.T) {
			tmp := t.TempDir()
			cfgPath := filepath.Join(tmp, "config.yaml")
			body := "behavior:\n  cleanup_old_snapshots: " + c.yamlValue + "\n"
			require.NoError(t, os.WriteFile(cfgPath, []byte(body), 0o644))

			cfg, err := Load(cfgPath, nil)
			require.NoError(t, err)
			assert.Equal(t, c.want, cfg.Behavior.CleanupOldSnapshots.IsTrue(),
				"yaml value %q must resolve to %v", c.yamlValue, c.want)
		})
	}
}
