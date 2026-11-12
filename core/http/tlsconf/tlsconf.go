// SPDX-License-Identifier: MIT

// Package tlsconf gives the API server a TLS certificate: one the operator
// supplies, or a self-signed one that LocalAI makes once and keeps in its data
// path. TLS is off unless one of the two is asked for.
//
// A paired client pins the certificate by its SHA-256 fingerprint, which the
// pairing offer carries, so a self-signed certificate is as strong as any other
// for that client. The names inside the certificate do not matter to it.
package tlsconf

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// SelfSignedDir is the folder of the data path that keeps the self-signed
	// certificate, and the names of its two files.
	SelfSignedDir  = "tls"
	selfSignedCert = "self-signed.crt"
	selfSignedKey  = "self-signed.key"

	// selfSignedLife is how long a made certificate is valid. A client that pins
	// the fingerprint loses its pin when the certificate is replaced, so the life
	// is long, and the certificate is replaced only when fewer than
	// selfSignedRenewBefore remain.
	selfSignedLife        = 10 * 365 * 24 * time.Hour
	selfSignedRenewBefore = 30 * 24 * time.Hour
)

// Material is the certificate of the server with its identity for pinning.
type Material struct {
	Certificate tls.Certificate
	// Fingerprint is the SHA-256 of the certificate in DER form, in lower case
	// hex: 64 characters.
	Fingerprint string
	// SelfSigned is true for a certificate that LocalAI made.
	SelfSigned bool
	// Persistent is false for a self-signed certificate kept in memory only, which
	// is new at each start and so has a new fingerprint.
	Persistent bool
}

// Digest is the short form of a fingerprint that two people can compare by
// eye: the first eight hex digits, upper case, in two groups, like AB12-CD34.
func (m *Material) Digest() string { return Digest(m.Fingerprint) }

// Digest returns the short form of a hex fingerprint, or "" when it is shorter
// than eight characters.
func Digest(fingerprint string) string {
	f := strings.ToUpper(fingerprint)
	if len(f) < 8 {
		return ""
	}
	return f[:4] + "-" + f[4:8]
}

// FingerprintOf returns the SHA-256 of a DER certificate as lower case hex.
func FingerprintOf(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// Config returns the server configuration: TLS 1.3 at least, and HTTP/2 offered
// next to HTTP/1.1 (a websocket still uses HTTP/1.1).
func (m *Material) Config() *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{m.Certificate},
		NextProtos:   []string{"h2", "http/1.1"},
	}
}

// PinnedClientConfig returns the configuration of a client of the server's own
// API that trusts one certificate: the one with this SHA-256 fingerprint. A call
// from the process to itself (a health probe, a service that reaches the API
// over the loopback) has no host name that the certificate would list, and it
// should not trust the system roots either, so it pins the certificate it knows.
// TLS 1.3 is the minimum, as for the server.
func PinnedClientConfig(fingerprint string) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		// The pin below replaces the name and chain check.
		InsecureSkipVerify: true, // #nosec G402 -- VerifyPeerCertificate pins the certificate
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 || FingerprintOf(raw[0]) != fingerprint {
				return errors.New("tls: the certificate is not the pinned one")
			}
			return nil
		},
	}
}

// ErrBothSources is the refusal of a certificate file together with the
// self-signed option.
var ErrBothSources = errors.New("tls: use either a certificate and key, or --tls-self-signed, not both")

// Resolve returns the certificate the flags ask for, or nil when TLS is off. A
// certificate file needs its key and the other way round. dataPath is where a
// self-signed certificate is kept; empty keeps it in memory.
func Resolve(certFile, keyFile string, selfSigned bool, dataPath string) (*Material, error) {
	switch {
	case certFile == "" && keyFile == "" && !selfSigned:
		return nil, nil
	case selfSigned && (certFile != "" || keyFile != ""):
		return nil, ErrBothSources
	case selfSigned:
		return SelfSigned(dataPath, time.Now())
	case certFile == "" || keyFile == "":
		return nil, errors.New("tls: a certificate needs its key: set both --tls-cert and --tls-key")
	}
	return Load(certFile, keyFile)
}

// Load reads a certificate and its key from PEM files. The fingerprint is that
// of the first certificate of the file, the one the server presents.
func Load(certFile, keyFile string) (*Material, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("tls: load certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("tls: parse certificate: %w", err)
	}
	if time.Now().After(leaf.NotAfter) {
		return nil, fmt.Errorf("tls: the certificate expired on %s", leaf.NotAfter.Format(time.DateOnly))
	}
	cert.Leaf = leaf
	return &Material{Certificate: cert, Fingerprint: FingerprintOf(cert.Certificate[0]), Persistent: true}, nil
}

// SelfSigned returns the self-signed certificate kept in dataPath, and makes it
// first when it is missing, unreadable or close to its end. With an empty
// dataPath it makes one in memory. The key is written with mode 0600.
func SelfSigned(dataPath string, now time.Time) (*Material, error) {
	if dataPath == "" {
		m, err := makeSelfSigned(now)
		if err != nil {
			return nil, err
		}
		return m, nil
	}
	dir := filepath.Join(dataPath, SelfSignedDir)
	certPath, keyPath := filepath.Join(dir, selfSignedCert), filepath.Join(dir, selfSignedKey)
	if m, err := Load(certPath, keyPath); err == nil && m.Certificate.Leaf.NotAfter.Sub(now) > selfSignedRenewBefore {
		m.SelfSigned = true
		return m, nil
	}
	m, err := makeSelfSigned(now)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("tls: create %s: %w", dir, err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(m.Certificate.PrivateKey)
	if err != nil {
		return nil, err
	}
	if err := writeAtomic(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return nil, err
	}
	if err := writeAtomic(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: m.Certificate.Certificate[0]}), 0o644); err != nil {
		return nil, err
	}
	m.Persistent = true
	return m, nil
}

func makeSelfSigned(now time.Time) (*Material, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("tls: generate key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, fmt.Errorf("tls: serial: %w", err)
	}
	dns, ips := localNames()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "LocalAI"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(selfSignedLife),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dns,
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("tls: make certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &Material{
		Certificate: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf},
		Fingerprint: FingerprintOf(der),
		SelfSigned:  true,
	}, nil
}

// localNames lists the names a browser or a client could use for this host, so
// that a client that checks names still finds one. A pinning client does not.
func localNames() (dns []string, ips []net.IP) {
	dns = []string{"localhost"}
	ips = []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	if h, err := os.Hostname(); err == nil && h != "" && h != "localhost" {
		dns = append(dns, h)
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return dns, ips
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			ips = append(ips, n.IP)
		}
	}
	return dns, ips
}

// writeAtomic writes through a temporary file in the same folder and renames it,
// so a crash never leaves half a key or half a certificate behind.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tls-*")
	if err != nil {
		return fmt.Errorf("tls: write %s: %w", path, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
