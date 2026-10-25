// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"
)

func certificates() (map[string]tls.Certificate, *x509.CertPool) {
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	Expect(e).NotTo(HaveOccurred())
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, pub, key)
	Expect(e).NotTo(HaveOccurred())
	ca, e = x509.ParseCertificate(der)
	Expect(e).NotTo(HaveOccurred())
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	out := map[string]tls.Certificate{}
	for i, id := range []string{"owner", "front-a", "front-b", "stranger"} {
		p, k, e := ed25519.GenerateKey(rand.Reader)
		Expect(e).NotTo(HaveOccurred())
		c := &x509.Certificate{SerialNumber: big.NewInt(int64(i + 2)), Subject: pkix.Name{CommonName: id}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		d, e := x509.CreateCertificate(rand.Reader, c, ca, p, key)
		Expect(e).NotTo(HaveOccurred())
		kd, e := x509.MarshalPKCS8PrivateKey(k)
		Expect(e).NotTo(HaveOccurred())
		pair, e := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd}))
		Expect(e).NotTo(HaveOccurred())
		out[id] = pair
	}
	return out, pool
}

type fixtureEnv struct {
	server   *httptest.Server
	carriers map[string]*Carrier
	certs    map[string]tls.Certificate
	roots    *x509.CertPool
	pins     map[string][32]byte
	keys     map[string]ed25519.PublicKey
	private  map[string]ed25519.PrivateKey
}

// carrierFixtureWith starts a real mTLS owner server. Frontends are built with
// the given limits; the zero value selects the defaults.
func carrierFixtureWith(limits RelayLimits, handler func(*Carrier) http.Handler) *fixtureEnv {
	certs, roots := certificates()
	pins := map[string][32]byte{}
	keys := map[string]ed25519.PublicKey{}
	private := map[string]ed25519.PrivateKey{}
	for _, id := range []string{"owner", "front-a", "front-b"} {
		pins[id] = sha256.Sum256(certs[id].Certificate[0])
		pub, key, e := ed25519.GenerateKey(rand.Reader)
		Expect(e).NotTo(HaveOccurred())
		keys[id] = pub
		private[id] = key
	}
	server := httptest.NewUnstartedServer(nil)
	carriers := map[string]*Carrier{}
	for _, id := range []string{"owner", "front-a", "front-b"} {
		c, e := NewCarrier(CarrierConfig{Limits: limits, InstanceID: id, OwnerID: "owner", OwnerAddress: "https://" + server.Listener.Addr().String(), Certificate: certs[id], Roots: roots, Pins: pins, SigningKey: private[id], VerificationKeys: keys})
		Expect(e).NotTo(HaveOccurred())
		carriers[id] = c
	}
	server.Config.Handler = handler(carriers["owner"])
	server.TLS = carriers["owner"].ServerTLS()
	server.StartTLS()
	DeferCleanup(server.Close)
	return &fixtureEnv{server: server, carriers: carriers, certs: certs, roots: roots, pins: pins, keys: keys, private: private}
}
func carrierFixture(handler func(*Carrier) http.Handler) (*httptest.Server, map[string]*Carrier) {
	env := carrierFixtureWith(RelayLimits{}, handler)
	return env.server, env.carriers
}

var _ = Describe("real owner transport", func() {
	It("alternates pinned frontends and rejects untrusted certificates without local fallback", func() {
		var checks atomic.Int32
		server, cs := carrierFixture(func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(&Gateway{Revalidate: func(context.Context, contract.Actor) error { checks.Add(1); return nil }})
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		actor := contract.Actor{UserID: "u", CredentialID: "key:k", Mode: "named-user"}
		for i := 0; i < 4; i++ {
			id := "front-a"
			if i%2 == 1 {
				id = "front-b"
			}
			_, e := cs[id].Dispatch(context.Background(), actor, contract.Request{OperationID: "original", Action: "operation-status"})
			Expect(e).To(Equal(ErrUnavailable))
		}
		Expect(checks.Load()).To(Equal(int32(8)))
		cs["front-a"].client.Transport.(*http.Transport).TLSClientConfig.Certificates = nil
		_, e := cs["front-a"].Dispatch(context.Background(), actor, contract.Request{OperationID: "op", Action: "operation-status"})
		Expect(e).To(HaveOccurred())
		Expect(checks.Load()).To(Equal(int32(8)))
		server.Close()
		_, e = cs["front-b"].Dispatch(context.Background(), actor, contract.Request{OperationID: "op", Action: "operation-status"})
		Expect(e).To(Equal(ErrUnavailable))
	})
	It("reports the original operation after lost mutation replies without retrying", func() {
		var dispatches atomic.Int32
		_, cs := carrierFixture(func(c *Carrier) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/addons/audience" {
					_, _ = w.Write([]byte(`{"audience":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}`))
					return
				}
				dispatches.Add(1)
				conn, _, e := w.(http.Hijacker).Hijack()
				Expect(e).NotTo(HaveOccurred())
				_ = conn.Close()
			})
		})
		_, e := cs["front-a"].Dispatch(context.Background(), contract.Actor{UserID: "u", CredentialID: "k", Mode: "named-user"}, contract.Request{OperationID: "same-id", Action: "enable"})
		Expect(e).To(Equal(&OutcomeUnknown{OperationID: "same-id"}))
		Expect(dispatches.Load()).To(Equal(int32(1)))
	})
	It("rejects a stream longer than its receiving frontend budget", func() {
		_, cs := carrierFixture(func(c *Carrier) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/addons/audience" {
					_, _ = w.Write([]byte(`{"audience":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}`))
					return
				}
				w.Header().Set("Content-Type", "application/octet-stream")
				block := make([]byte, 1<<20)
				for i := 0; i < 65; i++ {
					if _, e := w.Write(block); e != nil {
						return
					}
				}
			})
		})
		e := cs["front-a"].Relay(context.Background(), contract.Actor{UserID: "u", CredentialID: "k", Mode: "named-user"}, RelayRequest{Request: contract.Request{OperationID: "op"}, Kind: "file"}, httptest.NewRecorder())
		Expect(e).To(HaveOccurred())
	})
})
