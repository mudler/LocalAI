// SPDX-License-Identifier: MIT
package gateway

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Carrier identities of a split deployment. The owner identity is the same
// value a single-node root already stores, so such a root keeps passing the
// owner identity check. Every frontend replica shares one identity: the owner
// keys replay protection by peer and nonce, so it needs no list of replicas.
const (
	OwnerIdentity    = "local"
	FrontendIdentity = "frontend"
)

// Names of the files in a carrier key directory.
const (
	carrierCAFile         = "ca.pem"
	carrierOwnerCert      = "owner.pem"
	carrierOwnerKey       = "owner.key"
	carrierFrontendCert   = "frontend.pem"
	carrierFrontendKey    = "frontend.key"
	carrierFrontendSign   = "frontend-sign.key"
	carrierFrontendVerify = "frontend-sign.pub"
)

var carrierKeyFiles = []string{carrierCAFile, carrierOwnerCert, carrierOwnerKey, carrierFrontendCert, carrierFrontendKey, carrierFrontendSign, carrierFrontendVerify}

// carrierRoleFiles lists what each role reads. A role never reads another
// role's private keys.
var carrierRoleFiles = map[string][]string{
	"owner":    {carrierCAFile, carrierOwnerCert, carrierOwnerKey, carrierFrontendCert, carrierFrontendVerify},
	"frontend": {carrierCAFile, carrierOwnerCert, carrierFrontendCert, carrierFrontendKey, carrierFrontendSign},
}

// GenerateCarrierKeys writes a static carrier key set into dir: a CA
// certificate, an owner leaf (server and client auth, with a SAN for each of
// ownerHosts), a frontend leaf (client auth) and the frontend signing key pair.
// The CA key stays in memory and is dropped, so nobody can issue a third leaf
// from this set. It refuses to overwrite any existing file.
func GenerateCarrierKeys(dir string, ownerHosts []string) error {
	if len(ownerHosts) == 0 {
		return errors.New("at least one owner host is required")
	}
	for _, h := range ownerHosts {
		if h == "" {
			return errors.New("owner host must not be empty")
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, name := range carrierKeyFiles {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			return fmt.Errorf("%s already exists in %s; refusing to overwrite a carrier key set", name, dir)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	caPub, caKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	notBefore := time.Now().Add(-time.Hour)
	notAfter := time.Now().AddDate(10, 0, 0)
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "addon carrier CA"}, NotBefore: notBefore, NotAfter: notAfter, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caPub, caKey)
	if err != nil {
		return err
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		return err
	}
	ownerTemplate := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: OwnerIdentity}, NotBefore: notBefore, NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	for _, h := range ownerHosts {
		if ip := net.ParseIP(h); ip != nil {
			ownerTemplate.IPAddresses = append(ownerTemplate.IPAddresses, ip)
		} else {
			ownerTemplate.DNSNames = append(ownerTemplate.DNSNames, h)
		}
	}
	frontendTemplate := &x509.Certificate{SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: FrontendIdentity}, NotBefore: notBefore, NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	ownerCert, ownerKey, err := signedLeaf(ownerTemplate, ca, caKey)
	if err != nil {
		return err
	}
	frontendCert, frontendKey, err := signedLeaf(frontendTemplate, ca, caKey)
	if err != nil {
		return err
	}
	signPub, signKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	signKeyDER, err := x509.MarshalPKCS8PrivateKey(signKey)
	if err != nil {
		return err
	}
	signPubDER, err := x509.MarshalPKIXPublicKey(signPub)
	if err != nil {
		return err
	}
	files := []struct {
		name string
		data []byte
	}{
		{carrierCAFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})},
		{carrierOwnerCert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ownerCert})},
		{carrierOwnerKey, ownerKey},
		{carrierFrontendCert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: frontendCert})},
		{carrierFrontendKey, frontendKey},
		{carrierFrontendSign, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: signKeyDER})},
		{carrierFrontendVerify, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: signPubDER})},
	}
	for i, f := range files {
		if err := writeNewKeyFile(filepath.Join(dir, f.name), f.data); err != nil {
			// Do not leave a partial set that a later run would refuse.
			for _, done := range files[:i] {
				_ = os.Remove(filepath.Join(dir, done.name))
			}
			return err
		}
	}
	return nil
}

func signedLeaf(template *x509.Certificate, ca *x509.Certificate, caKey ed25519.PrivateKey) (certDER, keyPEM []byte, err error) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	certDER, err = x509.CreateCertificate(rand.Reader, template, ca, pub, caKey)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return certDER, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), nil
}

func writeNewKeyFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// LoadCarrierConfig reads the files of one role from a key directory made by
// GenerateCarrierKeys and returns a carrier config. The role is "owner" or
// "frontend". Only that role's files are read, and a file that group or other
// users can access is refused.
func LoadCarrierConfig(dir, role, ownerAddress string) (*CarrierConfig, error) {
	names, ok := carrierRoleFiles[role]
	if !ok {
		return nil, fmt.Errorf("unknown carrier role %q", role)
	}
	raw := map[string][]byte{}
	for _, name := range names {
		b, err := readKeyFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		raw[name] = b
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(raw[carrierCAFile]) {
		return nil, fmt.Errorf("%s holds no certificate", filepath.Join(dir, carrierCAFile))
	}
	ownerLeaf, err := leafDER(raw[carrierOwnerCert], carrierOwnerCert)
	if err != nil {
		return nil, err
	}
	frontendLeaf, err := leafDER(raw[carrierFrontendCert], carrierFrontendCert)
	if err != nil {
		return nil, err
	}
	cfg := &CarrierConfig{
		OwnerID: OwnerIdentity, OwnerAddress: ownerAddress, Roots: roots,
		Pins: map[string][32]byte{OwnerIdentity: sha256.Sum256(ownerLeaf), FrontendIdentity: sha256.Sum256(frontendLeaf)},
	}
	if role == "owner" {
		cfg.InstanceID = OwnerIdentity
		cfg.Certificate, err = tls.X509KeyPair(raw[carrierOwnerCert], raw[carrierOwnerKey])
		if err != nil {
			return nil, fmt.Errorf("owner certificate and key: %w", err)
		}
		pub, err := parsePublicKey(raw[carrierFrontendVerify])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", carrierFrontendVerify, err)
		}
		cfg.VerificationKeys = map[string]ed25519.PublicKey{FrontendIdentity: pub}
		// The owner never signs an outbound request, so it holds no signing key.
		return cfg, nil
	}
	cfg.InstanceID = FrontendIdentity
	cfg.Certificate, err = tls.X509KeyPair(raw[carrierFrontendCert], raw[carrierFrontendKey])
	if err != nil {
		return nil, fmt.Errorf("frontend certificate and key: %w", err)
	}
	key, err := parseSigningKey(raw[carrierFrontendSign])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", carrierFrontendSign, err)
	}
	cfg.SigningKey = key
	return cfg, nil
}

// readKeyFile reads a regular file. Key files must not be accessible to group
// or other users, so a file that someone else could have changed fails closed.
func readKeyFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular file", path)
	}
	// On Windows, skip the Unix-style permission check since Windows
	// doesn't use Unix-style file permissions (0600). The file is
	// already protected by being inside the user's data directory.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s must have mode 0600 or less", path)
	}
	return io.ReadAll(io.LimitReader(f, 1<<20))
}

func leafDER(certPEM []byte, name string) ([]byte, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%s holds no certificate", name)
	}
	return block.Bytes, nil
}

func parsePublicKey(b []byte) (ed25519.PublicKey, error) {
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errors.New("not a PEM file")
	}
	k, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	pub, ok := k.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("not an ed25519 public key")
	}
	return pub, nil
}

func parseSigningKey(b []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errors.New("not a PEM file")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("not an ed25519 private key")
	}
	return key, nil
}
