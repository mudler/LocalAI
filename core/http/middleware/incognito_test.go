package middleware_test

import (
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/privacy"
)

var _ = Describe("Incognito middleware", func() {
	var (
		appConfig *config.ApplicationConfig
		seen      privacy.Level
	)

	// app builds the chain the server uses: auth first, then the incognito
	// middleware, then a handler that records the level it sees.
	app := func(db *gorm.DB) *echo.Echo {
		e := echo.New()
		e.Use(auth.Middleware(db, appConfig))
		e.Use(middleware.Incognito(appConfig, db != nil))
		e.POST("/v1/chat/completions", func(c echo.Context) error {
			seen = privacy.FromContext(c.Request().Context())
			return c.String(http.StatusOK, "ok")
		})
		return e
	}
	send := func(e *echo.Echo, marker string, opts ...func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		if marker != "" {
			req.Header.Set(privacy.Header, marker)
		}
		for _, o := range opts {
			o(req)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	BeforeEach(func() {
		appConfig = config.NewApplicationConfig()
		seen = privacy.Normal
	})

	Context("with accounts", func() {
		var (
			db      *gorm.DB
			session string
			apiKey  string
		)
		BeforeEach(func() {
			var err error
			db, err = auth.InitDB(":memory:")
			Expect(err).ToNot(HaveOccurred())
			u := &auth.User{ID: "u1", Email: "u1@example.test", Name: "U", Provider: auth.ProviderLocal, Role: auth.RoleUser, Status: auth.StatusActive}
			Expect(db.Create(u).Error).To(Succeed())
			session, err = auth.CreateSession(db, u.ID, "")
			Expect(err).ToNot(HaveOccurred())
			apiKey, _, err = auth.CreateAPIKey(db, u.ID, "k", auth.RoleUser, "", nil)
			Expect(err).ToNot(HaveOccurred())
		})
		cookie := func(req *http.Request) { req.AddCookie(&http.Cookie{Name: "session", Value: session}) }
		bearer := func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+apiKey) }
		xKey := func(req *http.Request) { req.Header.Set("x-api-key", apiKey) }
		bearerSession := func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+session) }

		It("gives a cookie session the level it asks for and echoes it", func() {
			rec := send(app(db), "zero-retention", cookie)
			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			Expect(seen).To(Equal(privacy.ZeroRetention))
			Expect(rec.Header().Get(privacy.Header)).To(Equal("zero-retention"))
		})

		It("ignores the marker of a bearer API key", func() {
			rec := send(app(db), "zero-retention", bearer)
			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			Expect(seen).To(Equal(privacy.Normal))
			Expect(rec.Header().Get(privacy.Header)).To(BeEmpty())
		})

		It("ignores the marker of an x-api-key and of a session token in a bearer header", func() {
			Expect(send(app(db), "memory-off", xKey).Code).To(Equal(http.StatusOK))
			Expect(seen).To(Equal(privacy.Normal))
			Expect(send(app(db), "memory-off", bearerSession).Code).To(Equal(http.StatusOK))
			Expect(seen).To(Equal(privacy.Normal))
		})

		It("answers 403 to a cookie session when the policy forbids incognito", func() {
			appConfig.Incognito = "forbid"
			rec := send(app(db), "memory-off", cookie)
			Expect(rec.Code).To(Equal(http.StatusForbidden))
			Expect(rec.Body.String()).To(ContainSubstring("incognito"))
		})

		It("keeps a bearer API key working when the policy forbids incognito", func() {
			appConfig.Incognito = "forbid"
			Expect(send(app(db), "memory-off", bearer).Code).To(Equal(http.StatusOK))
			Expect(seen).To(Equal(privacy.Normal))
		})

		It("answers 403 to a level above the maximum", func() {
			appConfig.IncognitoMaxLevelName = "memory-off"
			Expect(send(app(db), "zero-retention", cookie).Code).To(Equal(http.StatusForbidden))
			Expect(send(app(db), "memory-off", cookie).Code).To(Equal(http.StatusOK))
			Expect(seen).To(Equal(privacy.MemoryOff))
		})

		It("answers 400 to a value that names no level", func() {
			Expect(send(app(db), "secret", cookie).Code).To(Equal(http.StatusBadRequest))
		})

		It("keeps the level an in-process request carries", func() {
			u := &auth.User{ID: "u1", Role: auth.RoleUser, Status: auth.StatusActive}
			internal := func(req *http.Request) {
				ctx := privacy.WithLevel(auth.WithInternalUser(req.Context(), u), privacy.ZeroRetention)
				*req = *req.WithContext(ctx)
			}
			Expect(send(app(db), "", internal).Code).To(Equal(http.StatusOK))
			Expect(seen).To(Equal(privacy.ZeroRetention))
		})
	})

	Context("without authentication", func() {
		It("honours the marker: every request is the web UI", func() {
			Expect(send(app(nil), "memory-off").Code).To(Equal(http.StatusOK))
			Expect(seen).To(Equal(privacy.MemoryOff))
		})
	})

	Context("with legacy API keys only", func() {
		BeforeEach(func() { appConfig.ApiKeys = []string{"legacy-key"} })
		It("honours the token cookie of the web UI", func() {
			Expect(send(app(nil), "memory-off", func(r *http.Request) {
				r.AddCookie(&http.Cookie{Name: "token", Value: "legacy-key"})
			}).Code).To(Equal(http.StatusOK))
			Expect(seen).To(Equal(privacy.MemoryOff))
		})
		It("ignores the marker of the same key in a header", func() {
			Expect(send(app(nil), "memory-off", func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer legacy-key")
			}).Code).To(Equal(http.StatusOK))
			Expect(seen).To(Equal(privacy.Normal))
		})
	})
})
