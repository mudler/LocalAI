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
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// loopbackPortFile names the file in the add-on state root that keeps the
// loopback port of the owner.
const loopbackPortFile = "loopback-port"

// loopbackPort returns the port the owner listens on. The owner persists its
// own address in its identity file and refuses to start when the address
// differs on a later start, so the port must be the same on every start for
// the same state root. A random port at each start broke that check. The port
// is chosen once, kept in the root, and reused. If it is taken by another
// process, the start fails with a clear error: another port would change the
// address and the identity would no longer match.
func loopbackPort(root string) (int, error) {
	path := filepath.Join(root, loopbackPortFile)
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		port, perr := strconv.Atoi(strings.TrimSpace(string(raw)))
		if perr != nil || port < 1 || port > 65535 {
			return 0, fmt.Errorf("add-on owner loopback port file %s is not a valid port; fix or remove it together with the add-on state", path)
		}
		probe, lerr := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if lerr != nil {
			return 0, fmt.Errorf("add-on owner loopback port %d is already in use (%v); free the port, or turn the owner off with --addons-enabled=false", port, lerr)
		}
		_ = probe.Close()
		return port, nil
	case !os.IsNotExist(err):
		return 0, err
	}
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return 0, err
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(port)+"\n"), 0o600); err != nil {
		return 0, err
	}
	return port, nil
}

// NewLoopbackCarrierConfig builds the carrier identity of a deployment with one
// instance, which is its own owner. The keys and the certificate are made fresh
// in memory, never written to disk and never leave the process, so the carrier
// is reachable only by this process on the loopback interface. The port is kept
// in root and is the same on every start (see loopbackPort); the certificate
// and the pin do not enter the address, so they stay in memory. It returns the
// config and the loopback address the owner listens on.
func NewLoopbackCarrierConfig(instanceID, root string) (*CarrierConfig, string, error) {
	port, err := loopbackPort(root)
	if err != nil {
		return nil, "", err
	}
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	caPub, caKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	notBefore := time.Now().Add(-time.Hour)
	notAfter := time.Now().AddDate(10, 0, 0)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "local addon owner CA"}, NotBefore: notBefore, NotAfter: notAfter, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, caPub, caKey)
	if err != nil {
		return nil, "", err
	}
	if ca, err = x509.ParseCertificate(der); err != nil {
		return nil, "", err
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	leafPub, leafKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: instanceID}, NotBefore: notBefore, NotAfter: notAfter, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, leafPub, caKey)
	if err != nil {
		return nil, "", err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		return nil, "", err
	}
	pair, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		return nil, "", err
	}
	signPub, signKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	return &CarrierConfig{
		InstanceID: instanceID, OwnerID: instanceID, OwnerAddress: "https://" + address,
		Certificate: pair, Roots: pool,
		Pins:             map[string][32]byte{instanceID: sha256.Sum256(leafDER)},
		SigningKey:       signKey,
		VerificationKeys: map[string]ed25519.PublicKey{instanceID: signPub},
	}, address, nil
}
