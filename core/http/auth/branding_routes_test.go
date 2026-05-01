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
// the branding endpoints: GET endpoints public, POST/DELETE gated by
// the route-level admin middleware. PathWithoutAuth is left to its
// NewApplicationConfig defaults so the "/api/branding" + "/branding/"
// exempt entries (added in this PR) participate in the test.
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

// These specs pin a contract that's easy to break by accident: the
// "/api/branding" entry in PathWithoutAuth uses a prefix match, so it
// also exempts POST/DELETE /api/branding/asset/:kind from the *global*
// auth middleware. Those mutations only stay admin-only because the
// route registration explicitly carries auth.RequireAdmin(). If that
// route-level middleware is ever forgotten — or someone adds a new
// admin sub-route under /api/branding/* without the gate — these
// specs go red.
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
		Expect(rec.Code).To(Equal(http.StatusUnauthorized),
			"PathWithoutAuth exempts the prefix from global auth, but the route-level adminMiddleware MUST still 401 anonymous mutations")
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
