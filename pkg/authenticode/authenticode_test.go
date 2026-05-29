package authenticode

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateFixture mints a fresh self-signed RSA-2048 cert + key per test
// invocation. Returned as PEM bytes so the tests exercise the same load
// path users hit through NewSignerFromPEM/NewSignerFromFiles.
func generateFixture(t *testing.T, cn string) (keyPEM, certPEM []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:     time.Date(2036, 1, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return
}

// loadFixturePE reads the committed single-profile UKI from pkg/uki/testdata
// and returns its bytes. Used as an unsigned PE32+ input that exercises
// the same code paths a real ukify-built UKI would.
func loadFixturePE(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "uki", "testdata", "uki-single-profile.efi"))
	require.NoError(t, err)
	return b
}

func TestNewSignerFromPEM_LoadsRSA(t *testing.T) {
	keyPEM, certPEM := generateFixture(t, "test signer")
	s, err := NewSignerFromPEM(keyPEM, certPEM)
	require.NoError(t, err)
	assert.NotNil(t, s)
}

func TestNewSignerFromPEM_RejectsMismatchedKeyCert(t *testing.T) {
	keyPEM, _ := generateFixture(t, "A")
	_, certPEM := generateFixture(t, "B")
	_, err := NewSignerFromPEM(keyPEM, certPEM)
	assert.Error(t, err, "different key + cert must not pair")
}

func TestNewSignerFromPEM_RejectsGarbage(t *testing.T) {
	_, err := NewSignerFromPEM([]byte("not pem"), []byte("not pem"))
	assert.Error(t, err)
}

func TestNewSignerFromFiles_RoundTrip(t *testing.T) {
	keyPEM, certPEM := generateFixture(t, "fixture")
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key.pem")
	certPath := filepath.Join(dir, "cert.pem")
	require.NoError(t, os.WriteFile(keyPath, keyPEM, 0o600))
	require.NoError(t, os.WriteFile(certPath, certPEM, 0o644))

	s, err := NewSignerFromFiles(keyPath, certPath)
	require.NoError(t, err)
	assert.NotNil(t, s)
}

func TestSign_ProducesVerifiableSignature(t *testing.T) {
	keyPEM, certPEM := generateFixture(t, "round-trip")
	s, err := NewSignerFromPEM(keyPEM, certPEM)
	require.NoError(t, err)

	signed, err := s.Sign(loadFixturePE(t))
	require.NoError(t, err)

	cert, err := s.Certificate()
	require.NoError(t, err)

	require.NoError(t, Verify(signed, []*x509.Certificate{cert}),
		"signed output must verify against the signer's own cert")
}

func TestSign_RejectsNonPE(t *testing.T) {
	keyPEM, certPEM := generateFixture(t, "x")
	s, _ := NewSignerFromPEM(keyPEM, certPEM)
	_, err := s.Sign([]byte("definitely not a PE binary"))
	assert.Error(t, err)
}

func TestVerify_RejectsWrongCert(t *testing.T) {
	signerKey, signerCert := generateFixture(t, "signer")
	s, _ := NewSignerFromPEM(signerKey, signerCert)
	signed, err := s.Sign(loadFixturePE(t))
	require.NoError(t, err)

	_, foreignCertPEM := generateFixture(t, "foreign")
	block, _ := pem.Decode(foreignCertPEM)
	foreign, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	err = Verify(signed, []*x509.Certificate{foreign})
	assert.Error(t, err, "verify must reject signatures from a different cert")
}

func TestInspect_ReportsUnsigned(t *testing.T) {
	info, err := Inspect(loadFixturePE(t))
	require.NoError(t, err)
	assert.False(t, info.Signed, "fixture UKI is unsigned")
	assert.Empty(t, info.SignerCN)
}

func TestInspect_ReportsSigned(t *testing.T) {
	keyPEM, certPEM := generateFixture(t, "Inspect signer")
	s, _ := NewSignerFromPEM(keyPEM, certPEM)
	signed, err := s.Sign(loadFixturePE(t))
	require.NoError(t, err)

	info, err := Inspect(signed)
	require.NoError(t, err)
	assert.True(t, info.Signed)
	assert.Equal(t, "Inspect signer", info.SignerCN)
	assert.NotEmpty(t, info.DigestHex)
}

func TestInspect_RejectsNonPE(t *testing.T) {
	_, err := Inspect([]byte("not a PE"))
	assert.Error(t, err)
}

func TestSign_DoesNotMutateInput(t *testing.T) {
	keyPEM, certPEM := generateFixture(t, "non-mutating")
	s, _ := NewSignerFromPEM(keyPEM, certPEM)
	input := loadFixturePE(t)
	checksum := make([]byte, len(input))
	copy(checksum, input)

	_, err := s.Sign(input)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(input, checksum), "Sign must not mutate the input slice")
}

func TestNewSignerFromPEM_AcceptsPKCS8Key(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "pkcs8"},
		NotBefore:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:     time.Date(2036, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	_, err = NewSignerFromPEM(keyPEM, certPEM)
	assert.NoError(t, err, "PKCS#8 wrapped RSA key must load")
}

func TestNewSignerFromPEM_RejectsEncryptedKey(t *testing.T) {
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:    "RSA PRIVATE KEY",
		Headers: map[string]string{"DEK-Info": "AES-256-CBC,..."},
		Bytes:   []byte("opaque ciphertext"),
	})
	_, certPEM := generateFixture(t, "x")
	_, err := NewSignerFromPEM(keyPEM, certPEM)
	assert.Error(t, err, "DEK-Info-marked PEM blocks must be rejected")
}

func TestVerify_RejectsUnsigned(t *testing.T) {
	keyPEM, certPEM := generateFixture(t, "any")
	s, _ := NewSignerFromPEM(keyPEM, certPEM)
	cert, _ := s.Certificate()

	err := Verify(loadFixturePE(t), []*x509.Certificate{cert})
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrNoValidSignature),
		"unsigned input must produce ErrNoValidSignature, got: %v", err)
}
