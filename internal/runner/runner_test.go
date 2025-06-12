package runner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNew(t *testing.T) {
	// Test dry run
	dryRunner := New(true)
	if !dryRunner.IsDryRun() {
		t.Error("Expected dry run to be true")
	}

	// Test real run
	realRunner := New(false)
	if realRunner.IsDryRun() {
		t.Error("Expected dry run to be false")
	}
}

func TestDryRunner(t *testing.T) {
	runner := &DryRunner{}

	// Test IsDryRun
	if !runner.IsDryRun() {
		t.Error("DryRunner should return true for IsDryRun")
	}

	// Test Command (should not execute)
	err := runner.Command("echo", []string{"test"}, "test command")
	if err != nil {
		t.Errorf("DryRunner Command should not return error, got: %v", err)
	}

	// Test MkdirAll (should not create directory)
	tempDir := t.TempDir()
	testDir := filepath.Join(tempDir, "test-dry-mkdir")

	err = runner.MkdirAll(testDir, 0755, "test mkdir")
	if err != nil {
		t.Errorf("DryRunner MkdirAll should not return error, got: %v", err)
	}

	// Directory should not exist
	if _, err := os.Stat(testDir); !os.IsNotExist(err) {
		t.Error("DryRunner should not create actual directory")
	}

	// Test WriteFile (should not create file)
	testFile := filepath.Join(tempDir, "test-dry-file.txt")
	testContent := []byte("test content")

	err = runner.WriteFile(testFile, testContent, 0644, "test write")
	if err != nil {
		t.Errorf("DryRunner WriteFile should not return error, got: %v", err)
	}

	// File should not exist
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("DryRunner should not create actual file")
	}
}

func TestRealRunner(t *testing.T) {
	runner := &RealRunner{}

	// Test IsDryRun
	if runner.IsDryRun() {
		t.Error("RealRunner should return false for IsDryRun")
	}

	// Test Command with successful command
	err := runner.Command("echo", []string{"test"}, "test echo")
	if err != nil {
		t.Errorf("RealRunner Command with echo should not return error, got: %v", err)
	}

	// Test Command with failing command
	err = runner.Command("false", []string{}, "test false")
	if err == nil {
		t.Error("RealRunner Command with 'false' should return error")
	}

	// Test MkdirAll
	tempDir := t.TempDir()
	testDir := filepath.Join(tempDir, "test-real-mkdir")

	err = runner.MkdirAll(testDir, 0755, "test mkdir")
	if err != nil {
		t.Errorf("RealRunner MkdirAll should not return error, got: %v", err)
	}

	// Directory should exist
	if info, err := os.Stat(testDir); err != nil {
		t.Errorf("RealRunner should create directory, got error: %v", err)
	} else if !info.IsDir() {
		t.Error("Created path should be a directory")
	}

	// Test WriteFile
	testFile := filepath.Join(tempDir, "test-real-file.txt")
	testContent := []byte("test content")

	err = runner.WriteFile(testFile, testContent, 0644, "test write")
	if err != nil {
		t.Errorf("RealRunner WriteFile should not return error, got: %v", err)
	}

	// File should exist with correct content
	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Errorf("RealRunner should create file, got error: %v", err)
	}
	if string(content) != string(testContent) {
		t.Errorf("File content mismatch, expected: %s, got: %s", testContent, content)
	}
}

func TestJoinArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected string
	}{
		{
			name:     "empty args",
			args:     []string{},
			expected: "",
		},
		{
			name:     "single arg",
			args:     []string{"test"},
			expected: "test",
		},
		{
			name:     "multiple args",
			args:     []string{"echo", "hello", "world"},
			expected: "echo hello world",
		},
		{
			name:     "args with spaces",
			args:     []string{"test", "arg with spaces", "another"},
			expected: "test arg with spaces another",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := joinArgs(tt.args)
			if result != tt.expected {
				t.Errorf("joinArgs() = %v, want %v", result, tt.expected)
			}
		})
	}
}
