package localai_test

import (
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/middleware"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Canvas preview", func() {
	var e *echo.Echo
	const page = `<script src="https://unpkg.com/three@0.160.0/build/three.module.js"></script>`

	BeforeEach(func() {
		store := NewCanvasPreviewStore()
		e = echo.New()
		e.Use(middleware.SecurityHeaders())
		e.POST("/api/canvas/preview", CreateCanvasPreviewEndpoint(store))
		e.GET("/api/canvas/preview/:id", GetCanvasPreviewEndpoint(store))
		e.GET("/other", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })
	})

	create := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/canvas/preview", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	It("serves the stored HTML with a CDN-friendly CSP", func() {
		rec := create(`{"code":"<script src=\"https://unpkg.com/three@0.160.0/build/three.module.js\"></script>"}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		id := strings.Trim(strings.Split(rec.Body.String(), `"id":"`)[1], "\"}\n")

		req := httptest.NewRequest(http.MethodGet, "/api/canvas/preview/"+id, nil)
		out := httptest.NewRecorder()
		e.ServeHTTP(out, req)
		Expect(out.Code).To(Equal(http.StatusOK))
		Expect(out.Body.String()).To(Equal(page))
		csp := out.Header().Get("Content-Security-Policy")
		for _, host := range []string{"https://unpkg.com", "https://cdn.jsdelivr.net", "https://cdnjs.cloudflare.com", "https://cdn.tailwindcss.com", "https://code.jquery.com"} {
			Expect(csp).To(ContainSubstring(host))
		}
		Expect(csp).To(ContainSubstring("default-src 'none'"))
		Expect(csp).To(ContainSubstring("sandbox allow-scripts"))
		Expect(csp).To(ContainSubstring("frame-ancestors 'self'"))
		Expect(out.Header().Values("Content-Security-Policy")).To(HaveLen(1))
	})

	It("returns 404 for an unknown id", func() {
		req := httptest.NewRequest(http.MethodGet, "/api/canvas/preview/nope", nil)
		out := httptest.NewRecorder()
		e.ServeHTTP(out, req)
		Expect(out.Code).To(Equal(http.StatusNotFound))
	})

	It("keeps the global CSP on other routes", func() {
		req := httptest.NewRequest(http.MethodGet, "/other", nil)
		out := httptest.NewRecorder()
		e.ServeHTTP(out, req)
		csp := out.Header().Get("Content-Security-Policy")
		Expect(csp).To(ContainSubstring("script-src 'self' 'unsafe-inline' 'unsafe-eval' blob:;"))
		Expect(csp).ToNot(ContainSubstring("unpkg.com"))
	})
})
