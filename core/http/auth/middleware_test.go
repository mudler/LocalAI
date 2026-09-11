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
	protectedRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/mcp/chat/completions"},
		{http.MethodPost, "/mcp/v1/chat/completions"},
		{http.MethodPost, "/moderations"},
		{http.MethodGet, "/models"},
		{http.MethodGet, "/backends"},
		{http.MethodGet, "/import-model"},
		{http.MethodGet, "/version"},
		{http.MethodGet, "/generated-audio/result.wav"},
		{http.MethodGet, "/generated-images/result.png"},
		{http.MethodGet, "/generated-videos/result.mp4"},
		{http.MethodGet, "/generated-3d/result.glb"},
		{http.MethodGet, "/newly-registered"},
	}

	publicRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/instructions"},
		{http.MethodGet, "/api/instructions/chat"},
		{http.MethodGet, "/swagger"},
		{http.MethodGet, "/swagger/index.html"},
		{http.MethodGet, "/.well-known/localai.json"},
		{http.MethodGet, "/healthz"},
		{http.MethodGet, "/readyz"},
		{http.MethodGet, "/api/auth/status"},
		{http.MethodPost, "/api/auth/token-login"},
		{http.MethodPost, "/api/auth/register"},
		{http.MethodPost, "/api/auth/login"},
		{http.MethodGet, "/api/auth/github/login"},
		{http.MethodGet, "/api/auth/github/callback"},
		{http.MethodGet, "/api/auth/oidc/login"},
		{http.MethodGet, "/api/auth/oidc/callback"},
		{http.MethodOptions, "/api/auth/resource"},
		{http.MethodGet, "/"},
		{http.MethodHead, "/"},
		{http.MethodGet, "/app"},
		{http.MethodGet, "/app/settings"},
		{http.MethodGet, "/browse"},
		{http.MethodGet, "/browse/models"},
		{http.MethodGet, "/login"},
		{http.MethodGet, "/invite/token"},
		{http.MethodGet, "/explorer"},
		{http.MethodGet, "/favicon.svg"},
		{http.MethodGet, "/assets/app.js"},
		{http.MethodGet, "/locales/en/common.json"},
		{http.MethodGet, "/static/app.js"},
		{http.MethodGet, "/api/branding"},
		{http.MethodGet, "/branding/asset/logo"},
	}

	privatePublicRouteLookalikes := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/instructions"},
		{http.MethodGet, "/api/instructions-private"},
		{http.MethodPost, "/swagger"},
		{http.MethodGet, "/swagger-private/index.html"},
		{http.MethodPost, "/healthz"},
		{http.MethodPost, "/api/auth/status"},
		{http.MethodGet, "/api/auth/token-login"},
		{http.MethodGet, "/api/auth/register"},
		{http.MethodGet, "/api/auth/private"},
		{http.MethodOptions, "/api/auth-private/resource"},
		{http.MethodPost, "/app/settings"},
		{http.MethodGet, "/app-private"},
		{http.MethodPost, "/browse/models"},
		{http.MethodGet, "/browse-private"},
		{http.MethodPost, "/invite/token"},
		{http.MethodGet, "/invite-private/token"},
		{http.MethodPost, "/assets/app.js"},
		{http.MethodGet, "/assets-private/app.js"},
		{http.MethodGet, "/locales-private/en/common.json"},
		{http.MethodGet, "/static-private/app.js"},
		{http.MethodPost, "/api/branding"},
		{http.MethodPost, "/branding/asset/logo"},
		{http.MethodGet, "/branding/asset-private/logo"},
		{http.MethodGet, "/api/node-private/models"},
	}

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

		It("passes every route regardless of its classification", func() {
			appConfig := config.NewApplicationConfig()
			app = newCatchAllAuthTestApp(nil, appConfig)
			for _, route := range append(append(protectedRoutes, publicRoutes...), privatePublicRouteLookalikes...) {
				rec := doRequest(app, route.method, route.path)
				Expect(rec.Code).To(Equal(http.StatusOK), route.method+" "+route.path)
			}
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

		It("denies every unapproved route by default", func() {
			appConfig := config.NewApplicationConfig()
			appConfig.ApiKeys = []string{validKey}
			app = newCatchAllAuthTestApp(nil, appConfig)
			for _, route := range protectedRoutes {
				rec := doRequest(app, route.method, route.path)
				Expect(rec.Code).To(Equal(http.StatusUnauthorized), route.method+" "+route.path)
			}
		})

		It("accepts a legacy key and rejects an invalid key on a newly registered route", func() {
			appConfig := config.NewApplicationConfig()
			appConfig.ApiKeys = []string{validKey}
			app = newCatchAllAuthTestApp(nil, appConfig)

			valid := doRequest(app, http.MethodGet, "/newly-registered", withBearerToken(validKey))
			Expect(valid.Code).To(Equal(http.StatusOK))

			invalid := doRequest(app, http.MethodGet, "/newly-registered", withBearerToken("wrong-key"))
			Expect(invalid.Code).To(Equal(http.StatusUnauthorized))
		})

		It("preserves the configured legacy GET regex override", func() {
			appConfig := config.NewApplicationConfig(
				config.WithDisableApiKeyRequirementForHttpGet(true),
				config.WithHttpGetExemptedEndpoints([]string{"^/legacy-public$"}),
			)
			appConfig.ApiKeys = []string{validKey}
			app = echo.New()
			app.Use(auth.Middleware(nil, appConfig))
			app.GET("/legacy-public", ok)

			rec := doRequest(app, http.MethodGet, "/legacy-public")
			Expect(rec.Code).To(Equal(http.StatusOK))
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

		It("allows authenticated users to call moderation by default", func() {
			sessionID := createTestSession(db, user.ID)
			rec := doRequest(app, http.MethodPost, "/v1/moderations", withSessionCookie(sessionID))
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("blocks moderation when the user's feature is disabled", func() {
			Expect(auth.UpdateUserPermissions(db, user.ID, auth.PermissionMap{auth.FeatureModeration: false})).To(Succeed())
			sessionID := createTestSession(db, user.ID)
			rec := doRequest(app, http.MethodPost, "/v1/moderations", withSessionCookie(sessionID))
			Expect(rec.Code).To(Equal(http.StatusForbidden))
		})

		It("allows requests with valid session as Bearer token", func() {
			sessionID := createTestSession(db, user.ID)
			rec := doRequest(app, http.MethodGet, "/v1/models", withBearerToken(sessionID))
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("allows requests with valid user API key as Bearer token", func() {
			plaintext, _, err := auth.CreateAPIKey(db, user.ID, "test", auth.RoleUser, "", nil)
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
			// Manually expire (session ID in DB is the hash)
			hash := auth.HashAPIKey(sessionID, "")
			db.Model(&auth.Session{}).Where("id = ?", hash).
				Update("expires_at", "2020-01-01")

			rec := doRequest(app, http.MethodGet, "/v1/models", withSessionCookie(sessionID))
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})

		It("returns 401 for invalid session ID", func() {
			rec := doRequest(app, http.MethodGet, "/v1/models", withSessionCookie("invalid-session-id"))
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})

		It("returns 401 for revoked API key", func() {
			plaintext, record, err := auth.CreateAPIKey(db, user.ID, "to revoke", auth.RoleUser, "", nil)
			Expect(err).ToNot(HaveOccurred())

			err = auth.RevokeAPIKey(db, record.ID, user.ID)
			Expect(err).ToNot(HaveOccurred())

			rec := doRequest(app, http.MethodGet, "/v1/models", withBearerToken(plaintext))
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})

		It("allows the public auth status route", func() {
			rec := doRequest(app, http.MethodGet, "/api/auth/status")
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("keeps built-in public routes out of PathWithoutAuth", func() {
			Expect(appConfig.PathWithoutAuth).To(BeEmpty())
		})

		It("returns 401 for unauthenticated API requests", func() {
			rec := doRequest(app, http.MethodGet, "/v1/models")
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})

		It("returns 401 for unauthenticated moderation requests", func() {
			rec := doRequest(app, http.MethodPost, "/v1/moderations")
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})

		It("returns 401 for unauthenticated 3D generation requests", func() {
			rec := doRequest(app, http.MethodPost, "/3d/generations")
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})

		It("returns 401 for unauthenticated 3D remesh requests", func() {
			rec := doRequest(app, http.MethodPost, "/3d/remesh")
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})

		It("allows the public SPA route", func() {
			rec := doRequest(app, http.MethodGet, "/app")
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("denies every unapproved route by default", func() {
			app = newCatchAllAuthTestApp(db, appConfig)
			for _, route := range protectedRoutes {
				rec := doRequest(app, route.method, route.path)
				Expect(rec.Code).To(Equal(http.StatusUnauthorized), route.method+" "+route.path)
			}
		})

		It("accepts sessions and user API keys on a newly registered route", func() {
			app = newCatchAllAuthTestApp(db, appConfig)
			sessionID := createTestSession(db, user.ID)
			sessionRec := doRequest(app, http.MethodGet, "/newly-registered", withSessionCookie(sessionID))
			Expect(sessionRec.Code).To(Equal(http.StatusOK))

			plaintext, _, err := auth.CreateAPIKey(db, user.ID, "default-deny", auth.RoleUser, "", nil)
			Expect(err).ToNot(HaveOccurred())
			keyRec := doRequest(app, http.MethodGet, "/newly-registered", withBearerToken(plaintext))
			Expect(keyRec.Code).To(Equal(http.StatusOK))

			invalidRec := doRequest(app, http.MethodGet, "/newly-registered", withBearerToken("wrong-key"))
			Expect(invalidRec.Code).To(Equal(http.StatusUnauthorized))
		})

		It("allows every approved public method and path anonymously", func() {
			app = newCatchAllAuthTestApp(db, appConfig)
			for _, route := range publicRoutes {
				rec := doRequest(app, route.method, route.path)
				Expect(rec.Code).To(Equal(http.StatusOK), route.method+" "+route.path)
			}
		})

		It("keeps wrong methods and near-prefixes private", func() {
			app = newCatchAllAuthTestApp(db, appConfig)
			for _, route := range privatePublicRouteLookalikes {
				rec := doRequest(app, route.method, route.path)
				Expect(rec.Code).To(Equal(http.StatusUnauthorized), route.method+" "+route.path)
			}
		})

		It("authenticates before a public handler runs", func() {
			sessionID := createTestSession(db, user.ID)
			e := echo.New()
			e.Use(auth.Middleware(db, appConfig))
			e.GET("/api/auth/status", func(c echo.Context) error {
				Expect(auth.GetUser(c)).ToNot(BeNil())
				Expect(auth.GetUser(c).ID).To(Equal(user.ID))
				return c.NoContent(http.StatusOK)
			})

			rec := doRequest(e, http.MethodGet, "/api/auth/status", withSessionCookie(sessionID))
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("preserves caller-supplied PathWithoutAuth prefixes", func() {
			appConfig.PathWithoutAuth = []string{"/caller-public/"}
			app = newCatchAllAuthTestApp(db, appConfig)

			rec := doRequest(app, http.MethodPost, "/caller-public/resource")
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("preserves the unauthorized response shape and challenge header", func() {
			app = newCatchAllAuthTestApp(db, appConfig)

			rec := doRequest(app, http.MethodGet, "/newly-registered")
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
			Expect(rec.Header().Get("WWW-Authenticate")).To(Equal("Bearer"))
			Expect(rec.Body.String()).To(ContainSubstring("An authentication key is required"))
			Expect(rec.Body.String()).To(ContainSubstring("invalid_request_error"))
		})

		It("preserves opaque unauthorized responses", func() {
			appConfig.OpaqueErrors = true
			app = newCatchAllAuthTestApp(db, appConfig)

			rec := doRequest(app, http.MethodGet, "/newly-registered")
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
			Expect(rec.Header().Get("WWW-Authenticate")).To(Equal("Bearer"))
			Expect(rec.Body.String()).To(BeEmpty())
		})

		It("delegates the full node self-service group to registration-token auth", func() {
			const registrationToken = "node-registration-secret"
			app = newNodeSelfServiceTestApp(db, appConfig, registrationToken)
			authHeader := withBearerToken(registrationToken)

			register := doRequestWithBody(
				app,
				http.MethodPost,
				"/api/node/register",
				`{"name":"worker-1","address":"127.0.0.1:50051","token":"node-registration-secret"}`,
				authHeader,
			)
			Expect(register.Code).To(Equal(http.StatusCreated))

			for _, route := range []struct {
				method string
				path   string
				body   string
			}{
				{http.MethodPost, "/api/node/missing/heartbeat", `{}`},
				{http.MethodPost, "/api/node/missing/drain", ""},
				{http.MethodPost, "/api/node/missing/resume", ""},
				{http.MethodPost, "/api/node/missing/deregister", ""},
				{http.MethodGet, "/api/node/missing/models", ""},
				{http.MethodDelete, "/api/node/missing", ""},
			} {
				rec := doRequestWithBody(app, route.method, route.path, route.body, authHeader)
				Expect(rec.Code).ToNot(Equal(http.StatusUnauthorized), route.method+" "+route.path)
			}
		})

		It("lets downstream node auth reject missing and invalid registration tokens", func() {
			const registrationToken = "node-registration-secret"
			app = newNodeSelfServiceTestApp(db, appConfig, registrationToken)

			missing := doRequest(app, http.MethodGet, "/api/node/missing/models")
			Expect(missing.Code).To(Equal(http.StatusUnauthorized))
			Expect(missing.Body.String()).To(ContainSubstring("missing or invalid Authorization header"))

			invalid := doRequest(
				app,
				http.MethodGet,
				"/api/node/missing/models",
				withBearerToken("wrong-registration-token"),
			)
			Expect(invalid.Code).To(Equal(http.StatusUnauthorized))
			Expect(invalid.Body.String()).To(ContainSubstring("invalid registration token"))
		})

		It("enforces disabled MCP and moderation permissions on every alias", func() {
			Expect(auth.UpdateUserPermissions(db, user.ID, auth.PermissionMap{
				auth.FeatureMCP:        false,
				auth.FeatureModeration: false,
			})).To(Succeed())
			sessionID := createTestSession(db, user.ID)

			for _, route := range []struct {
				method string
				path   string
			}{
				{http.MethodPost, "/v1/mcp/chat/completions"},
				{http.MethodPost, "/mcp/v1/chat/completions"},
				{http.MethodPost, "/mcp/chat/completions"},
				{http.MethodPost, "/v1/moderations"},
				{http.MethodPost, "/moderations"},
			} {
				rec := doRequest(app, route.method, route.path, withSessionCookie(sessionID))
				Expect(rec.Code).To(Equal(http.StatusForbidden), route.method+" "+route.path)
			}
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

	Describe("auth context plumbing for usage source", func() {
		// probeApp builds a minimal echo app with the auth middleware and a single
		// "/probe" route that captures the user, source, and apikey from context.
		type probe struct {
			user   *auth.User
			source string
			key    *auth.UserAPIKey
		}
		probeApp := func(db *gorm.DB, appConfig *config.ApplicationConfig, p *probe) *echo.Echo {
			e := echo.New()
			e.Use(auth.Middleware(db, appConfig))
			e.GET("/probe", func(c echo.Context) error {
				p.user = auth.GetUser(c)
				p.source = auth.GetSource(c)
				p.key = auth.GetAPIKey(c)
				return c.NoContent(http.StatusOK)
			})
			return e
		}

		It("session cookie sets source=web, apikey=nil", func() {
			db := testDB()
			appConfig := config.NewApplicationConfig()
			user := createTestUser(db, "alice@example.com", auth.RoleUser, auth.ProviderLocal)
			token := createTestSession(db, user.ID)

			var p probe
			app := probeApp(db, appConfig, &p)
			rec := doRequest(app, http.MethodGet, "/probe", withSessionCookie(token))

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(p.user).ToNot(BeNil())
			Expect(p.user.ID).To(Equal(user.ID))
			Expect(p.source).To(Equal(auth.UsageSourceWeb))
			Expect(p.key).To(BeNil())
		})

		It("Bearer session token sets source=web, apikey=nil", func() {
			db := testDB()
			appConfig := config.NewApplicationConfig()
			user := createTestUser(db, "alice@example.com", auth.RoleUser, auth.ProviderLocal)
			token := createTestSession(db, user.ID)

			var p probe
			app := probeApp(db, appConfig, &p)
			rec := doRequest(app, http.MethodGet, "/probe", withBearerToken(token))

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(p.user).ToNot(BeNil())
			Expect(p.user.ID).To(Equal(user.ID))
			Expect(p.source).To(Equal(auth.UsageSourceWeb))
			Expect(p.key).To(BeNil())
		})

		It("Bearer API key sets source=apikey and exposes the resolved *UserAPIKey", func() {
			db := testDB()
			appConfig := config.NewApplicationConfig()
			user := createTestUser(db, "alice@example.com", auth.RoleUser, auth.ProviderLocal)
			plaintext, key, err := auth.CreateAPIKey(db, user.ID, "ci", auth.RoleUser, appConfig.Auth.APIKeyHMACSecret, nil)
			Expect(err).ToNot(HaveOccurred())

			var p probe
			app := probeApp(db, appConfig, &p)
			rec := doRequest(app, http.MethodGet, "/probe", withBearerToken(plaintext))

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(p.source).To(Equal(auth.UsageSourceAPIKey))
			Expect(p.key).ToNot(BeNil())
			Expect(p.key.ID).To(Equal(key.ID))
		})

		It("x-api-key header sets source=apikey", func() {
			db := testDB()
			appConfig := config.NewApplicationConfig()
			user := createTestUser(db, "alice@example.com", auth.RoleUser, auth.ProviderLocal)
			plaintext, _, err := auth.CreateAPIKey(db, user.ID, "ci", auth.RoleUser, appConfig.Auth.APIKeyHMACSecret, nil)
			Expect(err).ToNot(HaveOccurred())

			var p probe
			app := probeApp(db, appConfig, &p)
			rec := doRequest(app, http.MethodGet, "/probe", withXApiKey(plaintext))

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(p.source).To(Equal(auth.UsageSourceAPIKey))
			Expect(p.key).ToNot(BeNil())
		})

		It("token cookie sets source=apikey", func() {
			db := testDB()
			appConfig := config.NewApplicationConfig()
			user := createTestUser(db, "alice@example.com", auth.RoleUser, auth.ProviderLocal)
			plaintext, _, err := auth.CreateAPIKey(db, user.ID, "ci", auth.RoleUser, appConfig.Auth.APIKeyHMACSecret, nil)
			Expect(err).ToNot(HaveOccurred())

			var p probe
			app := probeApp(db, appConfig, &p)
			rec := doRequest(app, http.MethodGet, "/probe", withTokenCookie(plaintext))

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(p.source).To(Equal(auth.UsageSourceAPIKey))
			Expect(p.key).ToNot(BeNil())
		})

		It("legacy env key sets source=legacy, apikey=nil", func() {
			db := testDB()
			appConfig := config.NewApplicationConfig()
			appConfig.ApiKeys = []string{"legacy-secret"}

			var p probe
			app := probeApp(db, appConfig, &p)
			rec := doRequest(app, http.MethodGet, "/probe", withBearerToken("legacy-secret"))

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(p.source).To(Equal(auth.UsageSourceLegacy))
			Expect(p.key).To(BeNil())
		})
	})
})
