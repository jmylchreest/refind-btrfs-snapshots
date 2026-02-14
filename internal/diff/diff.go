package diff

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/term"
)

// FileDiff represents a diff for a single file
type FileDiff struct {
	Path     string
	Original string
	Modified string
	IsNew    bool
}

// Generate creates a unified diff between original and modified content
func (fd *FileDiff) Generate() string {
	if fd.IsNew {
		return fd.generateNewFileDiff()
	}
	return fd.generateUnifiedDiff()
}

// generateNewFileDiff creates a diff for a new file
func (fd *FileDiff) generateNewFileDiff() string {
	var result strings.Builder

	result.WriteString("--- /dev/null\n")
	result.WriteString(fmt.Sprintf("+++ %s\n", fd.Path))
	result.WriteString("@@ -0,0 +1,")

	lines := strings.Split(fd.Modified, "\n")
	if fd.Modified != "" && !strings.HasSuffix(fd.Modified, "\n") {
		lines = lines[:len(lines)-1] // Remove empty line if content doesn't end with newline
	} else if fd.Modified == "" {
		lines = []string{}
	}

	result.WriteString(fmt.Sprintf("%d @@\n", len(lines)))

	for _, line := range lines {
		result.WriteString(fmt.Sprintf("+%s\n", line))
	}

	return result.String()
}

// generateUnifiedDiff creates a unified diff between original and modified content
func (fd *FileDiff) generateUnifiedDiff() string {
	var originalLines, modifiedLines []string

	if fd.Original == "" {
		originalLines = []string{}
	} else if strings.HasSuffix(fd.Original, "\n") {
		originalLines = strings.Split(fd.Original, "\n")
		originalLines = originalLines[:len(originalLines)-1] // Remove empty line at end
	} else {
		originalLines = strings.Split(fd.Original, "\n")
	}

	if fd.Modified == "" {
		modifiedLines = []string{}
	} else if strings.HasSuffix(fd.Modified, "\n") {
		modifiedLines = strings.Split(fd.Modified, "\n")
		modifiedLines = modifiedLines[:len(modifiedLines)-1] // Remove empty line at end
	} else {
		modifiedLines = strings.Split(fd.Modified, "\n")
	}

	// Simple line-by-line diff (not the most efficient, but works for our use case)
	diff := generateSimpleDiff(originalLines, modifiedLines)

	// Check if there are any actual changes (lines starting with + or -)
	hasChanges := false
	for _, line := range diff {
		if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
			hasChanges = true
			break
		}
	}

	if !hasChanges {
		return "" // No changes
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("--- %s\n", fd.Path))
	result.WriteString(fmt.Sprintf("+++ %s\n", fd.Path))

	// Generate hunk headers and content
	result.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n",
		1, len(originalLines),
		1, len(modifiedLines)))

	for _, line := range diff {
		result.WriteString(line + "\n")
	}

	return result.String()
}

// generateSimpleDiff generates a simple line-by-line diff
func generateSimpleDiff(original, modified []string) []string {
	var result []string

	// Simple algorithm: mark all original lines as removed, all new lines as added
	// This could be improved with a proper LCS algorithm, but for our purposes this works

	maxLen := len(original)
	if len(modified) > maxLen {
		maxLen = len(modified)
	}

	for i := 0; i < maxLen; i++ {
		if i < len(original) && i < len(modified) {
			if original[i] == modified[i] {
				result = append(result, " "+original[i])
			} else {
				result = append(result, "-"+original[i])
				result = append(result, "+"+modified[i])
			}
		} else if i < len(original) {
			result = append(result, "-"+original[i])
		} else if i < len(modified) {
			result = append(result, "+"+modified[i])
		}
	}

	return result
}

// PatchDiff represents a unified patch containing multiple file diffs
type PatchDiff struct {
	Files []*FileDiff
}

// NewPatchDiff creates a new patch diff
func NewPatchDiff() *PatchDiff {
	return &PatchDiff{
		Files: make([]*FileDiff, 0),
	}
}

// AddFile adds a file diff to the patch
func (pd *PatchDiff) AddFile(fileDiff *FileDiff) {
	pd.Files = append(pd.Files, fileDiff)
}

// Generate creates a unified patch from all file diffs
func (pd *PatchDiff) Generate() string {
	var result strings.Builder

	for i, fileDiff := range pd.Files {
		diff := fileDiff.Generate()
		if diff == "" {
			continue // Skip files with no changes
		}

		if i > 0 {
			result.WriteString("\n") // Separate file diffs with a blank line
		}

		result.WriteString(diff)
	}

	return result.String()
}

// ShowDiff prints a nicely formatted diff to the console
func ShowDiff(fileDiff *FileDiff) {
	patch := NewPatchDiff()
	patch.AddFile(fileDiff)
	ShowPatch(patch)
}

// ShowPatch prints a nicely formatted unified patch to the console with paging
func ShowPatch(patch *PatchDiff) {
	ShowPatchWithPager(patch, true)
}

// ShowPatchWithPager prints a nicely formatted unified patch with optional pager control
func ShowPatchWithPager(patch *PatchDiff, allowPager bool) {
	diff := patch.Generate()
	if diff == "" {
		return
	}

	// Check if we should use a pager
	if allowPager && shouldUsePager(diff) {
		showWithPager(diff)
	} else {
		showDirect(diff)
	}
}

// shouldUsePager determines if we should use a pager based on terminal size and content
func shouldUsePager(content string) bool {
	// Only use pager if we're in an interactive terminal
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return false
	}

	// Get terminal size
	width, height, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return false // If we can't get size, don't use pager
	}

	// Count lines in content
	lines := strings.Count(content, "\n")

	// Use pager if content would exceed terminal height (with some margin)
	_ = width                   // We don't currently use width, but it's available
	return lines > (height - 5) // Leave some margin
}

// showWithPager displays content using a pager
func showWithPager(content string) {
	// Try to find a suitable pager
	pagers := []string{"less", "more", "cat"}
	var pagerCmd string

	for _, pager := range pagers {
		if _, err := exec.LookPath(pager); err == nil {
			pagerCmd = pager
			break
		}
	}

	if pagerCmd == "" {
		// Fallback to direct output if no pager found
		showDirect(content)
		return
	}

	// Set up pager command
	var cmd *exec.Cmd
	if pagerCmd == "less" {
		// Use less with useful options:
		// -R: interpret ANSI color codes
		// -S: don't wrap long lines
		// -F: quit if content fits on one screen
		// -X: don't clear screen on exit
		cmd = exec.Command("less", "-RSF", "-X")
	} else {
		cmd = exec.Command(pagerCmd)
	}

	// Set up pipes
	stdin, err := cmd.StdinPipe()
	if err != nil {
		showDirect(content)
		return
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Start the pager
	if err := cmd.Start(); err != nil {
		showDirect(content)
		return
	}

	// Write colorized content to pager
	colorizedContent := colorizeContent(content)
	_, _ = stdin.Write([]byte(colorizedContent))
	_ = stdin.Close()

	// Wait for pager to finish
	_ = cmd.Wait()
}

// showDirect displays content directly to stdout
func showDirect(content string) {
	colorizedContent := colorizeContent(content)
	fmt.Print(colorizedContent)
}

// colorizeContent adds ANSI color codes to diff content
func colorizeContent(content string) string {
	var result strings.Builder

	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
			result.WriteString(fmt.Sprintf("\033[1m%s\033[0m\n", line)) // Bold
		} else if strings.HasPrefix(line, "@@") {
			result.WriteString(fmt.Sprintf("\033[36m%s\033[0m\n", line)) // Cyan
		} else if strings.HasPrefix(line, "+") {
			result.WriteString(fmt.Sprintf("\033[32m%s\033[0m\n", line)) // Green
		} else if strings.HasPrefix(line, "-") {
			result.WriteString(fmt.Sprintf("\033[31m%s\033[0m\n", line)) // Red
		} else {
			result.WriteString(fmt.Sprintf("%s\n", line))
		}
	}

	return result.String()
}

// ConfirmChanges shows a diff and asks the user for confirmation
func ConfirmChanges(fileDiff *FileDiff, autoApprove bool) bool {
	diff := fileDiff.Generate()
	if diff == "" {
		return true // No changes to confirm
	}

	// Show the diff
	ShowDiff(fileDiff)

	// Auto-approve if requested
	if autoApprove {
		fmt.Printf("Auto-approving changes to %s\n", fileDiff.Path)
		return true
	}

	// Ask for confirmation
	fmt.Printf("Apply changes to %s? [y/N]: ", fileDiff.Path)

	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		response := strings.TrimSpace(strings.ToLower(scanner.Text()))
		return response == "y" || response == "yes"
	}

	return false
}

// ConfirmPatchChanges shows a unified patch and asks the user for confirmation
func ConfirmPatchChanges(patch *PatchDiff, autoApprove bool) bool {
	diff := patch.Generate()
	if diff == "" {
		return true // No changes to confirm
	}

	// Show the patch
	ShowPatch(patch)

	// Auto-approve if requested
	if autoApprove {
		fmt.Printf("Auto-approving changes to %d file(s)\n", len(patch.Files))
		return true
	}

	// Ask for confirmation
	fmt.Printf("Apply changes to %d file(s)? [y/N]: ", len(patch.Files))

	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		response := strings.TrimSpace(strings.ToLower(scanner.Text()))
		return response == "y" || response == "yes"
	}

	return false
}
