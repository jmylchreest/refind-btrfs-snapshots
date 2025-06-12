package cmd

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionCommand(t *testing.T) {
	tests := []struct {
		name      string
		version   string
		commit    string
		buildTime string
	}{
		{
			name:      "all_values_set",
			version:   "1.2.3",
			commit:    "abc123def456",
			buildTime: "2024-01-15T10:30:00Z",
		},
		{
			name:      "dev_version",
			version:   "dev",
			commit:    "unknown",
			buildTime: "unknown",
		},
		{
			name:      "empty_values",
			version:   "",
			commit:    "",
			buildTime: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original values
			originalVersion := Version
			originalCommit := Commit
			originalBuildTime := BuildTime
			originalStdout := os.Stdout

			// Set test values
			Version = tt.version
			Commit = tt.commit
			BuildTime = tt.buildTime

			// Create a pipe to capture stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			// Create a new command for testing
			cmd := &cobra.Command{
				Use: "version",
				Run: runVersion,
			}

			// Execute the command
			runVersion(cmd, []string{})

			// Close writer and restore stdout
			w.Close()
			os.Stdout = originalStdout

			// Read the output
			var buf bytes.Buffer
			buf.ReadFrom(r)
			result := buf.String()

			// Verify output contains expected information
			expectedVersion := tt.version
			if expectedVersion == "" {
				expectedVersion = "dev"
			}

			assert.Contains(t, result, fmt.Sprintf("refind-btrfs-snapshots %s", expectedVersion))
			assert.Contains(t, result, fmt.Sprintf("Commit: %s", tt.commit))
			assert.Contains(t, result, fmt.Sprintf("Built: %s", tt.buildTime))
			assert.Contains(t, result, fmt.Sprintf("Go version: %s", runtime.Version()))

			// Restore original values
			Version = originalVersion
			Commit = originalCommit
			BuildTime = originalBuildTime
		})
	}
}

func TestVersionCommandRegistration(t *testing.T) {
	// Test that version command is properly registered with root
	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "version" {
			found = true
			break
		}
	}
	assert.True(t, found, "version command should be registered with root command")
}

func TestVersionCommandProperties(t *testing.T) {
	// Find the version command
	var versionCommand *cobra.Command
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "version" {
			versionCommand = cmd
			break
		}
	}

	require.NotNil(t, versionCommand, "version command should exist")

	// Test command properties
	assert.Equal(t, "version", versionCommand.Use)
	assert.Equal(t, "Show version information", versionCommand.Short)
	assert.Contains(t, versionCommand.Long, "Display version information including build details")
	assert.Contains(t, versionCommand.Long, "Application version")
	assert.Contains(t, versionCommand.Long, "Git commit hash")
	assert.Contains(t, versionCommand.Long, "Build timestamp")
	assert.Contains(t, versionCommand.Long, "Go version used for compilation")
}

func TestRunVersion(t *testing.T) {
	// Test the runVersion function directly
	tests := []struct {
		name      string
		version   string
		commit    string
		buildTime string
	}{
		{
			name:      "production_build",
			version:   "v1.0.0",
			commit:    "1a2b3c4d5e6f7890",
			buildTime: "2024-01-15T10:30:00Z",
		},
		{
			name:      "development_build",
			version:   "dev",
			commit:    "local-build",
			buildTime: "2024-01-15T12:00:00Z",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original values
			originalVersion := Version
			originalCommit := Commit
			originalBuildTime := BuildTime
			originalStdout := os.Stdout

			// Set test values
			Version = tt.version
			Commit = tt.commit
			BuildTime = tt.buildTime

			// Create a pipe to capture stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			// Create a mock command
			cmd := &cobra.Command{}

			// Call runVersion directly
			runVersion(cmd, []string{})

			// Close writer and restore stdout
			w.Close()
			os.Stdout = originalStdout

			// Read the output
			var buf bytes.Buffer
			buf.ReadFrom(r)
			result := buf.String()
			lines := strings.Split(strings.TrimSpace(result), "\n")

			// Verify we have exactly 4 lines
			assert.Len(t, lines, 4)

			// Verify each line
			expectedVersion := tt.version
			if expectedVersion == "" {
				expectedVersion = "dev"
			}

			assert.Equal(t, fmt.Sprintf("refind-btrfs-snapshots %s", expectedVersion), lines[0])
			assert.Equal(t, fmt.Sprintf("Commit: %s", tt.commit), lines[1])
			assert.Equal(t, fmt.Sprintf("Built: %s", tt.buildTime), lines[2])
			assert.Equal(t, fmt.Sprintf("Go version: %s", runtime.Version()), lines[3])

			// Restore original values
			Version = originalVersion
			Commit = originalCommit
			BuildTime = originalBuildTime
		})
	}
}

func TestVersionWithEmptyGlobals(t *testing.T) {
	// Test behavior when global variables are empty
	originalVersion := Version
	originalCommit := Commit
	originalBuildTime := BuildTime
	originalStdout := os.Stdout

	// Set all to empty
	Version = ""
	Commit = ""
	BuildTime = ""

	// Create a pipe to capture stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := &cobra.Command{}

	runVersion(cmd, []string{})

	// Close writer and restore stdout
	w.Close()
	os.Stdout = originalStdout

	// Read the output
	var buf bytes.Buffer
	buf.ReadFrom(r)
	result := buf.String()

	// Should show "dev" for empty version
	assert.Contains(t, result, "refind-btrfs-snapshots dev")
	assert.Contains(t, result, "Commit: ")
	assert.Contains(t, result, "Built: ")
	assert.Contains(t, result, fmt.Sprintf("Go version: %s", runtime.Version()))

	// Restore original values
	Version = originalVersion
	Commit = originalCommit
	BuildTime = originalBuildTime
}

func TestVersionOutputFormat(t *testing.T) {
	// Test that the output format is consistent and parseable
	originalVersion := Version
	originalCommit := Commit
	originalBuildTime := BuildTime
	originalStdout := os.Stdout

	Version = "1.2.3"
	Commit = "abc123"
	BuildTime = "2024-01-15T10:30:00Z"

	// Create a pipe to capture stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := &cobra.Command{}

	runVersion(cmd, []string{})

	// Close writer and restore stdout
	w.Close()
	os.Stdout = originalStdout

	// Read the output
	var buf bytes.Buffer
	buf.ReadFrom(r)
	result := buf.String()
	lines := strings.Split(strings.TrimSpace(result), "\n")

	// Test specific format patterns
	assert.Regexp(t, `^refind-btrfs-snapshots .+$`, lines[0])
	assert.Regexp(t, `^Commit: .+$`, lines[1])
	assert.Regexp(t, `^Built: .+$`, lines[2])
	assert.Regexp(t, `^Go version: go\d+\.\d+`, lines[3])

	// Restore original values
	Version = originalVersion
	Commit = originalCommit
	BuildTime = originalBuildTime
}
