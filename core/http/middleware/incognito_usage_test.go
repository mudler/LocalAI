package middleware_test

import (
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	httpMiddleware "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/privacy"
	"github.com/mudler/LocalAI/core/services/routing/billing"
)

var _ = Describe("UsageMiddleware for an incognito request", func() {
	record := func(level privacy.Level) *auth.UsageRecord {
		cap := &captureBackend{}
		e := echo.New()
		e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				c.SetRequest(c.Request().WithContext(privacy.WithLevel(c.Request().Context(), level)))
				return next(c)
			}
		})
		e.POST("/v1/chat/completions", func(c echo.Context) error {
			Expect(httpMiddleware.StampResponseMetadata(c, "m", []byte(`{"usage":{"input_units":5,"output_units":7,"accounting_rule":"tokens"}}`))).To(Succeed())
			return c.JSON(http.StatusOK, map[string]string{"model": "m"})
		}, httpMiddleware.UsageMiddleware(billing.NewRecorder(cap), &auth.User{ID: "local"}))
		w := httptest.NewRecorder()
		e.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(cap.records).To(HaveLen(1))
		return cap.records[0]
	}

	It("keeps the counters of a zero-retention request, flags it and drops the metadata", func() {
		r := record(privacy.ZeroRetention)
		Expect(r.UserID).To(Equal("local"))
		Expect(r.Model).To(Equal("m"))
		Expect(r.PromptTokens).To(Equal(int64(5)))
		Expect(r.CompletionTokens).To(Equal(int64(7)))
		Expect(r.Incognito).To(BeTrue())
		Expect(r.Metadata).To(BeEmpty())
	})

	It("keeps everything of a memory-off request", func() {
		r := record(privacy.MemoryOff)
		Expect(r.Incognito).To(BeFalse())
		Expect(r.Metadata).ToNot(BeEmpty())
	})
})
