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
	"math/big"
	"net"
	"time"
)

// NewLoopbackCarrierConfig builds the carrier identity of a deployment with one
// instance, which is its own owner. The keys and the certificate are made fresh
// in memory, never written to disk and never leave the process, so the carrier
// is reachable only by this process on the loopback interface. It returns the
// config and the loopback address the owner listens on.
func NewLoopbackCarrierConfig(instanceID string) (*CarrierConfig, string, error) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	address := probe.Addr().String()
	_ = probe.Close()
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
