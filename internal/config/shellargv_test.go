package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseShellArgv(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"peseal sign {}", []string{"peseal", "sign", "{}"}},
		{`sbsign --key /etc/x --output "{}" "{}"`, []string{"sbsign", "--key", "/etc/x", "--output", "{}", "{}"}},
		{`/usr/local/sbin/sign-uki "{}"`, []string{"/usr/local/sbin/sign-uki", "{}"}},
	}
	for _, tt := range tests {
		got, err := ParseShellArgv(tt.in)
		require.NoError(t, err, "input %q", tt.in)
		assert.Equal(t, tt.want, got, "input %q", tt.in)
	}
}

func TestLoad_SignCommand_ListForm(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
uki:
  write_entries: true
  sign_command:
    - peseal
    - sign
    - "{}"
`), 0o644))

	cfg, err := Load(path, nil)
	require.NoError(t, err)
	assert.Equal(t, ShellArgv{"peseal", "sign", "{}"}, cfg.UKI.SignCommand)
}

func TestLoad_SignCommand_StringForm(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
uki:
  write_entries: true
  sign_command: "peseal sign {}"
`), 0o644))

	cfg, err := Load(path, nil)
	require.NoError(t, err)
	assert.Equal(t, ShellArgv{"peseal", "sign", "{}"}, cfg.UKI.SignCommand,
		"string form must be shellwords-split into the same argv as the list form")
}

func TestLoad_SignCommand_AbsentMeansNil(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	require.NoError(t, os.WriteFile(path, []byte("uki:\n  write_entries: true\n"), 0o644))

	cfg, err := Load(path, nil)
	require.NoError(t, err)
	assert.Empty(t, cfg.UKI.SignCommand)
	assert.Nil(t, cfg.UKI.SignCommand.Argv(), "absent must read as nil so callers can short-circuit cleanly")
}
