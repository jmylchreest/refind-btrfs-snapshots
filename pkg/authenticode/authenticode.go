package authenticode

import (
	"bytes"
	"crypto"
	_ "crypto/sha256" // register SHA-256 with the crypto.Hash registry
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"

	goauth "github.com/foxboron/go-uefi/authenticode"
)

// ErrNoValidSignature is returned by Verify when none of the supplied roots
// validates any signature on the input.
var ErrNoValidSignature = errors.New("authenticode: no signature verifies against supplied roots")

// Signer holds an RSA private key paired with its X.509 certificate.
// Construct via NewSignerFromPEM or NewSignerFromFiles. Safe for
// concurrent use; the underlying go-uefi sign path does not mutate state.
type Signer struct {
	key  *rsa.PrivateKey
	cert *x509.Certificate
}

// NewSignerFromPEM loads a PEM-encoded RSA private key and an X.509
// certificate. The two must match (the certificate's public key must
// derive from the private key); a mismatch returns an error so misconfigured
// signing setups fail at construction rather than at firmware-rejection
// time. Passphrase-encrypted keys are rejected — out of scope for v1.
func NewSignerFromPEM(keyPEM, certPEM []byte) (*Signer, error) {
	key, err := parsePrivateKey(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("authenticode: load key: %w", err)
	}
	cert, err := parseCertificate(certPEM)
	if err != nil {
		return nil, fmt.Errorf("authenticode: load cert: %w", err)
	}
	if !publicKeyMatches(key, cert) {
		return nil, errors.New("authenticode: key does not match certificate public key")
	}
	return &Signer{key: key, cert: cert}, nil
}

// NewSignerFromFiles is a convenience wrapper that reads keyPath and
// certPath and forwards to NewSignerFromPEM.
func NewSignerFromFiles(keyPath, certPath string) (*Signer, error) {
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("authenticode: read key %s: %w", keyPath, err)
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("authenticode: read cert %s: %w", certPath, err)
	}
	return NewSignerFromPEM(keyPEM, certPEM)
}

// Certificate returns the X.509 certificate paired with the signer's key.
// Useful when the same cert needs to flow into Verify alongside Sign.
func (s *Signer) Certificate() (*x509.Certificate, error) {
	if s == nil || s.cert == nil {
		return nil, errors.New("authenticode: signer not initialised")
	}
	return s.cert, nil
}

// Sign returns a signed copy of unsigned. The input slice is not modified.
// If unsigned already carries signatures, the new signature is appended;
// callers wanting exactly-one-signature semantics should idempotency-check
// via Inspect first or strip the existing signatures upstream.
func (s *Signer) Sign(unsigned []byte) ([]byte, error) {
	buf := make([]byte, len(unsigned))
	copy(buf, unsigned)
	bin, err := goauth.Parse(bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("authenticode: parse PE: %w", err)
	}
	sig, err := bin.Sign(s.key, s.cert)
	if err != nil {
		return nil, fmt.Errorf("authenticode: sign: %w", err)
	}
	if err := bin.AppendSignature(sig); err != nil {
		return nil, fmt.Errorf("authenticode: attach signature: %w", err)
	}
	return bin.Bytes(), nil
}

// Verify returns nil if pe carries at least one signature that validates
// against any of the supplied roots. Roots act as a trust set; a signature
// matches if its embedded cert chains to (or equals) any root. Pass a
// single root to mimic the common "did this specific cert sign it?" check.
// Returns ErrNoValidSignature when no root accepts; other errors propagate
// from PE parsing.
func Verify(pe []byte, roots []*x509.Certificate) error {
	bin, err := goauth.Parse(bytes.NewReader(pe))
	if err != nil {
		return fmt.Errorf("authenticode: parse PE: %w", err)
	}
	sigs, err := bin.Signatures()
	if err != nil {
		return fmt.Errorf("authenticode: read signatures: %w", err)
	}
	if len(sigs) == 0 {
		return ErrNoValidSignature
	}
	for _, root := range roots {
		ok, err := bin.Verify(root)
		if err == nil && ok {
			return nil
		}
	}
	return ErrNoValidSignature
}

// SignatureInfo describes the Authenticode state of a PE binary.
type SignatureInfo struct {
	Signed       bool   // at least one WIN_CERTIFICATE entry present
	SignerCN     string // CommonName of the first signer's leaf cert (empty if unsigned)
	SignatureAlg string // e.g. "SHA256-RSA"
	DigestAlg    string // e.g. "SHA-256"
	DigestHex    string // hex of the PE Authenticode hash (always populated)
}

// Inspect reports on the input's PE Authenticode state without performing
// chain validation. Use Verify for trust decisions; Inspect is for
// human-readable diagnostics.
func Inspect(pe []byte) (*SignatureInfo, error) {
	bin, err := goauth.Parse(bytes.NewReader(pe))
	if err != nil {
		return nil, fmt.Errorf("authenticode: parse PE: %w", err)
	}
	info := &SignatureInfo{
		DigestAlg: "SHA-256",
		DigestHex: hex.EncodeToString(bin.Hash(crypto.SHA256)),
	}
	sigs, err := bin.Signatures()
	if err != nil {
		return nil, fmt.Errorf("authenticode: read signatures: %w", err)
	}
	if len(sigs) == 0 {
		return info, nil
	}
	info.Signed = true

	auth, err := goauth.ParseAuthenticode(sigs[0].Certificate)
	if err != nil {
		return info, nil
	}
	if certs := auth.Pkcs.Certs; len(certs) > 0 {
		info.SignerCN = certs[0].Subject.CommonName
		info.SignatureAlg = certs[0].SignatureAlgorithm.String()
	}
	return info, nil
}

func parsePrivateKey(keyPEM []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	// x509.IsEncryptedPEMBlock is deprecated and unreliable on modern PKCS#8
	// keys; passphrase support is out of scope for v1, so we treat any
	// parse failure on encrypted-looking material as a clean rejection.
	if isEncryptedPEM(block) {
		return nil, errors.New("encrypted keys not supported")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		rsaKey, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("key type %T is not RSA", k)
		}
		return rsaKey, nil
	default:
		return nil, fmt.Errorf("unsupported PEM type %q", block.Type)
	}
}

func parseCertificate(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return x509.ParseCertificate(certPEM) // try raw DER
	}
	return x509.ParseCertificate(block.Bytes)
}

func publicKeyMatches(key *rsa.PrivateKey, cert *x509.Certificate) bool {
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return false
	}
	return pub.N.Cmp(key.N) == 0 && pub.E == key.E
}

func isEncryptedPEM(block *pem.Block) bool {
	if _, ok := block.Headers["DEK-Info"]; ok {
		return true
	}
	return block.Type == "ENCRYPTED PRIVATE KEY"
}

