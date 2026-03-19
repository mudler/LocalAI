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

var _ = Describe("Auth Middleware", func() {

	Context("auth disabled, no API keys", func() {
		var app *echo.Echo

		BeforeEach(func() {
			appConfig := config.NewApplicationConfig()
			app = newAuthTestApp(nil, appConfig)
		})

		It("passes through all requests", func() {
			rec := doRequest(app, http.MethodGet, "/v1/models")
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("passes through POST requests", func() {
			rec := doRequest(app, http.MethodPost, "/v1/chat/completions")
			Expect(rec.Code).To(Equal(http.StatusOK))
		})
	})

	Context("auth disabled, API keys configured", func() {
		var app *echo.Echo
		const validKey = "sk-test-key-123"

		BeforeEach(func() {
			appConfig := config.NewApplicationConfig()
			appConfig.ApiKeys = []string{validKey}
			app = newAuthTestApp(nil, appConfig)
		})

		It("returns 401 for request without key", func() {
			rec := doRequest(app, http.MethodGet, "/v1/models")
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})

		It("passes with valid Bearer token", func() {
			rec := doRequest(app, http.MethodGet, "/v1/models", withBearerToken(validKey))
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("passes with valid x-api-key header", func() {
			rec := doRequest(app, http.MethodGet, "/v1/models", withXApiKey(validKey))
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("passes with valid token cookie", func() {
			rec := doRequest(app, http.MethodGet, "/v1/models", withTokenCookie(validKey))
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("returns 401 for invalid key", func() {
			rec := doRequest(app, http.MethodGet, "/v1/models", withBearerToken("wrong-key"))
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})
	})

	Context("auth enabled with database", func() {
		var (
			db        *gorm.DB
			app       *echo.Echo
			appConfig *config.ApplicationConfig
			user      *auth.User
		)

		BeforeEach(func() {
			db = testDB()
			appConfig = config.NewApplicationConfig()
			app = newAuthTestApp(db, appConfig)
			user = createTestUser(db, "user@example.com", auth.RoleUser, auth.ProviderGitHub)
		})

		It("allows requests with valid session cookie", func() {
			sessionID := createTestSession(db, user.ID)
			rec := doRequest(app, http.MethodGet, "/v1/models", withSessionCookie(sessionID))
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("allows requests with valid session as Bearer token", func() {
			sessionID := createTestSession(db, user.ID)
			rec := doRequest(app, http.MethodGet, "/v1/models", withBearerToken(sessionID))
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("allows requests with valid user API key as Bearer token", func() {
			plaintext, _, err := auth.CreateAPIKey(db, user.ID, "test", auth.RoleUser)
			Expect(err).ToNot(HaveOccurred())

			rec := doRequest(app, http.MethodGet, "/v1/models", withBearerToken(plaintext))
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("allows requests with legacy API_KEY as admin bypass", func() {
			appConfig.ApiKeys = []string{"legacy-key-123"}
			app = newAuthTestApp(db, appConfig)

			rec := doRequest(app, http.MethodGet, "/v1/models", withBearerToken("legacy-key-123"))
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("returns 401 for expired session", func() {
			sessionID := createTestSession(db, user.ID)
			// Manually expire
			db.Model(&auth.Session{}).Where("id = ?", sessionID).
				Update("expires_at", "2020-01-01")

			rec := doRequest(app, http.MethodGet, "/v1/models", withSessionCookie(sessionID))
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})

		It("returns 401 for invalid session ID", func() {
			rec := doRequest(app, http.MethodGet, "/v1/models", withSessionCookie("invalid-session-id"))
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})

		It("returns 401 for revoked API key", func() {
			plaintext, record, err := auth.CreateAPIKey(db, user.ID, "to revoke", auth.RoleUser)
			Expect(err).ToNot(HaveOccurred())

			err = auth.RevokeAPIKey(db, record.ID, user.ID)
			Expect(err).ToNot(HaveOccurred())

			rec := doRequest(app, http.MethodGet, "/v1/models", withBearerToken(plaintext))
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})

		It("skips auth for /api/auth/* paths", func() {
			rec := doRequest(app, http.MethodGet, "/api/auth/status")
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("skips auth for PathWithoutAuth paths", func() {
			rec := doRequest(app, http.MethodGet, "/healthz")
			// healthz is not registered in our test app, so it'll be 404/405 but NOT 401
			Expect(rec.Code).ToNot(Equal(http.StatusUnauthorized))
		})

		It("returns 401 for unauthenticated API requests", func() {
			rec := doRequest(app, http.MethodGet, "/v1/models")
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})

		It("allows unauthenticated access to non-API paths when no legacy keys", func() {
			rec := doRequest(app, http.MethodGet, "/app")
			Expect(rec.Code).To(Equal(http.StatusOK))
		})
	})

	Describe("RequireAdmin", func() {
		var (
			db        *gorm.DB
			appConfig *config.ApplicationConfig
		)

		BeforeEach(func() {
			db = testDB()
			appConfig = config.NewApplicationConfig()
		})

		It("passes for admin user", func() {
			admin := createTestUser(db, "admin@example.com", auth.RoleAdmin, auth.ProviderGitHub)
			sessionID := createTestSession(db, admin.ID)
			app := newAdminTestApp(db, appConfig)

			rec := doRequest(app, http.MethodPost, "/api/settings", withSessionCookie(sessionID))
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("returns 403 for user role", func() {
			user := createTestUser(db, "user@example.com", auth.RoleUser, auth.ProviderGitHub)
			sessionID := createTestSession(db, user.ID)
			app := newAdminTestApp(db, appConfig)

			rec := doRequest(app, http.MethodPost, "/api/settings", withSessionCookie(sessionID))
			Expect(rec.Code).To(Equal(http.StatusForbidden))
		})

		It("returns 401 when no user in context", func() {
			app := newAdminTestApp(db, appConfig)

			rec := doRequest(app, http.MethodPost, "/api/settings")
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})

		It("allows admin to access model management", func() {
			admin := createTestUser(db, "admin@example.com", auth.RoleAdmin, auth.ProviderGitHub)
			sessionID := createTestSession(db, admin.ID)
			app := newAdminTestApp(db, appConfig)

			rec := doRequest(app, http.MethodPost, "/models/apply", withSessionCookie(sessionID))
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("blocks user from model management", func() {
			user := createTestUser(db, "user@example.com", auth.RoleUser, auth.ProviderGitHub)
			sessionID := createTestSession(db, user.ID)
			app := newAdminTestApp(db, appConfig)

			rec := doRequest(app, http.MethodPost, "/models/apply", withSessionCookie(sessionID))
			Expect(rec.Code).To(Equal(http.StatusForbidden))
		})

		It("allows user to access regular inference endpoints", func() {
			user := createTestUser(db, "user@example.com", auth.RoleUser, auth.ProviderGitHub)
			sessionID := createTestSession(db, user.ID)
			app := newAdminTestApp(db, appConfig)

			rec := doRequest(app, http.MethodPost, "/v1/chat/completions", withSessionCookie(sessionID))
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("allows legacy API key (admin bypass) on admin routes", func() {
			appConfig.ApiKeys = []string{"admin-key"}
			app := newAdminTestApp(db, appConfig)

			rec := doRequest(app, http.MethodPost, "/api/settings", withBearerToken("admin-key"))
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("allows admin to access trace endpoints", func() {
			admin := createTestUser(db, "admin2@example.com", auth.RoleAdmin, auth.ProviderGitHub)
			sessionID := createTestSession(db, admin.ID)
			app := newAdminTestApp(db, appConfig)

			rec := doRequest(app, http.MethodGet, "/api/traces", withSessionCookie(sessionID))
			Expect(rec.Code).To(Equal(http.StatusOK))

			rec = doRequest(app, http.MethodGet, "/api/backend-logs", withSessionCookie(sessionID))
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("blocks non-admin from trace endpoints", func() {
			user := createTestUser(db, "user2@example.com", auth.RoleUser, auth.ProviderGitHub)
			sessionID := createTestSession(db, user.ID)
			app := newAdminTestApp(db, appConfig)

			rec := doRequest(app, http.MethodGet, "/api/traces", withSessionCookie(sessionID))
			Expect(rec.Code).To(Equal(http.StatusForbidden))

			rec = doRequest(app, http.MethodGet, "/api/backend-logs", withSessionCookie(sessionID))
			Expect(rec.Code).To(Equal(http.StatusForbidden))
		})

		It("allows admin to access agent job endpoints", func() {
			admin := createTestUser(db, "admin3@example.com", auth.RoleAdmin, auth.ProviderGitHub)
			sessionID := createTestSession(db, admin.ID)
			app := newAdminTestApp(db, appConfig)

			rec := doRequest(app, http.MethodGet, "/api/agent/tasks", withSessionCookie(sessionID))
			Expect(rec.Code).To(Equal(http.StatusOK))

			rec = doRequest(app, http.MethodGet, "/api/agent/jobs", withSessionCookie(sessionID))
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("blocks non-admin from agent job endpoints", func() {
			user := createTestUser(db, "user3@example.com", auth.RoleUser, auth.ProviderGitHub)
			sessionID := createTestSession(db, user.ID)
			app := newAdminTestApp(db, appConfig)

			rec := doRequest(app, http.MethodGet, "/api/agent/tasks", withSessionCookie(sessionID))
			Expect(rec.Code).To(Equal(http.StatusForbidden))

			rec = doRequest(app, http.MethodGet, "/api/agent/jobs", withSessionCookie(sessionID))
			Expect(rec.Code).To(Equal(http.StatusForbidden))
		})

		It("blocks non-admin from system/management endpoints", func() {
			user := createTestUser(db, "user4@example.com", auth.RoleUser, auth.ProviderGitHub)
			sessionID := createTestSession(db, user.ID)
			app := newAdminTestApp(db, appConfig)

			for _, path := range []string{"/api/operations", "/api/models", "/api/backends", "/api/resources", "/api/p2p/workers", "/system", "/backend/monitor"} {
				rec := doRequest(app, http.MethodGet, path, withSessionCookie(sessionID))
				Expect(rec.Code).To(Equal(http.StatusForbidden), "expected 403 for path: "+path)
			}
		})

		It("allows admin to access system/management endpoints", func() {
			admin := createTestUser(db, "admin4@example.com", auth.RoleAdmin, auth.ProviderGitHub)
			sessionID := createTestSession(db, admin.ID)
			app := newAdminTestApp(db, appConfig)

			for _, path := range []string{"/api/operations", "/api/models", "/api/backends", "/api/resources", "/api/p2p/workers", "/system", "/backend/monitor"} {
				rec := doRequest(app, http.MethodGet, path, withSessionCookie(sessionID))
				Expect(rec.Code).To(Equal(http.StatusOK), "expected 200 for path: "+path)
			}
		})
	})
})
