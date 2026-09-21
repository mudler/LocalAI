//go:build auth

// SPDX-License-Identifier: MIT

package auth_test

import (
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

var _ = Describe("CSRF authentication exemptions", func() {
	const path = "/v1/chat/completions"
	check := func(db *gorm.DB, cfg *config.ApplicationConfig, header, value, cookieName, cookieValue, site string, status int) {
		app := echo.New()
		app.Use(auth.Middleware(db, cfg))
		app.Use(auth.CSRFMiddleware())
		reached := false
		app.POST(path, func(c echo.Context) error {
			reached = true
			return c.NoContent(http.StatusOK)
		})
		req := httptest.NewRequest(http.MethodPost, path, nil)
		if header != "" {
			req.Header.Set(header, value)
		}
		if site != "" {
			req.Header.Set("Sec-Fetch-Site", site)
		}
		if cookieName != "" {
			req.AddCookie(&http.Cookie{Name: cookieName, Value: cookieValue})
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(status), rec.Body.String())
		Expect(reached).To(Equal(status == http.StatusOK))
	}

	for _, header := range []string{"Authorization", "x-api-key", "xi-api-key"} {
		It("rejects arbitrary "+header+" when auth is disabled", func() {
			check(nil, &config.ApplicationConfig{}, header, "Bearer bogus", "", "", "cross-site", http.StatusBadRequest)
		})
		It("allows a validated legacy "+header, func() {
			value := "valid-key"
			if header == "Authorization" {
				value = "Bearer " + value
			}
			check(nil, &config.ApplicationConfig{ApiKeys: []string{"valid-key"}}, header, value, "", "", "cross-site", http.StatusOK)
		})
		It("rejects invalid "+header+" on an auth-exempt path", func() {
			check(nil, &config.ApplicationConfig{ApiKeys: []string{"valid-key"}, PathWithoutAuth: []string{path}}, header, "bogus", "", "", "cross-site", http.StatusBadRequest)
		})
		It("allows a validated named key via "+header, func() {
			db := testDB()
			user := createTestUser(db, "csrf@example.com", auth.RoleUser, auth.ProviderGitHub)
			key, _, err := auth.CreateAPIKey(db, user.ID, "csrf", auth.RoleUser, "", nil)
			Expect(err).ToNot(HaveOccurred())
			value := key
			if header == "Authorization" {
				value = "Bearer " + value
			}
			check(db, &config.ApplicationConfig{}, header, value, "", "", "cross-site", http.StatusOK)
			check(db, &config.ApplicationConfig{}, header, "bogus", "token", key, "cross-site", http.StatusBadRequest)
		})
		It("does not exempt session cookies with arbitrary "+header, func() {
			db := testDB()
			user := createTestUser(db, "csrf@example.com", auth.RoleUser, auth.ProviderGitHub)
			token := createTestSession(db, user.ID)
			check(db, &config.ApplicationConfig{}, header, "bogus", "session", token, "cross-site", http.StatusBadRequest)
		})
	}
	It("allows a session authenticated via Bearer", func() {
		db := testDB()
		user := createTestUser(db, "csrf@example.com", auth.RoleUser, auth.ProviderGitHub)
		token := createTestSession(db, user.ID)
		check(db, &config.ApplicationConfig{}, "Authorization", "Bearer "+token, "", "", "cross-site", http.StatusOK)
	})
	It("does not exempt legacy token cookies", func() {
		check(nil, &config.ApplicationConfig{ApiKeys: []string{"valid-key"}}, "", "", "token", "valid-key", "cross-site", http.StatusBadRequest)
	})
	for _, site := range []string{"same-origin", "same-site", ""} {
		It("preserves requests with Sec-Fetch-Site="+site, func() {
			check(nil, &config.ApplicationConfig{}, "", "", "", "", site, http.StatusOK)
		})
	}
})
