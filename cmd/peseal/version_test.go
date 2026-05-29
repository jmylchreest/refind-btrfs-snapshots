package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	defer func() { os.Stdout = original }()

	fn()
	require.NoError(t, w.Close())

	data, err := io.ReadAll(r)
	require.NoError(t, err)
	return strings.TrimSpace(string(data))
}

func TestVersionCmd_OutputFormat(t *testing.T) {
	origV, origC, origB := version.Version, version.Commit, version.BuildTime
	t.Cleanup(func() {
		version.Version, version.Commit, version.BuildTime = origV, origC, origB
	})
	version.Version = "4.2.0"
	version.Commit = "deadbeef"
	version.BuildTime = "2026-05-29T01:00:00Z"

	output := captureStdout(t, func() { versionCmd.Run(nil, nil) })

	assert.Contains(t, output, "peseal 4.2.0")
	assert.Contains(t, output, "Commit: deadbeef")
	assert.Contains(t, output, "Built: 2026-05-29T01:00:00Z")
}
