package middleware_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/middleware"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Compression middleware", func() {
	var e *echo.Echo

	// A payload comfortably above the minimum-length threshold and highly
	// repetitive, so a real gzip pass shrinks it dramatically.
	body := strings.Repeat("localai compresses this json payload. ", 400)

	BeforeEach(func() {
		e = echo.New()
		e.Use(middleware.Compression(middleware.DefaultCompressionMinLength))
		handler := func(c echo.Context) error {
			return c.String(http.StatusOK, body)
		}
		e.GET("/assets/bundle.js", handler)
		e.GET("/api/traces", handler)
		e.GET("/v1/chat/completions", handler)
		e.GET("/api/agents/demo/sse", handler)
		e.GET("/api/tiny", func(c echo.Context) error {
			return c.String(http.StatusOK, "ok")
		})
		e.GET("/assets/font.woff2", handler)
	})

	get := func(path string, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Accept-Encoding", "gzip")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	It("gzips a compressible static asset response", func() {
		rec := get("/assets/bundle.js", nil)

		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Header().Get("Content-Encoding")).To(Equal("gzip"))
		Expect(rec.Body.Len()).To(BeNumerically("<", len(body)/2))

		zr, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
		Expect(err).ToNot(HaveOccurred())
		decoded, err := io.ReadAll(zr)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(decoded)).To(Equal(body))
	})

	It("gzips JSON API responses", func() {
		rec := get("/api/traces", nil)

		Expect(rec.Header().Get("Content-Encoding")).To(Equal("gzip"))
		Expect(rec.Body.Len()).To(BeNumerically("<", len(body)))
	})

	It("does not compress streaming completion endpoints", func() {
		rec := get("/v1/chat/completions", nil)

		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Header().Get("Content-Encoding")).To(BeEmpty())
		Expect(rec.Body.String()).To(Equal(body))
	})

	It("does not compress SSE bridges", func() {
		rec := get("/api/agents/demo/sse", nil)

		Expect(rec.Header().Get("Content-Encoding")).To(BeEmpty())
		Expect(rec.Body.String()).To(Equal(body))
	})

	It("does not compress a request that asks for an event stream", func() {
		rec := get("/api/traces", map[string]string{"Accept": "text/event-stream"})

		Expect(rec.Header().Get("Content-Encoding")).To(BeEmpty())
		Expect(rec.Body.String()).To(Equal(body))
	})

	It("does not compress responses below the minimum length", func() {
		rec := get("/api/tiny", nil)

		Expect(rec.Header().Get("Content-Encoding")).To(BeEmpty())
		Expect(rec.Body.String()).To(Equal("ok"))
	})

	It("does not re-compress formats that are already compressed", func() {
		rec := get("/assets/font.woff2", nil)

		Expect(rec.Header().Get("Content-Encoding")).To(BeEmpty())
		Expect(rec.Body.String()).To(Equal(body))
	})

	It("leaves the body untouched when the client does not accept gzip", func() {
		req := httptest.NewRequest(http.MethodGet, "/assets/bundle.js", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec.Header().Get("Content-Encoding")).To(BeEmpty())
		Expect(rec.Body.String()).To(Equal(body))
	})
})
