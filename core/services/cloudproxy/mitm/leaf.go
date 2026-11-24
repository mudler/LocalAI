package mitm

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"
)

type leafEntry struct {
	cert      *tls.Certificate
	expiresAt time.Time
}

const (
	leafLifetime     = 30 * 24 * time.Hour
	minBeforeReissue = 24 * time.Hour
)

// IssueLeaf returns a TLS certificate for host, signed by this CA.
// Cached per host, re-minted when the cached cert is within
// minBeforeReissue of expiry.
func (c *CA) IssueLeaf(host string) (*tls.Certificate, error) {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(host)

	now := time.Now()

	c.mu.Lock()
	if entry, ok := c.leaves[host]; ok {
		if entry.expiresAt.After(now.Add(minBeforeReissue)) {
			c.mu.Unlock()
			return entry.cert, nil
		}
		delete(c.leaves, host)
	}
	c.mu.Unlock()

	// Mint outside the lock so a slow ECDSA key-gen doesn't block
	// concurrent lookups for already-cached hosts.
	leaf, err := c.mintLeaf(host)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.leaves[host] = &leafEntry{
		cert:      leaf,
		expiresAt: now.Add(leafLifetime),
	}
	c.mu.Unlock()
	return leaf, nil
}

func (c *CA) mintLeaf(host string) (*tls.Certificate, error) {
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("mitm: leaf key for %q: %w", host, err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("mitm: leaf serial: %w", err)
	}

	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    now.Add(-1 * time.Hour),
		NotAfter:     now.Add(leafLifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
		},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &leafKey.PublicKey, c.key)
	if err != nil {
		return nil, fmt.Errorf("mitm: sign leaf for %q: %w", host, err)
	}

	return &tls.Certificate{
		Certificate: [][]byte{der, c.cert.Raw},
		PrivateKey:  leafKey,
	}, nil
}
