//go:build auth

package auth_test

import (
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("a client key on a public route", func() {
	const secret = "test-secret"

	It("passes the routes a client needs, and no OPTIONS request that carries the key", func() {
		db := testDB()
		owner := createTestUser(db, "owner@example.com", auth.RoleAdmin, auth.ProviderLocal)
		code, _, err := auth.CreatePairingCode(db, owner.ID, "c", []string{auth.FeatureClientSelf}, nil, 0, 0, secret)
		Expect(err).ToNot(HaveOccurred())
		key, _, err := auth.RedeemPairingCode(db, code, "", secret)
		Expect(err).ToNot(HaveOccurred())

		cfg := &config.ApplicationConfig{}
		cfg.Auth.Enabled = true
		cfg.Auth.APIKeyHMACSecret = secret
		e := echo.New()
		e.Use(auth.Middleware(db, cfg))
		e.Use(auth.RequireRouteFeature(db))
		ok := func(c echo.Context) error { return c.String(http.StatusOK, "ok") }
		e.POST("/api/client/pair", ok)
		e.GET("/healthz", ok)
		// A route registered for every method, as an Any() handler is.
		e.Any("/api/settings", ok)

		do := func(method, path, token string) int {
			req := httptest.NewRequest(method, path, nil)
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			return rec.Code
		}
		Expect(do(http.MethodPost, "/api/client/pair", key)).To(Equal(http.StatusOK), "a client pairs again")
		Expect(do(http.MethodGet, "/healthz", key)).To(Equal(http.StatusOK))
		Expect(do(http.MethodGet, "/api/settings", key)).To(Equal(http.StatusForbidden))
		Expect(do(http.MethodOptions, "/api/settings", key)).To(Equal(http.StatusForbidden), "the key does not open an OPTIONS route")
		Expect(do(http.MethodOptions, "/api/settings", "")).To(Equal(http.StatusOK), "a preflight has no credential and still passes")
	})
})
