package diff

import (
	"strings"
	"testing"
)

func TestFileDiff_Generate(t *testing.T) {
	tests := []struct {
		name     string
		fileDiff *FileDiff
		wantErr  bool
	}{
		{
			name: "new file",
			fileDiff: &FileDiff{
				Path:     "/test/new.txt",
				Original: "",
				Modified: "new content\n",
				IsNew:    true,
			},
			wantErr: false,
		},
		{
			name: "modified file",
			fileDiff: &FileDiff{
				Path:     "/test/existing.txt",
				Original: "old content\n",
				Modified: "new content\n",
				IsNew:    false,
			},
			wantErr: false,
		},
		{
			name: "no changes",
			fileDiff: &FileDiff{
				Path:     "/test/same.txt",
				Original: "same content\n",
				Modified: "same content\n",
				IsNew:    false,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fileDiff.Generate()
			if tt.name == "no changes" && got != "" {
				t.Errorf("FileDiff.Generate() for no changes should return empty string, got %v", got)
			}
			if tt.name != "no changes" && got == "" {
				t.Errorf("FileDiff.Generate() should return diff content, got empty string")
			}
			if tt.name == "new file" && !strings.Contains(got, "+new content") {
				t.Errorf("FileDiff.Generate() for new file should contain added content")
			}
			if tt.name == "modified file" && (!strings.Contains(got, "-old content") || !strings.Contains(got, "+new content")) {
				t.Errorf("FileDiff.Generate() for modified file should contain both old and new content")
			}
		})
	}
}

func TestFileDiff_generateNewFileDiff(t *testing.T) {
	fileDiff := &FileDiff{
		Path:     "/test/new.txt",
		Original: "",
		Modified: "line1\nline2\n",
		IsNew:    true,
	}

	result := fileDiff.generateNewFileDiff()

	if !strings.Contains(result, "--- /dev/null") {
		t.Error("New file diff should contain '--- /dev/null'")
	}
	if !strings.Contains(result, "+++ /test/new.txt") {
		t.Error("New file diff should contain '+++ /test/new.txt'")
	}
	if !strings.Contains(result, "+line1") {
		t.Error("New file diff should contain '+line1'")
	}
	if !strings.Contains(result, "+line2") {
		t.Error("New file diff should contain '+line2'")
	}
}

func TestGenerateSimpleDiff(t *testing.T) {
	tests := []struct {
		name     string
		original []string
		modified []string
		want     []string
	}{
		{
			name:     "identical",
			original: []string{"line1", "line2"},
			modified: []string{"line1", "line2"},
			want:     []string{" line1", " line2"},
		},
		{
			name:     "addition",
			original: []string{"line1"},
			modified: []string{"line1", "line2"},
			want:     []string{" line1", "+line2"},
		},
		{
			name:     "removal",
			original: []string{"line1", "line2"},
			modified: []string{"line1"},
			want:     []string{" line1", "-line2"},
		},
		{
			name:     "modification",
			original: []string{"old line"},
			modified: []string{"new line"},
			want:     []string{"-old line", "+new line"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateSimpleDiff(tt.original, tt.modified)
			if len(got) != len(tt.want) {
				t.Errorf("generateSimpleDiff() length = %v, want %v", len(got), len(tt.want))
				return
			}
			for i, line := range got {
				if line != tt.want[i] {
					t.Errorf("generateSimpleDiff() line %d = %v, want %v", i, line, tt.want[i])
				}
			}
		})
	}
}

func TestPatchDiff(t *testing.T) {
	patch := NewPatchDiff()

	if patch == nil {
		t.Fatal("NewPatchDiff() returned nil")
	}

	if len(patch.Files) != 0 {
		t.Errorf("NewPatchDiff() should create empty patch, got %d files", len(patch.Files))
	}

	// Test AddFile
	fileDiff := &FileDiff{
		Path:     "/test/file.txt",
		Original: "old",
		Modified: "new",
		IsNew:    false,
	}

	patch.AddFile(fileDiff)

	if len(patch.Files) != 1 {
		t.Errorf("AddFile() should add file, got %d files", len(patch.Files))
	}

	if patch.Files[0] != fileDiff {
		t.Error("AddFile() should add the correct file")
	}

	// Test Generate
	generated := patch.Generate()
	if generated == "" {
		t.Error("PatchDiff.Generate() should return content for modified files")
	}
}

func TestConfirmChanges(t *testing.T) {
	fileDiff := &FileDiff{
		Path:     "/test/file.txt",
		Original: "",
		Modified: "",
		IsNew:    false,
	}

	// Test auto-approve
	result := ConfirmChanges(fileDiff, true)
	if !result {
		t.Error("ConfirmChanges() with autoApprove should return true")
	}

	// Test no changes
	result = ConfirmChanges(fileDiff, false)
	if !result {
		t.Error("ConfirmChanges() with no changes should return true")
	}
}

func TestConfirmPatchChanges(t *testing.T) {
	patch := NewPatchDiff()

	// Test auto-approve
	result := ConfirmPatchChanges(patch, true)
	if !result {
		t.Error("ConfirmPatchChanges() with autoApprove should return true")
	}

	// Test no changes
	result = ConfirmPatchChanges(patch, false)
	if !result {
		t.Error("ConfirmPatchChanges() with no changes should return true")
	}
}

func TestShouldUsePager(t *testing.T) {
	// Test with short content
	shortContent := "short content"
	// Note: This test may fail in non-terminal environments, which is expected
	result := shouldUsePager(shortContent)
	// We can't reliably test this in all environments, so just ensure it doesn't panic
	_ = result

	// Test with long content
	longContent := strings.Repeat("line\n", 100)
	result = shouldUsePager(longContent)
	_ = result
}

func TestColorizeContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "plus line",
			content: "+added line",
			want:    "\033[32m+added line\033[0m\n",
		},
		{
			name:    "minus line",
			content: "-removed line",
			want:    "\033[31m-removed line\033[0m\n",
		},
		{
			name:    "context line",
			content: " context line",
			want:    " context line\n",
		},
		{
			name:    "header line",
			content: "+++ file.txt",
			want:    "\033[1m+++ file.txt\033[0m\n",
		},
		{
			name:    "hunk header",
			content: "@@ -1,3 +1,4 @@",
			want:    "\033[36m@@ -1,3 +1,4 @@\033[0m\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := colorizeContent(tt.content)
			if got != tt.want {
				t.Errorf("colorizeContent() = %q, want %q", got, tt.want)
			}
		})
	}
}
