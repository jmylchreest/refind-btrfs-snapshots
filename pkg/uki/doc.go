// Package uki — references for spec-correctness tracking.
//
// There is no other Go-native UKI library at the time of writing
// (surveyed 2026); debug/pe and github.com/saferwall/pe both stop at
// read-only PE parsing and neither carries UKI semantics. The canonical
// references for "what should a UKI builder/reader do" all live outside
// the Go ecosystem:
//
//   - UAPI spec — the authoritative format definition:
//     https://uapi-group.org/specifications/specs/unified_kernel_image/
//     Source: https://github.com/uapi-group/specifications/blob/main/specs/unified_kernel_image.md
//
//   - systemd-ukify — the canonical builder (Python on top of pefile).
//     Use this to answer "what does ukify do for edge case X?":
//     https://github.com/systemd/systemd/blob/main/src/ukify/ukify.py
//
//   - systemd-stub — the C-side reader; the EFI binary that parses its
//     own UKI sections at boot. Use this to answer "what does the
//     firmware/stub actually see?":
//     https://github.com/systemd/systemd/tree/main/src/boot/efi
//     (look at stub.c / stub-pe-section.c).
//
// When extending this package, prefer matching ukify/stub behaviour over
// reinventing it. The spec leaves room (e.g., section ordering, profile
// inheritance) and the canonical implementations are the de facto truth.
package uki
