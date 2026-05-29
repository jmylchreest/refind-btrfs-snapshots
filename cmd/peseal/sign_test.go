package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmylchreest/refind-btrfs-snapshots/pkg/authenticode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFixtureKeyCert mints a fresh self-signed RSA-2048 cert + key into
// dir/{key.pem,cert.pem} and returns the paths. Mirrors the fixture
// helper in pkg/authenticode tests but writes to disk so the cmd-layer
// loader path is exercised.
func writeFixtureKeyCert(t *testing.T, dir, cn string) (keyPath, certPath string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:     time.Date(2036, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	// CN-suffixed names so two calls with the same dir don't clobber each other.
	keyPath = filepath.Join(dir, cn+".key.pem")
	certPath = filepath.Join(dir, cn+".cert.pem")
	require.NoError(t, os.WriteFile(keyPath,
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
		0o600))
	require.NoError(t, os.WriteFile(certPath,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		0o644))
	return
}

func copyFixturePE(t *testing.T, dst string) {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", "pkg", "uki", "testdata", "uki-single-profile.efi"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(dst, src, 0o644))
}

func TestSignFiles_WritesValidSignature(t *testing.T) {
	dir := t.TempDir()
	keyPath, certPath := writeFixtureKeyCert(t, dir, "round-trip")
	target := filepath.Join(dir, "uki.efi")
	copyFixturePE(t, target)

	res, err := signFiles(signOptions{
		KeyPath:           keyPath,
		CertPath:          certPath,
		SkipAlreadySigned: true,
	}, []string{target})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Signed)
	assert.Equal(t, 0, res.Skipped)
	assert.Equal(t, 0, res.Failed)

	// Confirm the file on disk now verifies against the same cert.
	signed, err := os.ReadFile(target)
	require.NoError(t, err)
	certPEM, _ := os.ReadFile(certPath)
	block, _ := pem.Decode(certPEM)
	cert, _ := x509.ParseCertificate(block.Bytes)
	assert.NoError(t, authenticode.Verify(signed, []*x509.Certificate{cert}))
}

func TestSignFiles_IdempotentSkipsAlreadySigned(t *testing.T) {
	dir := t.TempDir()
	keyPath, certPath := writeFixtureKeyCert(t, dir, "idempotent")
	target := filepath.Join(dir, "uki.efi")
	copyFixturePE(t, target)
	opts := signOptions{KeyPath: keyPath, CertPath: certPath, SkipAlreadySigned: true}

	// First call signs.
	res, err := signFiles(opts, []string{target})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Signed)

	firstSize, err := os.Stat(target)
	require.NoError(t, err)

	// Second call sees the existing signature and skips.
	res, err = signFiles(opts, []string{target})
	require.NoError(t, err)
	assert.Equal(t, 0, res.Signed, "already-signed file must not be re-signed")
	assert.Equal(t, 1, res.Skipped)

	secondSize, err := os.Stat(target)
	require.NoError(t, err)
	assert.Equal(t, firstSize.Size(), secondSize.Size(), "skipped file size must not change")
}

func TestSignFiles_NoSkipForcesResign(t *testing.T) {
	dir := t.TempDir()
	keyPath, certPath := writeFixtureKeyCert(t, dir, "force")
	target := filepath.Join(dir, "uki.efi")
	copyFixturePE(t, target)
	opts := signOptions{KeyPath: keyPath, CertPath: certPath, SkipAlreadySigned: false}

	res, err := signFiles(opts, []string{target})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Signed)

	// With skip disabled, re-signing must proceed (file grows by a second sig).
	res, err = signFiles(opts, []string{target})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Signed)
	assert.Equal(t, 0, res.Skipped)
}

func TestSignFiles_AggregatesPerFileErrors(t *testing.T) {
	dir := t.TempDir()
	keyPath, certPath := writeFixtureKeyCert(t, dir, "agg")
	good := filepath.Join(dir, "good.efi")
	bad := filepath.Join(dir, "bad.efi")
	copyFixturePE(t, good)
	require.NoError(t, os.WriteFile(bad, []byte("not a PE"), 0o644))

	res, err := signFiles(signOptions{
		KeyPath:           keyPath,
		CertPath:          certPath,
		SkipAlreadySigned: true,
	}, []string{good, bad})
	require.Error(t, err, "any per-file failure must surface as a non-nil error")
	assert.Equal(t, 1, res.Signed, "the good file must still get signed")
	assert.Equal(t, 1, res.Failed)
}

func TestSignFiles_DryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	keyPath, certPath := writeFixtureKeyCert(t, dir, "dry")
	target := filepath.Join(dir, "uki.efi")
	copyFixturePE(t, target)
	original, _ := os.ReadFile(target)

	res, err := signFiles(signOptions{
		KeyPath:           keyPath,
		CertPath:          certPath,
		DryRun:            true,
		SkipAlreadySigned: true,
	}, []string{target})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Planned, "dry-run reports what would be signed")
	assert.Equal(t, 0, res.Signed)

	after, _ := os.ReadFile(target)
	assert.Equal(t, original, after, "dry-run must not mutate target")
}

func TestVerifyFiles_AcceptsRightCertRejectsWrong(t *testing.T) {
	dir := t.TempDir()
	signKey, signCert := writeFixtureKeyCert(t, dir, "signer")
	target := filepath.Join(dir, "uki.efi")
	copyFixturePE(t, target)

	_, err := signFiles(signOptions{
		KeyPath:           signKey,
		CertPath:          signCert,
		SkipAlreadySigned: true,
	}, []string{target})
	require.NoError(t, err)

	_, foreignCert := writeFixtureKeyCert(t, dir, "foreign")

	good := verifyFiles(verifyOptions{CertPath: signCert}, []string{target})
	assert.Equal(t, 1, good.Verified)
	assert.Equal(t, 0, good.Failed)

	bad := verifyFiles(verifyOptions{CertPath: foreignCert}, []string{target})
	assert.Equal(t, 0, bad.Verified)
	assert.Equal(t, 1, bad.Failed)
}

func TestExpandPaths_GlobsAndDeduplicates(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.efi", "b.efi", "c.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644))
	}
	paths := expandPaths([]string{
		filepath.Join(dir, "*.efi"),
		filepath.Join(dir, "a.efi"), // explicit listing of one of the glob hits — must dedupe
		filepath.Join(dir, "doesnt-match-*.bin"),
	})
	assert.ElementsMatch(t, []string{
		filepath.Join(dir, "a.efi"),
		filepath.Join(dir, "b.efi"),
	}, paths)
}
