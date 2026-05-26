package kernel

import "fmt"

// Inspect identifies a boot image's actual format by attempting parsers in
// order, returning the first that succeeds. The hint role only reorders
// the attempts (fast path) — every parser still verifies independently, so
// a wrong hint never produces a false positive.
//
// Returns (nil, nil) for roles that have no inspection support (microcode).
func Inspect(path string, hint ImageRole) (*InspectedMetadata, error) {
	type parser struct {
		fn func(string) (*InspectedMetadata, error)
	}
	uki := parser{InspectUKI}
	kern := parser{InspectKernel}
	initrd := parser{InspectInitramfs}
	microcode := parser{InspectMicrocode}

	var ordered []parser
	switch hint {
	case RoleUKI:
		ordered = []parser{uki, kern, initrd, microcode}
	case RoleKernel:
		ordered = []parser{kern, uki, initrd, microcode}
	case RoleInitramfs, RoleFallbackInitramfs:
		ordered = []parser{initrd, microcode, kern, uki}
	case RoleMicrocode:
		// Microcode is the only case where the role label is filename-anchored
		// (we only get here for exact matches like intel-ucode.img / amd-ucode.img).
		// Don't fall through to other parsers — the initramfs sniffer is too
		// permissive and would mislabel a malformed microcode file as "unknown
		// initramfs". On failure the caller keeps the filename-derived role
		// with Inspected = nil.
		ordered = []parser{microcode}
	default:
		ordered = []parser{uki, kern, initrd, microcode}
	}

	var firstErr error
	for _, p := range ordered {
		meta, err := p.fn(path)
		if err == nil && meta != nil {
			return meta, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}

	if firstErr != nil {
		return nil, fmt.Errorf("could not identify boot image format: %w", firstErr)
	}
	return nil, fmt.Errorf("could not identify boot image format")
}
