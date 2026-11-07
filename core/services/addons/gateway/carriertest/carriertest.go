// SPDX-License-Identifier: MIT

// Package carriertest starts a real add-on owner carrier for tests in other
// packages: an owner server with pinned mutual TLS and the carriers of two
// frontends, front-a and front-b. Only tests import it.
package carriertest

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http/httptest"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The instance IDs of the fixture.
const (
	Owner  = "owner"
	FrontA = "front-a"
	FrontB = "front-b"
)

// Fixture is a running owner and the carriers of every instance.
type Fixture struct {
	Server   *httptest.Server
	Carriers map[string]*gateway.Carrier
}

// Close stops the owner server. A frontend call after it finds no owner.
func (f *Fixture) Close() { f.Server.Close() }

// Frontend returns a gateway of a frontend that forwards to the owner and
// accepts every account, as the host check of a test does.
func (f *Fixture) Frontend(id string) *gateway.Gateway {
	return &gateway.Gateway{Forward: f.Carriers[id], Revalidate: allow, RevalidateAccount: allow}
}

func allow(context.Context, contract.Actor) error { return nil }

// New starts the owner. The owner serves the handler of the owner gateway
// that owner builds; a nil gateway is refused.
func New(owner func() *gateway.Gateway) (*Fixture, error) {
	ids := []string{Owner, FrontA, FrontB}
	certs, roots, err := certificates(ids)
	if err != nil {
		return nil, err
	}
	pins := map[string][32]byte{}
	keys := map[string]ed25519.PublicKey{}
	private := map[string]ed25519.PrivateKey{}
	for _, id := range ids {
		pins[id] = sha256.Sum256(certs[id].Certificate[0])
		pub, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		keys[id], private[id] = pub, key
	}
	server := httptest.NewUnstartedServer(nil)
	carriers := map[string]*gateway.Carrier{}
	for _, id := range ids {
		c, err := gateway.NewCarrier(gateway.CarrierConfig{InstanceID: id, OwnerID: Owner, OwnerAddress: "https://" + server.Listener.Addr().String(), Certificate: certs[id], Roots: roots, Pins: pins, SigningKey: private[id], VerificationKeys: keys})
		if err != nil {
			return nil, err
		}
		carriers[id] = c
	}
	g := owner()
	if g == nil {
		return nil, errors.New("carriertest: no owner gateway")
	}
	h, err := carriers[Owner].OwnerHandler(g)
	if err != nil {
		return nil, err
	}
	server.Config.Handler = h
	// The production owner server has the same write bound.
	server.Config.WriteTimeout = 65 * time.Second
	server.TLS = carriers[Owner].ServerTLS()
	server.StartTLS()
	return &Fixture{Server: server, Carriers: carriers}, nil
}

func certificates(ids []string) (map[string]tls.Certificate, *x509.CertPool, error) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, pub, key)
	if err != nil {
		return nil, nil, err
	}
	if ca, err = x509.ParseCertificate(der); err != nil {
		return nil, nil, err
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	out := map[string]tls.Certificate{}
	for i, id := range ids {
		p, k, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, nil, err
		}
		c := &x509.Certificate{SerialNumber: big.NewInt(int64(i + 2)), Subject: pkix.Name{CommonName: id}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		d, err := x509.CreateCertificate(rand.Reader, c, ca, p, key)
		if err != nil {
			return nil, nil, err
		}
		kd, err := x509.MarshalPKCS8PrivateKey(k)
		if err != nil {
			return nil, nil, err
		}
		pair, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd}))
		if err != nil {
			return nil, nil, err
		}
		out[id] = pair
	}
	return out, pool, nil
}
