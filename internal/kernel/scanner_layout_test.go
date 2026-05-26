package kernel

import (
	"os"
	"path/filepath"
	"testing"
)

// Documented contract (from scanner.go + image.go):
//   - ScanDir is variadic; per-dir errors logged at trace and skipped;
//     aggregate error only when every supplied dir fails.
//   - DefaultPatterns matches *.efi as RoleUKI.
//   - BuildBootSets keys by (kernelName, layout). A kernel with both
//     vmlinuz-X and X.efi produces two distinct sets.
//   - UKI sets use LayoutUKI; loose kernels use LayoutSplit.
//   - Microcode is shared across all sets regardless of layout.

func TestScanDir_Variadic_AggregatesMultipleDirs(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	touch(t, dir1, "vmlinuz-linux", []byte("k"))
	touch(t, dir2, "initramfs-linux.img", []byte("i"))
	touch(t, dir2, "linux.efi", []byte{'M', 'Z'}) // tagged as UKI by glob, not yet inspected

	scanner := NewScanner("", nil)
	images, err := scanner.ScanDir(dir1, dir2)
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	if len(images) != 3 {
		t.Fatalf("got %d images, want 3 (%+v)", len(images), images)
	}
	roles := map[ImageRole]int{}
	for _, img := range images {
		roles[img.Role]++
	}
	if roles[RoleKernel] != 1 || roles[RoleInitramfs] != 1 || roles[RoleUKI] != 1 {
		t.Errorf("role distribution wrong: %v", roles)
	}
}

func TestScanDir_Variadic_SkipsMissingDirs(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "vmlinuz-linux", []byte("k"))

	scanner := NewScanner("", nil)
	images, err := scanner.ScanDir(dir, "/nonexistent/path/12345")
	if err != nil {
		t.Fatalf("ScanDir should succeed when at least one dir is valid: %v", err)
	}
	if len(images) != 1 {
		t.Errorf("got %d images, want 1", len(images))
	}
}

func TestScanDir_Variadic_ErrorsOnlyWhenAllFail(t *testing.T) {
	scanner := NewScanner("", nil)
	_, err := scanner.ScanDir("/nonexistent/a", "/nonexistent/b")
	if err == nil {
		t.Fatal("expected error when every dir is unreadable")
	}
}

func TestScanDir_NoDirs(t *testing.T) {
	scanner := NewScanner("", nil)
	images, err := scanner.ScanDir()
	if err != nil || images != nil {
		t.Errorf("ScanDir() = %v, %v; want nil, nil", images, err)
	}
}

func TestScanDir_RecognisesUKIFiles(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "linux-cachyos.efi", []byte{'M', 'Z'})

	scanner := NewScanner("", nil)
	images, err := scanner.ScanDir(dir)
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("got %d images, want 1", len(images))
	}
	if images[0].Role != RoleUKI {
		t.Errorf("Role = %q, want uki", images[0].Role)
	}
	if images[0].KernelName != "linux-cachyos" {
		t.Errorf("KernelName = %q, want linux-cachyos (.efi suffix stripped)", images[0].KernelName)
	}
}

func TestBuildBootSets_UKILayout(t *testing.T) {
	scanner := NewScanner("", nil)
	images := []*BootImage{
		{Filename: "linux.efi", KernelName: "linux", Role: RoleUKI},
	}
	sets := scanner.BuildBootSets(images)
	if len(sets) != 1 {
		t.Fatalf("got %d sets, want 1", len(sets))
	}
	if sets[0].Layout != LayoutUKI {
		t.Errorf("Layout = %q, want uki", sets[0].Layout)
	}
	if sets[0].UKI == nil {
		t.Error("UKI slot must be populated for LayoutUKI sets")
	}
	if sets[0].Kernel != nil {
		t.Error("Kernel slot must be nil for LayoutUKI sets")
	}
}

func TestBuildBootSets_SameKernelNameDifferentLayoutsProduceTwoSets(t *testing.T) {
	// A system with both vmlinuz-linux and linux.efi (same kernel name)
	// must produce two distinct boot sets — one split, one uki.
	scanner := NewScanner("", nil)
	images := []*BootImage{
		{Filename: "vmlinuz-linux", KernelName: "linux", Role: RoleKernel},
		{Filename: "initramfs-linux.img", KernelName: "linux", Role: RoleInitramfs},
		{Filename: "linux.efi", KernelName: "linux", Role: RoleUKI},
	}
	sets := scanner.BuildBootSets(images)
	if len(sets) != 2 {
		t.Fatalf("got %d sets, want 2 (one per layout)", len(sets))
	}
	layouts := map[BootLayout]bool{}
	for _, s := range sets {
		layouts[s.Layout] = true
	}
	if !layouts[LayoutSplit] || !layouts[LayoutUKI] {
		t.Errorf("expected one Split + one UKI set, got %v", layouts)
	}
}

func TestBuildBootSets_MicrocodeAttachesToAllLayouts(t *testing.T) {
	scanner := NewScanner("", nil)
	images := []*BootImage{
		{Filename: "vmlinuz-linux", KernelName: "linux", Role: RoleKernel},
		{Filename: "linux.efi", KernelName: "linux", Role: RoleUKI},
		{Filename: "amd-ucode.img", KernelName: "amd-ucode.img", Role: RoleMicrocode},
	}
	sets := scanner.BuildBootSets(images)
	if len(sets) != 2 {
		t.Fatalf("got %d sets, want 2", len(sets))
	}
	for _, s := range sets {
		if len(s.Microcode) != 1 {
			t.Errorf("layout=%s: Microcode len = %d, want 1", s.Layout, len(s.Microcode))
		}
	}
}

func TestBuildBootSets_DeterministicOrder(t *testing.T) {
	scanner := NewScanner("", nil)
	images := []*BootImage{
		{Filename: "vmlinuz-zeta", KernelName: "zeta", Role: RoleKernel},
		{Filename: "vmlinuz-alpha", KernelName: "alpha", Role: RoleKernel},
		{Filename: "vmlinuz-beta", KernelName: "beta", Role: RoleKernel},
	}
	sets := scanner.BuildBootSets(images)
	if len(sets) != 3 {
		t.Fatalf("got %d sets", len(sets))
	}
	wantOrder := []string{"alpha", "beta", "zeta"}
	for i, want := range wantOrder {
		if sets[i].KernelName != want {
			t.Errorf("sets[%d].KernelName = %q, want %q", i, sets[i].KernelName, want)
		}
	}
}

func TestBootSet_PrimaryImage(t *testing.T) {
	uki := &BootImage{Filename: "x.efi", Role: RoleUKI}
	kern := &BootImage{Filename: "vmlinuz-x", Role: RoleKernel}

	splitSet := &BootSet{Layout: LayoutSplit, Kernel: kern}
	if splitSet.PrimaryImage() != kern {
		t.Errorf("PrimaryImage for split must be Kernel")
	}

	ukiSet := &BootSet{Layout: LayoutUKI, UKI: uki}
	if ukiSet.PrimaryImage() != uki {
		t.Errorf("PrimaryImage for UKI must be UKI slot")
	}

	blsSet := &BootSet{Layout: LayoutBLS, Kernel: kern}
	if blsSet.PrimaryImage() != kern {
		t.Errorf("PrimaryImage for BLS must be Kernel")
	}
}

func TestBootSet_KernelVersion_PrefersUKIForUKILayout(t *testing.T) {
	// KernelVersion should pull from the UKI's inspected metadata
	// when Layout is UKI, not from any (unset) Kernel slot.
	uki := &BootImage{
		Filename:  "x.efi",
		Role:      RoleUKI,
		Inspected: &InspectedMetadata{Format: "uki", Version: "6.19.0-uki"},
	}
	set := &BootSet{Layout: LayoutUKI, UKI: uki}
	if got := set.KernelVersion(); got != "6.19.0-uki" {
		t.Errorf("KernelVersion = %q, want 6.19.0-uki", got)
	}
}

// touch creates an empty (or content-bearing) file in dir.
func touch(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
