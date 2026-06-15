package localai_test

import (
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Traces Endpoints", func() {
	var app *echo.Echo

	BeforeEach(func() {
		app = echo.New()
		app.GET("/api/traces", GetAPITracesEndpoint())
		app.POST("/api/traces/clear", ClearAPITracesEndpoint())
		app.GET("/api/backend-traces", GetBackendTracesEndpoint())
		app.POST("/api/backend-traces/clear", ClearBackendTracesEndpoint())
	})

	It("should return API traces", func() {
		req := httptest.NewRequest(http.MethodGet, "/api/traces", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusOK))
	})

	It("should clear API traces", func() {
		req := httptest.NewRequest(http.MethodPost, "/api/traces/clear", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusNoContent))
	})

	It("should return backend traces", func() {
		req := httptest.NewRequest(http.MethodGet, "/api/backend-traces", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusOK))
	})

	It("should clear backend traces", func() {
		req := httptest.NewRequest(http.MethodPost, "/api/backend-traces/clear", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusNoContent))
	})
})
