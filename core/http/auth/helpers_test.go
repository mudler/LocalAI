//go:build auth

package auth_test

import (
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/pkg/natsauth"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// testDB creates an in-memory SQLite GORM instance with auto-migration.
func testDB() *gorm.DB {
	db, err := auth.InitDB(":memory:")
	Expect(err).ToNot(HaveOccurred())
	return db
}

// createTestUser inserts a user directly into the DB for test setup.
func createTestUser(db *gorm.DB, email, role, provider string) *auth.User {
	user := &auth.User{
		ID:       generateTestID(),
		Email:    email,
		Name:     "Test User",
		Provider: provider,
		Subject:  generateTestID(),
		Role:     role,
		Status:   auth.StatusActive,
	}
	err := db.Create(user).Error
	Expect(err).ToNot(HaveOccurred())
	return user
}

// createTestSession creates a session for a user, returns plaintext session token.
func createTestSession(db *gorm.DB, userID string) string {
	sessionID, err := auth.CreateSession(db, userID, "")
	Expect(err).ToNot(HaveOccurred())
	return sessionID
}

var testIDCounter int

func generateTestID() string {
	testIDCounter++
	return "test-id-" + string(rune('a'+testIDCounter))
}

// ok is a simple handler that returns 200 OK.
func ok(c echo.Context) error {
	return c.String(http.StatusOK, "ok")
}

// newAuthTestApp creates a minimal Echo app with the new auth middleware.
func newAuthTestApp(db *gorm.DB, appConfig *config.ApplicationConfig) *echo.Echo {
	e := echo.New()
	e.Use(auth.Middleware(db, appConfig))
	if db != nil {
		e.Use(auth.RequireRouteFeature(db))
	}

	// API routes (require auth)
	e.GET("/v1/models", ok)
	e.POST("/v1/chat/completions", ok)
	e.POST("/v1/moderations", ok)
	e.POST("/moderations", ok)
	e.POST("/v1/mcp/chat/completions", ok)
	e.POST("/mcp/v1/chat/completions", ok)
	e.POST("/mcp/chat/completions", ok)
	e.GET("/api/settings", ok)
	e.POST("/api/settings", ok)

	// Auth routes (exempt)
	e.GET("/api/auth/status", ok)
	e.GET("/api/auth/github/login", ok)

	// Static routes
	e.GET("/app", ok)
	e.GET("/app/*", ok)

	return e
}

// newCatchAllAuthTestApp makes the route itself irrelevant so middleware tests
// can prove that newly registered routes are private by default.
func newCatchAllAuthTestApp(db *gorm.DB, appConfig *config.ApplicationConfig) *echo.Echo {
	e := echo.New()
	e.Use(auth.Middleware(db, appConfig))
	e.Any("/", ok)
	e.Any("/*", ok)
	return e
}

// newNodeSelfServiceTestApp uses the real node route registration so requests
// that global auth delegates must still pass nodeTokenAuth before the handler.
func newNodeSelfServiceTestApp(db *gorm.DB, appConfig *config.ApplicationConfig, registrationToken string) *echo.Echo {
	registry, err := nodes.NewNodeRegistry(db)
	Expect(err).ToNot(HaveOccurred())

	e := echo.New()
	e.Use(auth.Middleware(db, appConfig))
	routes.RegisterNodeSelfServiceRoutes(
		e,
		registry,
		registrationToken,
		false,
		nil,
		"",
		natsauth.Config{},
	)
	return e
}

// newAdminTestApp creates an Echo app with admin-protected routes.
func newAdminTestApp(db *gorm.DB, appConfig *config.ApplicationConfig) *echo.Echo {
	e := echo.New()
	e.Use(auth.Middleware(db, appConfig))
	if db != nil {
		e.Use(auth.RequireRouteFeature(db))
	}

	// Regular routes
	e.GET("/v1/models", ok)
	e.POST("/v1/chat/completions", ok)
	e.POST("/v1/moderations", ok)

	// Admin-only routes
	adminMw := auth.RequireAdmin()
	e.POST("/api/settings", ok, adminMw)
	e.POST("/models/apply", ok, adminMw)
	e.POST("/backends/apply", ok, adminMw)
	e.GET("/api/agents", ok, adminMw)

	// Trace/log endpoints (admin only)
	e.GET("/api/traces", ok, adminMw)
	e.POST("/api/traces/clear", ok, adminMw)
	e.GET("/api/backend-logs", ok, adminMw)
	e.GET("/api/backend-logs/:modelId", ok, adminMw)

	// Gallery/management reads (admin only)
	e.GET("/api/operations", ok, adminMw)
	e.GET("/api/models", ok, adminMw)
	e.GET("/api/backends", ok, adminMw)
	e.GET("/api/resources", ok, adminMw)
	e.GET("/api/p2p/workers", ok, adminMw)

	// Agent task/job routes (admin only)
	e.POST("/api/agent/tasks", ok, adminMw)
	e.GET("/api/agent/tasks", ok, adminMw)
	e.GET("/api/agent/jobs", ok, adminMw)

	// System info (admin only)
	e.GET("/system", ok, adminMw)
	e.GET("/backend/monitor", ok, adminMw)

	return e
}

// doRequest performs an HTTP request against the given Echo app and returns the recorder.
func doRequest(e *echo.Echo, method, path string, opts ...func(*http.Request)) *httptest.ResponseRecorder {
	return doRequestWithBody(e, method, path, "", opts...)
}

func doRequestWithBody(
	e *echo.Echo,
	method, path, body string,
	opts ...func(*http.Request),
) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, opt := range opts {
		opt(req)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func withBearerToken(token string) func(*http.Request) {
	return func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func withXApiKey(key string) func(*http.Request) {
	return func(req *http.Request) {
		req.Header.Set("x-api-key", key)
	}
}

func withSessionCookie(sessionID string) func(*http.Request) {
	return func(req *http.Request) {
		req.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
	}
}

func withTokenCookie(token string) func(*http.Request) {
	return func(req *http.Request) {
		req.AddCookie(&http.Cookie{Name: "token", Value: token})
	}
}
