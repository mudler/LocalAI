//go:build auth

package middleware_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/middleware"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// testAuthDB returns a fresh in-memory SQLite auth DB.
func testAuthDB() *gorm.DB {
	db, err := auth.InitDB(":memory:")
	if err != nil {
		panic(err)
	}
	return db
}

var _ = Describe("UsageMiddleware", func() {
	var (
		e  *echo.Echo
		db *gorm.DB
	)

	BeforeEach(func() {
		db = testAuthDB()
		e = echo.New()
		middleware.InitUsageRecorder(db)
	})

	AfterEach(func() {
		middleware.ShutdownUsageRecorder()
	})

	okHandler := func(c echo.Context) error {
		body, _ := json.Marshal(map[string]any{
			"model": "gpt-4",
			"usage": map[string]int{
				"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15,
			},
		})
		c.Response().Header().Set("Content-Type", "application/json")
		c.Response().WriteHeader(http.StatusOK)
		_, _ = c.Response().Write(body)
		return nil
	}

	// FlushNow drains pending records synchronously, replacing the 6s sleep
	// that was previously needed to wait for the batcher's ticker.
	flush := middleware.FlushNow

	It("records source=web when auth_source is web", func() {
		e.POST("/v1/chat/completions", okHandler, func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				c.Set("auth_user", &auth.User{ID: "alice", Name: "Alice"})
				c.Set("auth_source", auth.UsageSourceWeb)
				return next(c)
			}
		}, middleware.UsageMiddleware(db))

		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader([]byte(`{}`)))
		e.ServeHTTP(httptest.NewRecorder(), req)
		flush()

		var rec auth.UsageRecord
		Expect(db.Where("user_id = ?", "alice").First(&rec).Error).To(Succeed())
		Expect(rec.Source).To(Equal(auth.UsageSourceWeb))
		Expect(rec.APIKeyID).To(BeNil())
		Expect(rec.APIKeyName).To(BeEmpty())
	})

	It("records source=apikey with snapshotted name when auth_apikey is set", func() {
		e.POST("/v1/chat/completions", okHandler, func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				c.Set("auth_user", &auth.User{ID: "alice", Name: "Alice"})
				c.Set("auth_source", auth.UsageSourceAPIKey)
				c.Set("auth_apikey", &auth.UserAPIKey{ID: "key-1", Name: "ci-runner"})
				return next(c)
			}
		}, middleware.UsageMiddleware(db))

		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader([]byte(`{}`)))
		e.ServeHTTP(httptest.NewRecorder(), req)
		flush()

		var rec auth.UsageRecord
		Expect(db.Where("user_id = ?", "alice").First(&rec).Error).To(Succeed())
		Expect(rec.Source).To(Equal(auth.UsageSourceAPIKey))
		Expect(rec.APIKeyID).ToNot(BeNil())
		Expect(*rec.APIKeyID).To(Equal("key-1"))
		Expect(rec.APIKeyName).To(Equal("ci-runner"))
	})

	It("FlushNow drains pending records synchronously", func() {
		e.POST("/v1/chat/completions", okHandler, func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				c.Set("auth_user", &auth.User{ID: "carol", Name: "Carol"})
				c.Set("auth_source", auth.UsageSourceWeb)
				return next(c)
			}
		}, middleware.UsageMiddleware(db))

		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader([]byte(`{}`)))
		e.ServeHTTP(httptest.NewRecorder(), req)

		// No sleep: FlushNow should drain immediately.
		middleware.FlushNow()

		var rec auth.UsageRecord
		Expect(db.Where("user_id = ?", "carol").First(&rec).Error).To(Succeed())
		Expect(rec.Source).To(Equal(auth.UsageSourceWeb))
	})

	It("falls back to source=web when auth_source is empty", func() {
		e.POST("/v1/chat/completions", okHandler, func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				c.Set("auth_user", &auth.User{ID: "alice", Name: "Alice"})
				// no auth_source set
				return next(c)
			}
		}, middleware.UsageMiddleware(db))

		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader([]byte(`{}`)))
		e.ServeHTTP(httptest.NewRecorder(), req)
		flush()

		var rec auth.UsageRecord
		Expect(db.Where("user_id = ?", "alice").First(&rec).Error).To(Succeed())
		Expect(rec.Source).To(Equal(auth.UsageSourceWeb))
	})
})
