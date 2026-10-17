// SPDX-License-Identifier: MIT
//go:build auth

package addons

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	managerpkg "github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/httpclient"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

var _ = Describe("alternating authenticated frontends", func() {
	It("serves one owner journal through two real authenticated HTTP frontends and rejects paused keys", func() {
		ctx := context.Background()
		db, e := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(e).NotTo(HaveOccurred())
		u := auth.User{ID: "admin", Role: auth.RoleAdmin, Status: auth.StatusActive}
		Expect(db.Create(&u).Error).To(Succeed())
		token, key, e := auth.CreateAPIKey(db, u.ID, "test", auth.RoleAdmin, "secret", nil)
		Expect(e).NotTo(HaveOccurred())
		actor := contract.Actor{UserID: u.ID, CredentialID: "key:" + key.ID, Mode: "named-user"}
		root := filepath.Join(GinkgoT().TempDir(), "owner")
		m, e := managerpkg.NewManager(ctx, managerpkg.OwnerConfig{InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "disk", Root: root}, catalog.Catalog{}, &packages.Installer{Root: filepath.Join(root, "packages")}, func(ctx context.Context, a contract.Actor) error { return auth.ValidateAddonActor(ctx, db, a, true) })
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(m.Close)
		_, e = m.Submit(ctx, actor, managerpkg.LifecycleCommand{AddonID: "sample", OperationID: "original", Action: "install", SourceID: "absent", Version: "1.0.0"})
		Expect(e).To(HaveOccurred())
		certs, roots := certificates()
		pins := map[string][32]byte{}
		keys := map[string]ed25519.PublicKey{}
		private := map[string]ed25519.PrivateKey{}
		for _, id := range []string{"owner", "front-a", "front-b"} {
			pins[id] = sha256.Sum256(certs[id].Certificate[0])
			pub, priv, e := ed25519.GenerateKey(rand.Reader)
			Expect(e).NotTo(HaveOccurred())
			keys[id] = pub
			private[id] = priv
		}
		owner := httptest.NewUnstartedServer(nil)
		cs := map[string]*gateway.Carrier{}
		for _, id := range []string{"owner", "front-a", "front-b"} {
			c, e := gateway.NewCarrier(gateway.CarrierConfig{InstanceID: id, OwnerID: "owner", OwnerAddress: "https://" + owner.Listener.Addr().String(), Certificate: certs[id], Roots: roots, Pins: pins, SigningKey: private[id], VerificationKeys: keys})
			Expect(e).NotTo(HaveOccurred())
			cs[id] = c
		}
		revalidate := func(ctx context.Context, a contract.Actor) error { return auth.ValidateAddonActor(ctx, db, a, false) }
		owner.Config.Handler, e = cs["owner"].OwnerHandler(&gateway.Gateway{Manager: m, Revalidate: revalidate})
		Expect(e).NotTo(HaveOccurred())
		owner.TLS = cs["owner"].ServerTLS()
		owner.StartTLS()
		DeferCleanup(owner.Close)
		fronts := []*httptest.Server{}
		for _, id := range []string{"front-a", "front-b"} {
			app := echo.New()
			cfg := &config.ApplicationConfig{}
			cfg.Auth.APIKeyHMACSecret = "secret"
			app.Use(auth.Middleware(db, cfg))
			app.POST("/api/addons/dispatch", Dispatch(&gateway.Gateway{Forward: cs[id], Revalidate: revalidate}, db), auth.RequireFeature(db, auth.FeatureAddons), BrowserProtection())
			front := httptest.NewServer(app)
			DeferCleanup(front.Close)
			fronts = append(fronts, front)
		}
		client := httpclient.New()
		client.Timeout = 5 * time.Second
		for i := 0; i < 4; i++ {
			req, e := http.NewRequest("POST", fronts[i%2].URL+"/api/addons/dispatch", bytes.NewBufferString(`{"addonId":"sample","operationId":"original","generation":0,"action":"operation-status","arguments":{}}`))
			Expect(e).NotTo(HaveOccurred())
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("X-Forwarded-Actor", "other")
			resp, e := client.Do(req)
			Expect(e).NotTo(HaveOccurred())
			b, e := io.ReadAll(resp.Body)
			resp.Body.Close()
			Expect(e).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(200))
			var result contract.Result
			Expect(json.Unmarshal(b, &result)).To(Succeed())
			Expect(string(result.Payload)).To(ContainSubstring("failed"))
		}
		Expect(auth.SetAPIKeyPause(db, key.ID, u.ID, true, nil)).To(Succeed())
		req, e := http.NewRequest("POST", fronts[1].URL+"/api/addons/dispatch", bytes.NewBufferString(`{}`))
		Expect(e).NotTo(HaveOccurred())
		req.Header.Set("Authorization", "Bearer "+token)
		resp, e := client.Do(req)
		Expect(e).NotTo(HaveOccurred())
		resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(401))
	})
})
