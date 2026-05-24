package cmd

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunVersion_OutputFormat exercises the only thing about runVersion
// worth testing: that it writes the four expected lines with the build
// metadata values substituted in.
func TestRunVersion_OutputFormat(t *testing.T) {
	originalVersion, originalCommit, originalBuildTime := Version, Commit, BuildTime
	t.Cleanup(func() {
		Version, Commit, BuildTime = originalVersion, originalCommit, originalBuildTime
	})
	Version = "1.2.3"
	Commit = "abcdef1"
	BuildTime = "2026-01-01T00:00:00Z"

	output := captureStdout(t, func() { runVersion(nil, nil) })

	assert.Contains(t, output, "refind-btrfs-snapshots 1.2.3")
	assert.Contains(t, output, "Commit: abcdef1")
	assert.Contains(t, output, "Built: 2026-01-01T00:00:00Z")
	assert.Contains(t, output, "Go version: go")
}

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
