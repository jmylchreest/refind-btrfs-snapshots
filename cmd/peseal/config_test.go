package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaults_HasIdempotentSignDefault(t *testing.T) {
	d := defaults()
	assert.True(t, d.SkipAlreadySigned.IsTrue(), "skip_already_signed must default true so .path-triggered runs don't double-sign")
	assert.NotEmpty(t, d.Paths, "default watch paths must be populated so .service has something to scan")
}

func TestLoadConfigFrom_MissingFileReturnsDefaults(t *testing.T) {
	cfg, err := loadConfigFrom(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "info", cfg.LogLevel)
	assert.True(t, cfg.SkipAlreadySigned.IsTrue())
}

func TestLoadConfigFrom_OverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peseal.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
key_path: /etc/sb/key.pem
cert_path: /etc/sb/cert.pem
log_level: debug
skip_already_signed: false
paths:
  - /custom/path/*.efi
`), 0o644))

	cfg, err := loadConfigFrom(path)
	require.NoError(t, err)
	assert.Equal(t, "/etc/sb/key.pem", cfg.KeyPath)
	assert.Equal(t, "/etc/sb/cert.pem", cfg.CertPath)
	assert.Equal(t, "debug", cfg.LogLevel)
	assert.False(t, cfg.SkipAlreadySigned.IsTrue())
	assert.Equal(t, []string{"/custom/path/*.efi"}, cfg.Paths)
}
