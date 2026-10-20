// SPDX-License-Identifier: MIT
//go:build auth

package addons

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	managerpkg "github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("install refusal in the dispatch response", func() {
	const secret = "secret"
	const refusal = "the add-on needs SDK 2 to 2 and this host provides SDK 1"
	const generic = "Addon request denied"

	// Two releases: "needs2" is built for SDK 2 only, "corrupt" has a digest
	// that does not match its artifact, an unrelated failure.
	setup := func() (*managerpkg.Manager, *echo.Echo, string, contract.Actor, func(*gateway.Gateway) *echo.Echo) {
		ctx := context.Background()
		db, e := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(e).NotTo(HaveOccurred())
		u := auth.User{ID: "admin", Role: auth.RoleAdmin, Status: auth.StatusActive}
		Expect(db.Create(&u).Error).To(Succeed())
		token, key, e := auth.CreateAPIKey(db, u.ID, "test", auth.RoleAdmin, secret, nil)
		Expect(e).NotTo(HaveOccurred())
		actor := contract.Actor{UserID: u.ID, CredentialID: "key:" + key.ID, Mode: "named-user"}
		root := GinkgoT().TempDir()
		manifest := func(id string) string {
			return `{"schemaVersion":1,"id":"` + id + `","version":"1.0.0","executable":"bin/sample","os":"linux","arch":"amd64","host":{"min":1,"max":2},"api":{"min":1,"max":1},"protocol":{"min":1,"max":1},"dataFormat":{"min":1,"max":2},"capabilities":{"service":true,"ui":{"entry":"ui.js","sdk":{"min":2,"max":2},"pages":["home"],"mainPage":"home"}}}`
		}
		var releases []catalog.Release
		for _, id := range []string{"needs2", "corrupt"} {
			var b bytes.Buffer
			tw := tar.NewWriter(&b)
			for name, data := range map[string]string{"manifest.json": manifest(id), "bin/sample": "binary", "ui.js": "export default {};"} {
				Expect(tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(data))})).To(Succeed())
				_, e = tw.Write([]byte(data))
				Expect(e).NotTo(HaveOccurred())
			}
			Expect(tw.Close()).To(Succeed())
			body := b.Bytes()
			artifact := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
			DeferCleanup(artifact.Close)
			digest := fmt.Sprintf("sha256:%x", sha256.Sum256(body))
			if id == "corrupt" {
				digest = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("other")))
			}
			releases = append(releases, catalog.Release{AddonID: id, Name: id, Version: "1.0.0", Artifact: artifact.URL + "/p.tar", Digest: digest, OS: "linux", Arch: "amd64"})
		}
		library := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = catalog.WriteIndex(w, releases) }))
		DeferCleanup(library.Close)
		admin := func(ctx context.Context, a contract.Actor) error { return auth.ValidateAddonActor(ctx, db, a, true) }
		sources, e := catalog.NewSources(filepath.Join(root, "sources.json"), admin, catalog.CredentialResolver{})
		Expect(e).NotTo(HaveOccurred())
		Expect(sources.Save(ctx, actor, catalog.Source{ID: "test", Name: "Test", URL: library.URL, Enabled: true, AllowLoopbackHTTP: true})).To(Succeed())
		_, e = sources.Refresh(ctx, "test")
		Expect(e).NotTo(HaveOccurred())
		c := catalog.Catalog{Sources: sources, TargetOS: "linux", TargetArch: "amd64"}
		cfg := managerpkg.OwnerConfig{InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "disk", Root: filepath.Join(root, "owner")}
		installer := &packages.Installer{Root: filepath.Join(cfg.Root, "packages"), Catalog: c, Host: contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: "linux", Arch: "amd64"}}
		DeferCleanup(func() {
			_ = filepath.Walk(root, func(p string, info os.FileInfo, e error) error {
				if e == nil && info.IsDir() {
					_ = os.Chmod(p, 0700)
				}
				return nil
			})
		})
		m, e := managerpkg.NewManager(ctx, cfg, c, installer, admin)
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(m.Close()).To(Succeed()) })
		mount := func(g *gateway.Gateway) *echo.Echo {
			app := echo.New()
			cfg := &config.ApplicationConfig{}
			cfg.Auth.APIKeyHMACSecret = secret
			app.Use(auth.Middleware(db, cfg))
			group := app.Group("/api/addons", auth.RequireFeature(db, auth.FeatureAddons), auth.CSRFMiddleware(), BrowserProtection())
			group.POST("/dispatch", Dispatch(g, db))
			return app
		}
		revalidate := func(ctx context.Context, a contract.Actor) error { return auth.ValidateAddonActor(ctx, db, a, false) }
		return m, mount(&gateway.Gateway{Manager: m, Revalidate: revalidate}), token, actor, mount
	}
	install := func(app *echo.Echo, token, id string) (int, string) {
		body := `{"addonId":"` + id + `","operationId":"op-` + id + `","generation":0,"action":"install","arguments":{"sourceId":"test","version":"1.0.0"}}`
		r := httptest.NewRequest(http.MethodPost, "/api/addons/dispatch", bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	message := func(body string) string {
		var out errorBody
		ExpectWithOffset(1, json.Unmarshal([]byte(body), &out)).To(Succeed(), body)
		m, _ := out.Error["message"].(string)
		return m
	}

	It("returns the SDK refusal text to the administrator and the generic message for an unrelated failure", func() {
		_, app, token, _, _ := setup()
		code, body := install(app, token, "needs2")
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(message(body)).To(Equal(refusal))
		code, body = install(app, token, "corrupt")
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(message(body)).To(Equal(generic))
	})

	It("returns the same text through a forwarding frontend", func() {
		m, _, token, _, mount := setup()
		certs, roots := certificates()
		pins := map[string][32]byte{}
		keys := map[string]ed25519.PublicKey{}
		private := map[string]ed25519.PrivateKey{}
		for _, id := range []string{"owner", "front-a"} {
			pins[id] = sha256.Sum256(certs[id].Certificate[0])
			pub, priv, e := ed25519.GenerateKey(rand.Reader)
			Expect(e).NotTo(HaveOccurred())
			keys[id], private[id] = pub, priv
		}
		owner := httptest.NewUnstartedServer(nil)
		carrier := func(id string) *gateway.Carrier {
			c, e := gateway.NewCarrier(gateway.CarrierConfig{InstanceID: id, OwnerID: "owner", OwnerAddress: "https://" + owner.Listener.Addr().String(), Certificate: certs[id], Roots: roots, Pins: pins, SigningKey: private[id], VerificationKeys: keys})
			Expect(e).NotTo(HaveOccurred())
			return c
		}
		allow := func(context.Context, contract.Actor) error { return nil }
		var e error
		owner.Config.Handler, e = carrier("owner").OwnerHandler(&gateway.Gateway{Manager: m, Revalidate: allow})
		Expect(e).NotTo(HaveOccurred())
		owner.TLS = carrier("owner").ServerTLS()
		owner.StartTLS()
		DeferCleanup(owner.Close)
		front := mount(&gateway.Gateway{Forward: carrier("front-a"), Revalidate: allow})
		code, body := install(front, token, "needs2")
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(message(body)).To(Equal(refusal))
		code, body = install(front, token, "corrupt")
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(message(body)).To(Equal(generic))
	})
})
