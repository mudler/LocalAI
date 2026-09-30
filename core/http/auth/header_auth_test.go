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

var _ = Describe("Validated header authentication", func() {
	DescribeTable("distinguishes supplied credentials from cookie authentication", func(mode string, expected bool) {
		db := testDB()
		user := createTestUser(db, "header@example.test", auth.RoleUser, auth.ProviderLocal)
		session := createTestSession(db, user.ID)
		key, _, err := auth.CreateAPIKey(db, user.ID, "header-test", auth.RoleUser, "", nil)
		Expect(err).NotTo(HaveOccurred())
		cfg := config.NewApplicationConfig()
		cfg.Auth.Enabled = true
		e := echo.New()
		e.Use(auth.Middleware(db, cfg))
		e.GET("/api/motion/check", func(c echo.Context) error {
			Expect(auth.HeaderAuthenticated(c)).To(Equal(expected))
			return c.NoContent(204)
		})
		r := httptest.NewRequest("GET", "/api/motion/check", nil)
		switch mode {
		case "session bearer":
			r.Header.Set("Authorization", "Bearer "+session)
		case "key bearer":
			r.Header.Set("Authorization", "Bearer "+key)
		case "key header":
			r.Header.Set("x-api-key", key)
		case "session cookie":
			r.AddCookie(&http.Cookie{Name: "session", Value: session})
		case "key cookie":
			r.AddCookie(&http.Cookie{Name: "token", Value: key})
		case "cookie with forged header":
			r.AddCookie(&http.Cookie{Name: "session", Value: session})
			r.Header.Set("Authorization", "Bearer invalid")
		case "key cookie with forged header":
			r.AddCookie(&http.Cookie{Name: "token", Value: key})
			r.Header.Set("Authorization", "Bearer invalid")
		}
		res := httptest.NewRecorder()
		e.ServeHTTP(res, r)
		Expect(res.Code).To(Equal(204))
	},
		Entry("session bearer", "session bearer", true),
		Entry("API key bearer", "key bearer", true),
		Entry("API key header", "key header", true),
		Entry("session cookie", "session cookie", false),
		Entry("API key cookie", "key cookie", false),
		Entry("cookie with forged header", "cookie with forged header", false),
		Entry("key cookie with forged header", "key cookie with forged header", false),
	)
})
