package bls

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Per the spec (https://uapi-group.org/specifications/specs/boot_loader_specification/),
// a Type #1 entry is a series of "key value" lines. Lines beginning with '#' are
// comments; blank lines are ignored. Some keys (initrd, options, devicetree-overlay)
// may appear multiple times.

func TestParse_StandardKeys(t *testing.T) {
	in := strings.NewReader(`title    Arch Linux
version  6.19.0-1-arch
machine-id abcdef0123456789abcdef0123456789
linux    /vmlinuz-linux
initrd   /intel-ucode.img
initrd   /initramfs-linux.img
options  root=UUID=xxx rw quiet
architecture x64
`)
	e, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if e.Title != "Arch Linux" {
		t.Errorf("Title = %q, want %q", e.Title, "Arch Linux")
	}
	if e.Version != "6.19.0-1-arch" {
		t.Errorf("Version = %q", e.Version)
	}
	if e.MachineID != "abcdef0123456789abcdef0123456789" {
		t.Errorf("MachineID = %q", e.MachineID)
	}
	if e.Linux != "/vmlinuz-linux" {
		t.Errorf("Linux = %q", e.Linux)
	}
	if e.Architecture != "x64" {
		t.Errorf("Architecture = %q", e.Architecture)
	}
	wantInitrd := []string{"/intel-ucode.img", "/initramfs-linux.img"}
	if len(e.Initrd) != len(wantInitrd) {
		t.Fatalf("Initrd len = %d, want %d (%v)", len(e.Initrd), len(wantInitrd), e.Initrd)
	}
	for i, v := range wantInitrd {
		if e.Initrd[i] != v {
			t.Errorf("Initrd[%d] = %q, want %q", i, e.Initrd[i], v)
		}
	}
	if got := e.OptionsString(); got != "root=UUID=xxx rw quiet" {
		t.Errorf("OptionsString = %q", got)
	}
}

func TestParse_MultipleOptionsLines(t *testing.T) {
	// The spec allows splitting options across multiple lines for readability;
	// OptionsString must join them with a single space.
	in := strings.NewReader(`title  Linux
linux  /vmlinuz
options root=UUID=x rw
options quiet loglevel=3
`)
	e, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := e.OptionsString(); got != "root=UUID=x rw quiet loglevel=3" {
		t.Errorf("OptionsString = %q", got)
	}
}

func TestParse_CommentsAndBlankLines(t *testing.T) {
	in := strings.NewReader(`# top comment
title  Linux

# inline comment
linux  /vmlinuz
   # indented comment
initrd /initramfs.img
`)
	e, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if e.Title != "Linux" || e.Linux != "/vmlinuz" || len(e.Initrd) != 1 {
		t.Errorf("entry not parsed correctly across comments: %+v", e)
	}
}

func TestParse_UnknownKeysGoToExtra(t *testing.T) {
	in := strings.NewReader(`title Linux
custom-vendor-key  some-value
`)
	e, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, ok := e.Extra["custom-vendor-key"]; !ok {
		t.Errorf("custom-vendor-key not in Extra: %+v", e.Extra)
	} else if got != "some-value" {
		t.Errorf("Extra[custom-vendor-key] = %q", got)
	}
}

func TestParse_MalformedLinesIgnored(t *testing.T) {
	// A line with no value is malformed; the parser should skip it without erroring.
	in := strings.NewReader(`title Linux
keyonly
linux /vmlinuz
`)
	e, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if e.Title != "Linux" || e.Linux != "/vmlinuz" {
		t.Errorf("entry not parsed correctly with malformed line: %+v", e)
	}
}

func TestParse_DevicetreeOverlayAccumulates(t *testing.T) {
	in := strings.NewReader(`title Linux
devicetree-overlay /overlays/a.dtbo
devicetree-overlay /overlays/b.dtbo
`)
	e, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(e.DevicetreeOverlay) != 2 {
		t.Fatalf("DevicetreeOverlay len = %d, want 2", len(e.DevicetreeOverlay))
	}
}

func TestParse_EFIChainload(t *testing.T) {
	// The "efi" key chainloads a different EFI binary; useful for UKIs
	// referenced from a BLS entry instead of an inline kernel/initrd.
	in := strings.NewReader(`title  UKI via BLS
efi    /EFI/Linux/linux-6.19.efi
`)
	e, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if e.EFI != "/EFI/Linux/linux-6.19.efi" {
		t.Errorf("EFI = %q", e.EFI)
	}
}

func TestParseFile_SetsPathAndID(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "arch-rolling.conf")
	if err := os.WriteFile(confPath, []byte("title Arch\nlinux /vmlinuz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := ParseFile(confPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if e.Path != confPath {
		t.Errorf("Path = %q, want %q", e.Path, confPath)
	}
	if e.ID != "arch-rolling" {
		t.Errorf("ID = %q, want %q", e.ID, "arch-rolling")
	}
}

func TestParseFile_Nonexistent(t *testing.T) {
	_, err := ParseFile("/nonexistent/path/12345.conf")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestScanEntriesDir_MultipleDirsAggregate(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	writeEntry := func(d, name, body string) {
		if err := os.WriteFile(filepath.Join(d, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeEntry(dir1, "arch.conf", "title Arch\nlinux /a\n")
	writeEntry(dir2, "fedora.conf", "title Fedora\nlinux /b\n")
	writeEntry(dir1, "not-an-entry.txt", "ignored")

	entries := ScanEntriesDir(dir1, dir2)
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	titles := map[string]bool{}
	for _, e := range entries {
		titles[e.Title] = true
	}
	for _, want := range []string{"Arch", "Fedora"} {
		if !titles[want] {
			t.Errorf("missing title %q in %v", want, titles)
		}
	}
}

func TestScanEntriesDir_MissingDirSkipped(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.conf"), []byte("title X\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := ScanEntriesDir(dir, "/nonexistent/path/that/does/not/exist")
	if len(entries) != 1 || entries[0].Title != "X" {
		t.Errorf("expected single X entry, got %+v", entries)
	}
}

func TestScanEntriesDir_BadEntryDoesNotBreakOthers(t *testing.T) {
	dir := t.TempDir()
	// Write a "bad" entry that's empty — parser should accept it (empty Entry is valid Go-wise)
	// and continue. A truly broken parse (e.g., unreadable file) should be skipped.
	if err := os.WriteFile(filepath.Join(dir, "good.conf"), []byte("title Good\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.conf"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := ScanEntriesDir(dir)
	// Both should parse; "bad.conf" produces an Entry with empty Title.
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
}

func TestScanEntriesDir_DeterministicOrder(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"c.conf", "a.conf", "b.conf"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("title "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entries := ScanEntriesDir(dir)
	if len(entries) != 3 {
		t.Fatalf("got %d, want 3", len(entries))
	}
	wantIDs := []string{"a", "b", "c"}
	for i, want := range wantIDs {
		if entries[i].ID != want {
			t.Errorf("entries[%d].ID = %q, want %q", i, entries[i].ID, want)
		}
	}
}
