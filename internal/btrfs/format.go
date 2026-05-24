package btrfs

import (
	"strings"
	"time"
)

// FormatSnapshotTimeForDisplay formats a snapshot timestamp in a fixed
// "YYYY-MM-DD HH:MM" layout for human-readable list output.
func FormatSnapshotTimeForDisplay(t time.Time, useLocalTime bool) string {
	if useLocalTime {
		return t.Local().Format("2006-01-02 15:04")
	}
	return t.UTC().Format("2006-01-02 15:04")
}

// FormatSnapshotTimeForMenu formats a snapshot time for menu entries using the
// supplied template. Two template flavors are supported and auto-detected:
//
//   - Go's reference-time layout (e.g. "2006-01-02 15:04:05")
//   - User-friendly placeholders: YYYY YY MM DD HH mm ss
//     (e.g. "btrfs snapshot: YYYY/MM/DD-HH:mm" → "btrfs snapshot: 2025/06/14-17:32")
//
// See https://pkg.go.dev/time#Time.Format for Go-format details.
func FormatSnapshotTimeForMenu(t time.Time, template string, useLocalTime bool) string {
	timeToUse := t.UTC()
	if useLocalTime {
		timeToUse = t.Local()
	}

	if strings.Contains(template, "YYYY") || strings.Contains(template, "YY") ||
		strings.Contains(template, "MM") || strings.Contains(template, "DD") ||
		strings.Contains(template, "HH") || strings.Contains(template, "mm") ||
		strings.Contains(template, "ss") {
		result := template
		result = strings.ReplaceAll(result, "YYYY", timeToUse.Format("2006"))
		result = strings.ReplaceAll(result, "YY", timeToUse.Format("06"))
		result = strings.ReplaceAll(result, "MM", timeToUse.Format("01"))
		result = strings.ReplaceAll(result, "DD", timeToUse.Format("02"))
		result = strings.ReplaceAll(result, "HH", timeToUse.Format("15"))
		result = strings.ReplaceAll(result, "mm", timeToUse.Format("04"))
		result = strings.ReplaceAll(result, "ss", timeToUse.Format("05"))
		return result
	}

	return timeToUse.Format(template)
}

// FormatSnapshotTimeForRwsnap formats a snapshot time like FormatSnapshotTimeForMenu
// then sanitizes the result so it is safe to use as a filesystem path component
// (no slashes, colons, shell-special characters, or spaces).
func FormatSnapshotTimeForRwsnap(t time.Time, template string, useLocalTime bool) string {
	result := FormatSnapshotTimeForMenu(t, template, useLocalTime)

	for _, ch := range []string{"/", "\\", ":", "<", ">", "|", "?", "*"} {
		result = strings.ReplaceAll(result, ch, "-")
	}
	return strings.ReplaceAll(result, " ", "_")
}
