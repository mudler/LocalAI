// SPDX-License-Identifier: MIT
//go:build auth

package addons

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/hostgrants"
	"github.com/mudler/LocalAI/core/services/addons/uploads"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The file routes with real accounts: an administrator stages with a session or
// a key, nobody else does, and a file belongs to the user that uploaded it.
var _ = Describe("add-on file routes with real sessions and API keys", func() {
	It("lets administrators stage a file and keeps it from everyone else", func() {
		db, err := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(err).NotTo(HaveOccurred())
		admin := auth.User{ID: "admin", Role: auth.RoleAdmin, Status: auth.StatusActive}
		other := auth.User{ID: "admin2", Role: auth.RoleAdmin, Status: auth.StatusActive}
		plain := auth.User{ID: "plain", Role: auth.RoleUser, Status: auth.StatusActive}
		for _, u := range []*auth.User{&admin, &other, &plain} {
			Expect(db.Create(u).Error).To(Succeed())
		}
		adminKey, _, err := auth.CreateAPIKey(db, admin.ID, "test", auth.RoleAdmin, "secret", nil)
		Expect(err).NotTo(HaveOccurred())
		adminSession, err := auth.CreateSession(db, admin.ID, "secret")
		Expect(err).NotTo(HaveOccurred())
		otherSession, err := auth.CreateSession(db, other.ID, "secret")
		Expect(err).NotTo(HaveOccurred())
		plainSession, err := auth.CreateSession(db, plain.ID, "secret")
		Expect(err).NotTo(HaveOccurred())

		store, err := hostgrants.Open(filepath.Join(GinkgoT().TempDir(), "hostgrants.json"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(store.Close)
		owner := &fileOwner{store: &uploads.Store{Root: filepath.Join(GinkgoT().TempDir(), "uploads")}, max: 1 << 20}
		svc, err := gateway.NewHostGrantsOwner(gateway.HostGrantsConfig{Store: store, Manager: &hgManager{},
			Admin: func(ctx context.Context, a contract.Actor) error { return auth.ValidateAddonActor(ctx, db, a, true) }})
		Expect(err).NotTo(HaveOccurred())
		svc.WithFiles(owner)
		gw := &gateway.Gateway{HostGrantsOwner: svc, Revalidate: func(ctx context.Context, a contract.Actor) error { return auth.ValidateAddonActor(ctx, db, a, false) }}

		e := echo.New()
		cfg := &config.ApplicationConfig{}
		cfg.Auth.APIKeyHMACSecret = "secret"
		e.Use(auth.Middleware(db, cfg))
		g := e.Group("/api/addons", BrowserProtection())
		g.POST("/files", UploadAddonFile(gw, db))
		g.GET("/files/:fileId", GetAddonFile(gw, db))

		upload := func(mutate func(*http.Request)) (int, string) {
			var b bytes.Buffer
			mw := multipart.NewWriter(&b)
			part, _ := mw.CreateFormFile("file", "x.zip")
			_, _ = part.Write([]byte("zip bytes"))
			Expect(mw.Close()).To(Succeed())
			r := httptest.NewRequest("POST", "/api/addons/files", &b)
			r.Host = "192.0.2.10:8080"
			r.Header.Set("Content-Type", mw.FormDataContentType())
			mutate(r)
			w := httptest.NewRecorder()
			e.ServeHTTP(w, r)
			return w.Code, w.Body.String()
		}
		tab := func(session string) func(*http.Request) {
			return func(r *http.Request) {
				r.AddCookie(&http.Cookie{Name: "session", Value: session})
				r.Header.Set("Sec-Fetch-Site", "same-origin")
			}
		}
		key := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+adminKey) }

		code, _ := upload(func(*http.Request) {})
		Expect(code).To(BeElementOf(401, 403), "anonymous")
		code, _ = upload(tab(plainSession))
		Expect(code).To(BeElementOf(401, 403), "a user who is not an administrator")
		code, _ = upload(func(r *http.Request) { tab(adminSession)(r); r.Header.Set("Sec-Fetch-Site", "cross-site") })
		Expect(code).To(Equal(403), "a cross-site request that rides the session")
		code, body := upload(tab(adminSession))
		Expect(code).To(Equal(200), body)
		code, _ = upload(key)
		Expect(code).To(Equal(200), "an administrator API key may stage a file")

		// A file is read by the user that uploaded it only.
		var staged struct {
			FileID string `json:"fileId"`
		}
		Expect(json.Unmarshal([]byte(body), &staged)).To(Succeed())
		id := staged.FileID
		Expect(id).NotTo(BeEmpty())
		read := func(session string) int {
			r := httptest.NewRequest("GET", "/api/addons/files/"+id, nil)
			r.Host = "192.0.2.10:8080"
			tab(session)(r)
			w := httptest.NewRecorder()
			e.ServeHTTP(w, r)
			return w.Code
		}
		Expect(read(adminSession)).To(Equal(200))
		Expect(read(otherSession)).To(Equal(404), "another administrator")
	})
})
