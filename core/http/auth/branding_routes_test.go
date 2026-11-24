//go:build auth

package auth_test

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// newBrandingTestApp mirrors how core/http/routes/ui_api.go registers
// the branding endpoints: the built-in registry allows only GET reads,
// while POST/DELETE mutations remain gated by global and admin middleware.
func newBrandingTestApp(db *gorm.DB, appConfig *config.ApplicationConfig) *echo.Echo {
	e := echo.New()
	e.Use(auth.Middleware(db, appConfig))

	adminMw := auth.RequireAdmin()

	// Public read + asset server.
	e.GET("/api/branding", ok)
	e.GET("/branding/asset/:kind", ok)

	// Admin-only mutations.
	e.POST("/api/branding/asset/:kind", ok, adminMw)
	e.DELETE("/api/branding/asset/:kind", ok, adminMw)

	return e
}

// These specs pin method-aware access: anonymous branding reads are public,
// anonymous mutations fail global auth, and authenticated non-admin mutations
// fail the route-level admin check.
var _ = Describe("Branding route admin gating", func() {
	var (
		db        *gorm.DB
		appConfig *config.ApplicationConfig
	)

	BeforeEach(func() {
		db = testDB()
		appConfig = config.NewApplicationConfig()
	})

	It("allows anonymous GET /api/branding (login screen reads it pre-auth)", func() {
		app := newBrandingTestApp(db, appConfig)
		rec := doRequest(app, http.MethodGet, "/api/branding")
		Expect(rec.Code).To(Equal(http.StatusOK))
	})

	It("allows anonymous GET /branding/asset/:kind (logo served pre-auth)", func() {
		app := newBrandingTestApp(db, appConfig)
		rec := doRequest(app, http.MethodGet, "/branding/asset/logo")
		Expect(rec.Code).To(Equal(http.StatusOK))
	})

	It("returns 401 for anonymous POST /api/branding/asset/:kind", func() {
		app := newBrandingTestApp(db, appConfig)
		rec := doRequest(app, http.MethodPost, "/api/branding/asset/logo")
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
	})

	It("returns 401 for anonymous DELETE /api/branding/asset/:kind", func() {
		app := newBrandingTestApp(db, appConfig)
		rec := doRequest(app, http.MethodDelete, "/api/branding/asset/logo")
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
	})

	It("returns 403 for non-admin POST /api/branding/asset/:kind", func() {
		user := createTestUser(db, "user@example.com", auth.RoleUser, auth.ProviderGitHub)
		sessionID := createTestSession(db, user.ID)
		app := newBrandingTestApp(db, appConfig)

		rec := doRequest(app, http.MethodPost, "/api/branding/asset/logo", withSessionCookie(sessionID))
		Expect(rec.Code).To(Equal(http.StatusForbidden))
	})

	It("allows admin POST /api/branding/asset/:kind", func() {
		admin := createTestUser(db, "admin@example.com", auth.RoleAdmin, auth.ProviderGitHub)
		sessionID := createTestSession(db, admin.ID)
		app := newBrandingTestApp(db, appConfig)

		rec := doRequest(app, http.MethodPost, "/api/branding/asset/logo", withSessionCookie(sessionID))
		Expect(rec.Code).To(Equal(http.StatusOK))
	})

	It("allows admin DELETE /api/branding/asset/:kind", func() {
		admin := createTestUser(db, "admin@example.com", auth.RoleAdmin, auth.ProviderGitHub)
		sessionID := createTestSession(db, admin.ID)
		app := newBrandingTestApp(db, appConfig)

		rec := doRequest(app, http.MethodDelete, "/api/branding/asset/logo", withSessionCookie(sessionID))
		Expect(rec.Code).To(Equal(http.StatusOK))
	})
})
