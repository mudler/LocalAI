package middleware_test

import (
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/middleware"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// ok is a simple handler that returns 200 OK.
func ok(c echo.Context) error {
	return c.String(http.StatusOK, "ok")
}

// newAuthApp creates a minimal Echo app with auth middleware applied.
// Requests that fail auth with Content-Type: application/json get a JSON 401
// (no template renderer needed).
func newAuthApp(appConfig *config.ApplicationConfig) *echo.Echo {
	e := echo.New()

	mw, err := GetKeyAuthConfig(appConfig)
	Expect(err).ToNot(HaveOccurred())
	e.Use(mw)

	// Sensitive API routes
	e.GET("/v1/models", ok)
	e.POST("/v1/chat/completions", ok)

	// UI routes
	e.GET("/app", ok)
	e.GET("/app/*", ok)
	e.GET("/browse", ok)
	e.GET("/browse/*", ok)
	e.GET("/login", ok)
	e.GET("/explorer", ok)
	e.GET("/assets/*", ok)
	e.POST("/app", ok)

	return e
}

// doRequest performs an HTTP request against the given Echo app and returns the recorder.
func doRequest(e *echo.Echo, method, path string, opts ...func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
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

func withXiApiKey(key string) func(*http.Request) {
	return func(req *http.Request) {
		req.Header.Set("xi-api-key", key)
	}
}

func withTokenCookie(token string) func(*http.Request) {
	return func(req *http.Request) {
		req.AddCookie(&http.Cookie{Name: "token", Value: token})
	}
}

var _ = Describe("Auth Middleware", func() {

	Context("when API keys are configured", func() {
		var app *echo.Echo
		const validKey = "sk-test-key-123"

		BeforeEach(func() {
			appConfig := config.NewApplicationConfig()
			appConfig.ApiKeys = []string{validKey}
			app = newAuthApp(appConfig)
		})

		It("returns 401 for GET request without a key", func() {
			rec := doRequest(app, http.MethodGet, "/v1/models")
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})

		It("returns 401 for POST request without a key", func() {
			rec := doRequest(app, http.MethodPost, "/v1/chat/completions")
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})

		It("returns 401 for request with an invalid key", func() {
			rec := doRequest(app, http.MethodGet, "/v1/models", withBearerToken("wrong-key"))
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})

		It("passes through with valid Bearer token in Authorization header", func() {
			rec := doRequest(app, http.MethodGet, "/v1/models", withBearerToken(validKey))
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("passes through with valid x-api-key header", func() {
			rec := doRequest(app, http.MethodGet, "/v1/models", withXApiKey(validKey))
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("passes through with valid xi-api-key header", func() {
			rec := doRequest(app, http.MethodGet, "/v1/models", withXiApiKey(validKey))
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("passes through with valid token cookie", func() {
			rec := doRequest(app, http.MethodGet, "/v1/models", withTokenCookie(validKey))
			Expect(rec.Code).To(Equal(http.StatusOK))
		})
	})

	Context("when no API keys are configured", func() {
		var app *echo.Echo

		BeforeEach(func() {
			appConfig := config.NewApplicationConfig()
			app = newAuthApp(appConfig)
		})

		It("passes through without any key", func() {
			rec := doRequest(app, http.MethodGet, "/v1/models")
			Expect(rec.Code).To(Equal(http.StatusOK))
		})
	})

	Context("GET exempted endpoints (feature enabled)", func() {
		var app *echo.Echo
		const validKey = "sk-test-key-456"

		BeforeEach(func() {
			appConfig := config.NewApplicationConfig(
				config.WithApiKeys([]string{validKey}),
				config.WithDisableApiKeyRequirementForHttpGet(true),
				config.WithHttpGetExemptedEndpoints([]string{
					"^/$",
					"^/app(/.*)?$",
					"^/browse(/.*)?$",
					"^/login/?$",
					"^/explorer/?$",
					"^/assets/.*$",
					"^/static/.*$",
					"^/swagger.*$",
				}),
			)
			app = newAuthApp(appConfig)
		})

		It("allows GET to /app without a key", func() {
			rec := doRequest(app, http.MethodGet, "/app")
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("allows GET to /app/chat/model sub-route without a key", func() {
			rec := doRequest(app, http.MethodGet, "/app/chat/llama3")
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("allows GET to /browse/models without a key", func() {
			rec := doRequest(app, http.MethodGet, "/browse/models")
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("allows GET to /login without a key", func() {
			rec := doRequest(app, http.MethodGet, "/login")
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("allows GET to /explorer without a key", func() {
			rec := doRequest(app, http.MethodGet, "/explorer")
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("allows GET to /assets/main.js without a key", func() {
			rec := doRequest(app, http.MethodGet, "/assets/main.js")
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("rejects POST to /app without a key", func() {
			rec := doRequest(app, http.MethodPost, "/app")
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})

		It("rejects GET to /v1/models without a key", func() {
			rec := doRequest(app, http.MethodGet, "/v1/models")
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})
	})

	Context("GET exempted endpoints (feature disabled)", func() {
		var app *echo.Echo
		const validKey = "sk-test-key-789"

		BeforeEach(func() {
			appConfig := config.NewApplicationConfig(
				config.WithApiKeys([]string{validKey}),
				// DisableApiKeyRequirementForHttpGet defaults to false
				config.WithHttpGetExemptedEndpoints([]string{
					"^/$",
					"^/app(/.*)?$",
				}),
			)
			app = newAuthApp(appConfig)
		})

		It("requires auth for GET to /app even though it matches exempted pattern", func() {
			rec := doRequest(app, http.MethodGet, "/app")
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})
	})
})
