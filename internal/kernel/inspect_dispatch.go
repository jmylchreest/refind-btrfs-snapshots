package kernel

import "fmt"

// Inspect tries parsers in hint-biased order and returns the first that
// succeeds. Each parser still verifies independently, so a wrong hint
// never produces a false positive.
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
		// No fall-through: the initramfs sniffer is permissive and would
		// mislabel a malformed microcode file as "unknown initramfs".
		// Caller keeps the filename-derived role with Inspected = nil.
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
