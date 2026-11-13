package btrfs

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// plannedSubvolumePath resolves a not-yet-created path relative to the Btrfs
// top level. Mount roots matter: the destination can be on a different mount
// of the same filesystem, or under a bind mount.
func plannedSubvolumePath(path string) (string, error) {
	parent := path
	var suffix string
	for {
		resolved, err := filepath.EvalSymlinks(parent)
		if err == nil {
			path = filepath.Join(resolved, suffix)
			break
		}
		if !os.IsNotExist(err) || parent == filepath.Dir(parent) {
			return "", err
		}
		suffix = filepath.Join(filepath.Base(parent), suffix)
		parent = filepath.Dir(parent)
	}
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return "", err
	}
	defer f.Close()
	return subvolumePathFromMountInfo(path, f)
}

func subvolumePathFromMountInfo(path string, mounts io.Reader) (string, error) {
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	bestMount, bestPath, bestType := "", "", ""
	scanner := bufio.NewScanner(mounts)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			continue
		}
		sep := 6
		for sep < len(fields) && fields[sep] != "-" {
			sep++
		}
		if sep+1 >= len(fields) {
			continue
		}
		mount := unescape.Replace(fields[4])
		rel, err := filepath.Rel(mount, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, "../") || len(mount) < len(bestMount) {
			continue
		}
		bestMount = mount
		bestPath = strings.TrimPrefix(filepath.Join(unescape.Replace(fields[3]), rel), "/")
		bestType = fields[sep+1]
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if bestType != "btrfs" {
		return "", fmt.Errorf("destination %s is not on a Btrfs mount", path)
	}
	return bestPath, nil
}
