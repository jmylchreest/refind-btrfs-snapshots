package uki

import (
	"bytes"
	"path/filepath"
	"testing"
)

// --- Cmdline / SetCmdline ---------------------------------------------------

func TestCmdline_ReturnsNullTrimmedContent(t *testing.T) {
	pe := buildPE(t, []peSection{
		{name: ".linux", data: []byte("k")},
		{name: ".cmdline", data: []byte("root=UUID=x rw\x00")},
	})
	img, err := Parse(bytes.NewReader(pe))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := img.Cmdline(); got != "root=UUID=x rw" {
		t.Errorf("Cmdline() = %q, want %q", got, "root=UUID=x rw")
	}
}

func TestCmdline_EmptyWhenMissing(t *testing.T) {
	pe := buildPE(t, []peSection{{name: ".linux", data: []byte("k")}})
	img, err := Parse(bytes.NewReader(pe))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := img.Cmdline(); got != "" {
		t.Errorf("Cmdline() = %q, want empty", got)
	}
}

func TestSetCmdline_StoresNullTerminated(t *testing.T) {
	// UAPI convention: string sections (.cmdline, .uname, .osrel) carry a
	// trailing NUL terminator on disk. SetCmdline must add it so the
	// written UKI matches what ukify/systemd-stub produce.
	pe := buildPE(t, []peSection{
		{name: ".linux", data: []byte("k")},
		{name: ".cmdline", data: []byte("old\x00")},
	})
	img, err := Parse(bytes.NewReader(pe))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	img.SetCmdline("root=UUID=y rw rootflags=subvol=/snap")

	got := img.Section(".cmdline")
	if got == nil {
		t.Fatal(".cmdline missing after SetCmdline")
	}
	if len(got.Data) == 0 || got.Data[len(got.Data)-1] != 0 {
		t.Errorf(".cmdline not null-terminated: %q", got.Data)
	}
	if string(got.Data[:len(got.Data)-1]) != "root=UUID=y rw rootflags=subvol=/snap" {
		t.Errorf(".cmdline content = %q", got.Data)
	}
}

func TestSetCmdline_RoundTripReadsBack(t *testing.T) {
	pe := buildPE(t, []peSection{
		{name: ".linux", data: []byte("k")},
		{name: ".cmdline", data: []byte("old\x00")},
	})
	img, err := Parse(bytes.NewReader(pe))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := "root=UUID=z rw"
	img.SetCmdline(want)

	var buf bytes.Buffer
	if _, err := img.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	rt, err := Parse(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if got := rt.Cmdline(); got != want {
		t.Errorf("round-trip Cmdline() = %q, want %q", got, want)
	}
}

func TestSetCmdline_AppendsWhenAbsent(t *testing.T) {
	pe := buildPE(t, []peSection{{name: ".linux", data: []byte("k")}})
	img, err := Parse(bytes.NewReader(pe))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	img.SetCmdline("root=UUID=x rw")
	if img.Section(".cmdline") == nil {
		t.Fatal(".cmdline absent after SetCmdline")
	}
	if got := img.Cmdline(); got != "root=UUID=x rw" {
		t.Errorf("Cmdline() = %q", got)
	}
}

// --- Uname ------------------------------------------------------------------

func TestUname_TrimsNullAndWhitespace(t *testing.T) {
	pe := buildPE(t, []peSection{
		{name: ".linux", data: []byte("k")},
		{name: ".uname", data: []byte("6.19.0-1-arch\n\x00")},
	})
	img, err := Parse(bytes.NewReader(pe))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := img.Uname(); got != "6.19.0-1-arch" {
		t.Errorf("Uname() = %q", got)
	}
}

// --- OSRelease --------------------------------------------------------------

func TestOSRelease_ParsesKeyValueAndStripsQuotes(t *testing.T) {
	osrel := "ID=arch\nNAME=\"Arch Linux\"\nPRETTY_NAME='Arch Linux (stable)'\nno-equals\n\x00"
	pe := buildPE(t, []peSection{
		{name: ".linux", data: []byte("k")},
		{name: ".osrel", data: []byte(osrel)},
	})
	img, err := Parse(bytes.NewReader(pe))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := img.OSRelease()
	want := map[string]string{
		"ID":          "arch",
		"NAME":        "Arch Linux",
		"PRETTY_NAME": "Arch Linux (stable)",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("OSRelease[%q] = %q, want %q", k, got[k], v)
		}
	}
	if _, ok := got["no-equals"]; ok {
		t.Errorf("malformed line should not appear in result")
	}
}

func TestOSRelease_EmptyWhenMissing(t *testing.T) {
	pe := buildPE(t, []peSection{{name: ".linux", data: []byte("k")}})
	img, err := Parse(bytes.NewReader(pe))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := img.OSRelease(); len(got) != 0 {
		t.Errorf("OSRelease() = %v, want empty map", got)
	}
}

// --- Profiles (multi-profile awareness) -------------------------------------

func TestProfiles_EmptyForSingleProfile(t *testing.T) {
	pe := buildPE(t, []peSection{
		{name: ".linux", data: []byte("k")},
		{name: ".cmdline", data: []byte("root=UUID=x rw\x00")},
	})
	img, err := Parse(bytes.NewReader(pe))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := img.Profiles(); len(got) != 0 {
		t.Errorf("Profiles() = %d entries, want 0 for single-profile", len(got))
	}
}

func TestProfiles_ExtractsBaseAndPerProfileCmdlines(t *testing.T) {
	pe := buildPE(t, []peSection{
		{name: ".linux", data: []byte("k")},
		{name: ".cmdline", data: []byte("root=UUID=x rw\x00")}, // base
		{name: ".profile", data: []byte("ID=snap-100\nTITLE=Snapshot 100\n\x00")},
		{name: ".cmdline", data: []byte("root=UUID=x rw rootflags=subvol=/snap-100\x00")},
		{name: ".profile", data: []byte("ID=snap-200\nTITLE=Snapshot 200\n\x00")},
		{name: ".cmdline", data: []byte("root=UUID=x rw rootflags=subvol=/snap-200\x00")},
	})
	img, err := Parse(bytes.NewReader(pe))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	profiles := img.Profiles()
	if len(profiles) != 2 {
		t.Fatalf("Profiles count = %d, want 2", len(profiles))
	}
	want := []Profile{
		{Index: 0, ID: "snap-100", Title: "Snapshot 100", Cmdline: "root=UUID=x rw rootflags=subvol=/snap-100"},
		{Index: 1, ID: "snap-200", Title: "Snapshot 200", Cmdline: "root=UUID=x rw rootflags=subvol=/snap-200"},
	}
	for i, w := range want {
		if profiles[i] != w {
			t.Errorf("Profiles[%d] = %+v, want %+v", i, profiles[i], w)
		}
	}
}

func TestProfiles_InheritsBaseCmdlineWhenNoOverride(t *testing.T) {
	pe := buildPE(t, []peSection{
		{name: ".linux", data: []byte("k")},
		{name: ".cmdline", data: []byte("root=UUID=x rw base\x00")},
		{name: ".profile", data: []byte("ID=a\nTITLE=A\n\x00")},
		{name: ".cmdline", data: []byte("root=UUID=x rw override-A\x00")},
		{name: ".profile", data: []byte("ID=b\nTITLE=B\n\x00")},
		// no .cmdline for profile b → inherits base
	})
	img, err := Parse(bytes.NewReader(pe))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	profiles := img.Profiles()
	if len(profiles) != 2 {
		t.Fatalf("len = %d", len(profiles))
	}
	if profiles[0].Cmdline != "root=UUID=x rw override-A" {
		t.Errorf("Profiles[0].Cmdline = %q", profiles[0].Cmdline)
	}
	if profiles[1].Cmdline != "root=UUID=x rw base" {
		t.Errorf("Profiles[1].Cmdline = %q (expected base inheritance)", profiles[1].Cmdline)
	}
}

func TestProfiles_NoBaseCmdline(t *testing.T) {
	pe := buildPE(t, []peSection{
		{name: ".linux", data: []byte("k")},
		{name: ".profile", data: []byte("ID=only\nTITLE=Only\n\x00")},
		{name: ".cmdline", data: []byte("root=UUID=x rw only\x00")},
	})
	img, err := Parse(bytes.NewReader(pe))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := img.Cmdline(); got != "" {
		t.Errorf("Cmdline() = %q, want empty (no base supplied)", got)
	}
	profiles := img.Profiles()
	if len(profiles) != 1 || profiles[0].Cmdline != "root=UUID=x rw only" {
		t.Errorf("Profiles = %+v", profiles)
	}
}

// --- Real fixtures ----------------------------------------------------------

func TestCmdline_RealSingleProfileFixture(t *testing.T) {
	img, err := ParseFile(filepath.Join("testdata", "uki-single-profile.efi"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if got, want := img.Cmdline(), "root=UUID=fixture-uuid rw quiet"; got != want {
		t.Errorf("Cmdline() = %q, want %q", got, want)
	}
	if got, want := img.Uname(), "6.19.0-test"; got != want {
		t.Errorf("Uname() = %q, want %q", got, want)
	}
	if got := img.OSRelease()["ID"]; got != "test" {
		t.Errorf("OSRelease[ID] = %q, want test", got)
	}
}

func TestProfiles_RealMultiProfileFixture(t *testing.T) {
	img, err := ParseFile(filepath.Join("testdata", "uki-multi-profile.efi"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	profiles := img.Profiles()
	// ukify auto-emits a "main" profile @0 for the base cmdline, then the
	// two we supplied — matching the legacy inspect_uki_test fixture
	// expectations.
	want := []Profile{
		{Index: 0, ID: "main", Cmdline: "root=UUID=fixture-uuid rw"},
		{Index: 1, ID: "snapshot-100", Title: "Snapshot 100", Cmdline: "root=UUID=fixture-uuid rw rootflags=subvol=/snap-100,subvolid=100"},
		{Index: 2, ID: "snapshot-200", Title: "Snapshot 200", Cmdline: "root=UUID=fixture-uuid rw rootflags=subvol=/snap-200,subvolid=200"},
	}
	if len(profiles) != len(want) {
		t.Fatalf("Profiles count = %d, want %d", len(profiles), len(want))
	}
	for i, w := range want {
		if profiles[i] != w {
			t.Errorf("Profiles[%d] = %+v\nwant %+v", i, profiles[i], w)
		}
	}
}
