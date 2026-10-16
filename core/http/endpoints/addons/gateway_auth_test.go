//go:build auth

// SPDX-License-Identifier: MIT
package addons

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These specs open the SQLite auth database, which exists only in a build
// with the auth tag.
var _ = Describe("authenticated addon HTTP", func() {
	It("checks browser CSRF feature and admin access with real stored keys", func() {
		db, err := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(err).NotTo(HaveOccurred())
		u := auth.User{ID: "admin", Role: auth.RoleAdmin, Status: auth.StatusActive}
		Expect(db.Create(&u).Error).To(Succeed())
		token, _, err := auth.CreateAPIKey(db, u.ID, "test", auth.RoleAdmin, "secret", nil)
		Expect(err).NotTo(HaveOccurred())
		e := echo.New()
		cfg := &config.ApplicationConfig{}
		cfg.Auth.APIKeyHMACSecret = "secret"
		e.Use(auth.Middleware(db, cfg))
		e.POST("/api/addons/dispatch", Dispatch(nil, db), BrowserProtection())
		call := func(cookie bool, site string) int {
			r := httptest.NewRequest("POST", "/api/addons/dispatch", bytes.NewBufferString(`{"addonId":"sample","operationId":"op","generation":1,"action":"enable","arguments":{}}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Sec-Fetch-Site", site)
			if cookie {
				s, err := auth.CreateSession(db, u.ID, "secret")
				Expect(err).NotTo(HaveOccurred())
				r.AddCookie(&http.Cookie{Name: "session", Value: s})
			} else {
				r.Header.Set("Authorization", "Bearer "+token)
			}
			w := httptest.NewRecorder()
			e.ServeHTTP(w, r)
			return w.Code
		}
		Expect(call(false, "cross-site")).To(Equal(503))
		Expect(call(true, "cross-site")).To(Equal(403))
		Expect(call(true, "")).To(Equal(403))
		Expect(call(true, "same-origin")).To(Equal(503))
		Expect(db.Model(&u).Update("role", auth.RoleUser).Error).To(Succeed())
		Expect(call(false, "")).To(Equal(401))
	})
})
