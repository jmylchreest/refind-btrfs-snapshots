package btrfs

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog/log"
)

// applySnapperMetadata enriches a snapshot with metadata from snapper's info.xml if available.
func (m *Manager) applySnapperMetadata(snapshot *Snapshot, entryPath string) {
	snapperInfo, err := m.parseSnapperInfo(entryPath)
	if err != nil {
		log.Debug().Err(err).Str("path", entryPath).Msg("No snapper info.xml found, using file timestamp")
		return
	}
	if snapperTime, err := m.getSnapperTimestamp(snapperInfo.Date); err == nil {
		snapshot.SnapshotTime = snapperTime
	}
	snapshot.Description = snapperInfo.Description
	snapshot.SnapperNum = snapperInfo.Num
	snapshot.SnapperType = snapperInfo.Type

	log.Debug().
		Str("path", snapshot.FilesystemPath).
		Str("description", snapshot.Description).
		Int("snapper_num", snapshot.SnapperNum).
		Time("snapper_time", snapshot.SnapshotTime).
		Msg("Found snapper metadata")
}

// parseSnapperInfo reads and parses snapper info.xml file
func (m *Manager) parseSnapperInfo(snapshotDir string) (*SnapperInfo, error) {
	infoPath := filepath.Join(snapshotDir, "info.xml")
	data, err := os.ReadFile(infoPath)
	if err != nil {
		return nil, err
	}

	var info SnapperInfo
	if err := xml.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("failed to parse info.xml: %w", err)
	}

	return &info, nil
}

// getSnapperTimestamp parses snapper date format and returns time.Time.
// Times in info.xml are assumed to be in UTC when no zone is present.
func (m *Manager) getSnapperTimestamp(dateStr string) (time.Time, error) {
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04:05 -0700",
		time.RFC3339,
	}

	for _, layout := range layouts {
		if t, err := time.Parse(layout, dateStr); err == nil {
			if layout == "2006-01-02 15:04:05" {
				return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC), nil
			}
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unable to parse snapper date: %s", dateStr)
}
