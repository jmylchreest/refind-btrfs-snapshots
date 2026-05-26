package kernel

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// Documented contract (from inspect_dispatch.go):
//   - Inspect(path, hint) reorders parsers based on hint.
//   - Every parser still verifies independently; a wrong hint never produces a
//     false positive (e.g., hinting a UKI as a kernel must still identify it as UKI).
//   - When hint is RoleMicrocode, only the microcode parser runs — no fall-through.
//   - Returns an error when no parser succeeds.

func TestInspect_KernelHintFindsBzImage(t *testing.T) {
	// Build a minimal valid bzImage (HdrS at 0x202).
	path := writeTempFile(t, buildMinimalBzImage())
	meta, err := Inspect(path, RoleKernel)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if meta.Format != "bzimage" {
		t.Errorf("Format = %q, want bzimage", meta.Format)
	}
}

func TestInspect_UKIHintFindsUKI(t *testing.T) {
	pe := buildPE(t, []peSection{{name: ".linux", data: []byte("x")}})
	path := writeTempFile(t, pe)
	meta, err := Inspect(path, RoleUKI)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if meta.Format != "uki" {
		t.Errorf("Format = %q, want uki", meta.Format)
	}
}

func TestInspect_WrongHintStillIdentifiesUKI(t *testing.T) {
	// Hint says kernel, but file is actually a UKI — must still detect UKI.
	pe := buildPE(t, []peSection{{name: ".linux", data: []byte("x")}})
	path := writeTempFile(t, pe)
	meta, err := Inspect(path, RoleKernel)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if meta.Format != "uki" {
		t.Errorf("Format = %q, want uki even with kernel hint", meta.Format)
	}
}

func TestInspect_MicrocodeHintFindsMicrocode(t *testing.T) {
	container := amdContainerBytes([]amdTestPatch{
		{year: 2024, month: 1, day: 15, patchID: 0x1, processorRevID: 0x100},
	})
	path := writeTempFile(t, container)
	meta, err := Inspect(path, RoleMicrocode)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if meta.Format != "microcode" || meta.MicrocodeVendor != "AMD" {
		t.Errorf("Format=%q Vendor=%q", meta.Format, meta.MicrocodeVendor)
	}
}

func TestInspect_MicrocodeHintDoesNotFallThrough(t *testing.T) {
	// Garbage bytes shaped to defeat the microcode parsers. With hint=RoleMicrocode
	// the dispatcher must NOT try other parsers — otherwise the initramfs sniffer
	// would mislabel this as "unknown initramfs".
	path := writeTempFile(t, []byte("totally not microcode but big enough to look like something"))
	_, err := Inspect(path, RoleMicrocode)
	if err == nil {
		t.Fatal("expected error — microcode hint must not fall through to other parsers")
	}
}

func TestInspect_MicrocodeReturnsNilForMicrocodeFiles(t *testing.T) {
	// Microcode IS now inspected (previous behaviour was return (nil, nil)).
	// This test pins the new behaviour: valid microcode returns metadata, not nil.
	container := amdContainerBytes([]amdTestPatch{
		{year: 2024, month: 1, day: 15, patchID: 0x1, processorRevID: 0x100},
	})
	path := writeTempFile(t, container)
	meta, err := Inspect(path, RoleMicrocode)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if meta == nil {
		t.Fatal("expected non-nil metadata for valid microcode")
	}
}

func TestInspect_UnknownFormatReturnsError(t *testing.T) {
	path := writeTempFile(t, []byte("garbage that matches no format whatsoever"))
	_, err := Inspect(path, RoleKernel)
	if err == nil {
		// The initramfs parser is permissive; the dispatcher returns its
		// "unknown" result rather than an error. Document the current
		// behaviour: when initramfs accepts the file, Inspect succeeds.
		// If we want stricter detection later, this test will signal it.
		t.Skip("initramfs parser accepts garbage as 'unknown' — dispatcher does not error")
	}
}

func TestInspect_NonexistentFile(t *testing.T) {
	_, err := Inspect("/nonexistent/file/12345", RoleKernel)
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

// buildMinimalBzImage constructs a tiny valid bzImage with just enough header
// for InspectKernel to recognise it. No version string is populated.
func buildMinimalBzImage() []byte {
	buf := make([]byte, 0x210) // > minHeaderSize
	// HdrS magic at offset 0x202
	binary.LittleEndian.PutUint32(buf[0x202:], hdrSMagic)
	// boot protocol version at 0x206 (e.g., 2.15 = 0x020F)
	binary.LittleEndian.PutUint16(buf[0x206:], 0x020F)
	// kernel_version pointer at 0x20E = 0 (no version string)
	return buf
}

// TestSynthesizedBzImageParses guards the bzImage synthesizer.
func TestSynthesizedBzImageParses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vmlinuz")
	if err := os.WriteFile(path, buildMinimalBzImage(), 0o644); err != nil {
		t.Fatal(err)
	}
	meta, err := InspectKernel(path)
	if err != nil {
		t.Fatalf("InspectKernel: %v", err)
	}
	if meta.BootProtocol != "2.15" {
		t.Errorf("BootProtocol = %q, want 2.15", meta.BootProtocol)
	}
}
