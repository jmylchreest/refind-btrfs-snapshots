// Package authenticode signs and verifies PE32+ binaries using the
// Microsoft Authenticode format. It is a thin wrapper around
// github.com/foxboron/go-uefi/authenticode, providing a small,
// stable API focused on the use cases this repo cares about:
// signing Unified Kernel Images and other EFI executables for
// Secure Boot.
//
// Authenticode embeds a PKCS#7/CMS detached signature in a PE
// binary's certificate table (referenced from the OptionalHeader
// Security data directory). The signature covers a SHA hash of the
// file with three regions excluded: the OptionalHeader CheckSum
// field, the Security data directory entry, and the certificate
// table itself. Spec reference:
// https://learn.microsoft.com/en-us/windows/win32/debug/pe-format
//
// This package does not implement PCR signing for TPM-sealed boots
// or hardware-backed (PKCS#11 / TPM-resident) signing keys. Both
// are out of scope.
package authenticode
