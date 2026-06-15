// Package mitm implements a TLS man-in-the-middle proxy that
// applies per-request PII redaction to allowlisted LLM API hosts
// while tunnelling everything else byte-for-byte.
package mitm

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type CA struct {
	cert      *x509.Certificate
	key       *ecdsa.PrivateKey
	publicPEM []byte

	mu     sync.Mutex
	leaves map[string]*leafEntry
}

// LoadOrCreateCA loads the CA from dir if both files exist, or
// generates a new ECDSA-P256 CA and persists it. The key file is
// mode 0600.
func LoadOrCreateCA(dir string) (*CA, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mitm: create ca dir %q: %w", dir, err)
	}

	certPath := filepath.Join(dir, "ca.crt")
	keyPath := filepath.Join(dir, "ca.key")

	certPEM, err1 := os.ReadFile(certPath)
	keyPEM, err2 := os.ReadFile(keyPath)
	if err1 == nil && err2 == nil {
		ca, err := parseCA(certPEM, keyPEM)
		if err == nil {
			return ca, nil
		}
		// Fall through and regenerate. We don't auto-delete the
		// existing files — the operator might have hand-edited
		// them. Surface the parse error instead.
		return nil, fmt.Errorf("mitm: parse existing CA at %s: %w (delete to regenerate)", dir, err)
	}

	ca, certPEMOut, keyPEMOut, err := generateCA()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(certPath, certPEMOut, 0o644); err != nil {
		return nil, fmt.Errorf("mitm: write ca cert %q: %w", certPath, err)
	}
	if err := os.WriteFile(keyPath, keyPEMOut, 0o600); err != nil {
		return nil, fmt.Errorf("mitm: write ca key %q: %w", keyPath, err)
	}
	return ca, nil
}

func generateCA() (*CA, []byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("mitm: generate ca key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("mitm: serial: %w", err)
	}

	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "LocalAI MITM Proxy CA",
			Organization: []string{"LocalAI"},
		},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("mitm: create ca cert: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("mitm: re-parse ca cert: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("mitm: marshal ca key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return &CA{
		cert:      cert,
		key:       key,
		publicPEM: certPEM,
		leaves:    make(map[string]*leafEntry),
	}, certPEM, keyPEM, nil
}

// NewInMemoryCA mints an ephemeral CA for tests.
func NewInMemoryCA() (*CA, error) {
	ca, _, _, err := generateCA()
	return ca, err
}

func parseCA(certPEM, keyPEM []byte) (*CA, error) {
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("mitm: ca cert PEM block missing or wrong type")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("mitm: parse ca cert: %w", err)
	}
	if !cert.IsCA {
		return nil, fmt.Errorf("mitm: stored cert at is not a CA")
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("mitm: ca key PEM block missing")
	}
	var key *ecdsa.PrivateKey
	switch keyBlock.Type {
	case "EC PRIVATE KEY":
		k, err := x509.ParseECPrivateKey(keyBlock.Bytes)
		if err != nil {
			return nil, fmt.Errorf("mitm: parse ec ca key: %w", err)
		}
		key = k
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
		if err != nil {
			return nil, fmt.Errorf("mitm: parse pkcs8 ca key: %w", err)
		}
		ecKey, ok := k.(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("mitm: pkcs8 key is not ECDSA")
		}
		key = ecKey
	default:
		return nil, fmt.Errorf("mitm: unsupported ca key PEM type %q", keyBlock.Type)
	}

	return &CA{
		cert:      cert,
		key:       key,
		publicPEM: certPEM,
		leaves:    make(map[string]*leafEntry),
	}, nil
}

// PublicCertPEM returns a copy of the PEM-encoded CA certificate.
func (c *CA) PublicCertPEM() []byte {
	out := make([]byte, len(c.publicPEM))
	copy(out, c.publicPEM)
	return out
}

func (c *CA) Cert() *x509.Certificate { return c.cert }
