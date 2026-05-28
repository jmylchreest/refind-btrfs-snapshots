package uki

import (
	"bytes"
	"fmt"

	"github.com/jmylchreest/refind-btrfs-snapshots/pkg/uki"
)

// cmdlineFromBytes reads the .cmdline of the UKI in srcBytes. Used by
// the cloner to grab the base cmdline that gets rewritten per snapshot.
func cmdlineFromBytes(srcBytes []byte) (string, error) {
	img, err := uki.Parse(bytes.NewReader(srcBytes))
	if err != nil {
		return "", err
	}
	return img.Cmdline(), nil
}

// CloneWithCmdline parses srcBytes as a UKI, rewrites the .cmdline section
// to newCmdline, and returns the re-emitted PE bytes. All other sections
// are preserved verbatim — only .cmdline changes. The result is a valid
// UKI that boots the same kernel/initrd with a different cmdline (and is
// no longer Authenticode-signed; the signature is invalidated by any byte
// mutation, which is expected at this stage — Secure Boot resigning is
// deferred to a later slice).
func CloneWithCmdline(srcBytes []byte, newCmdline string) ([]byte, error) {
	img, err := uki.Parse(bytes.NewReader(srcBytes))
	if err != nil {
		return nil, fmt.Errorf("parse source UKI: %w", err)
	}
	img.SetCmdline(newCmdline)

	var buf bytes.Buffer
	if _, err := img.WriteTo(&buf); err != nil {
		return nil, fmt.Errorf("write cloned UKI: %w", err)
	}
	return buf.Bytes(), nil
}
