// SPDX-License-Identifier: MIT

// Package mailtest holds an in-process IMAP server and an in-process SMTP
// server for tests, like slacktest for Slack and githubtest for GitHub. Both
// speak the real protocols over real sockets on the loopback address, with
// implicit TLS, STARTTLS or no encryption, and keep what the add-on did to
// them so that a test can see it: the commands the client sent, the messages
// that were appended and sent, and which messages carry the \Seen flag.
package mailtest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/netip"
	"time"
)

// CA is a certificate authority that issues the certificates of the fakes.
type CA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

// NewCA makes a new authority.
func NewCA() *CA {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mailtest CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &CA{cert: cert, key: key, pool: pool}
}

// Pool is the pool of roots that trusts the authority.
func (c *CA) Pool() *x509.CertPool { return c.pool }

// Config is a server configuration whose certificate is valid for names, each
// a DNS name or an IP literal. With no name it is valid for 127.0.0.1.
func (c *CA) Config(names ...string) *tls.Config {
	if len(names) == 0 {
		names = []string{"127.0.0.1"}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: names[0]},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, n := range names {
		if a, err := netip.ParseAddr(n); err == nil {
			tpl.IPAddresses = append(tpl.IPAddresses, net.IP(a.AsSlice()))
		} else {
			tpl.DNSNames = append(tpl.DNSNames, n)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		panic(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
}

// Wrap makes l accept TLS connections with a certificate for names.
func (c *CA) Wrap(l net.Listener, names ...string) net.Listener {
	return tls.NewListener(l, c.Config(names...))
}

// PEM is the certificate of the authority, for a process that cannot share the pool.
func (c *CA) PEM() string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.cert.Raw}))
}
