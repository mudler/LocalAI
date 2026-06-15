package middleware

import (
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("BaseURL", func() {
	Context("without prefix", func() {
		It("should return base URL without prefix", func() {
			app := echo.New()
			actualURL := ""

			// Register route - use the actual request path so routing works
			routePath := "/hello/world"
			app.GET(routePath, func(c echo.Context) error {
				actualURL = BaseURL(c)
				return nil
			})

			req := httptest.NewRequest("GET", "/hello/world", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(200), "response status code")
			Expect(actualURL).To(Equal("http://example.com/"), "base URL")
		})
	})

	Context("with prefix", func() {
		It("should return base URL with prefix", func() {
			app := echo.New()
			actualURL := ""

			// Register route with the stripped path (after middleware removes prefix)
			routePath := "/hello/world"
			app.GET(routePath, func(c echo.Context) error {
				// Simulate what StripPathPrefix middleware does - store original path
				c.Set("_original_path", "/myprefix/hello/world")
				// Modify the request path to simulate prefix stripping
				c.Request().URL.Path = "/hello/world"
				actualURL = BaseURL(c)
				return nil
			})

			// Make request with stripped path (middleware would have already processed it)
			req := httptest.NewRequest("GET", "/hello/world", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(200), "response status code")
			Expect(actualURL).To(Equal("http://example.com/myprefix/"), "base URL")
		})
	})

	// Caddy's handle_path (and similar reverse-proxy directives) strips the
	// matched prefix before forwarding upstream, so LocalAI receives the
	// already-stripped path together with X-Forwarded-Prefix. In that case
	// StripPathPrefix never stores _original_path, but BaseURL must still
	// honor the header so that <base href> and asset URLs include the prefix.
	Context("with X-Forwarded-Prefix header but pre-stripped path", func() {
		It("should return base URL with prefix from header", func() {
			app := echo.New()
			actualURL := ""

			routePath := "/app"
			app.GET(routePath, func(c echo.Context) error {
				actualURL = BaseURL(c)
				return nil
			})

			req := httptest.NewRequest("GET", "/app", nil)
			req.Header.Set("X-Forwarded-Prefix", "/localai")
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(200), "response status code")
			Expect(actualURL).To(Equal("http://example.com/localai/"), "base URL")
		})

		It("should normalize a prefix that already ends with a slash", func() {
			app := echo.New()
			actualURL := ""

			routePath := "/app"
			app.GET(routePath, func(c echo.Context) error {
				actualURL = BaseURL(c)
				return nil
			})

			req := httptest.NewRequest("GET", "/app", nil)
			req.Header.Set("X-Forwarded-Prefix", "/localai/")
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(200), "response status code")
			Expect(actualURL).To(Equal("http://example.com/localai/"), "base URL")
		})
	})

	// X-Forwarded-Prefix is attacker controllable on misconfigured proxy
	// chains, and the value flows into the SPA HTML response (<base href>
	// and asset URLs). BasePathPrefix must gate the header through
	// SafeForwardedPrefix so values that turn the prefix into an open
	// redirect or a protocol-relative URL are ignored and the base falls
	// back to "/".
	Context("with unsafe X-Forwarded-Prefix header", func() {
		DescribeTable("falls back to / when the header is unsafe",
			func(header string) {
				app := echo.New()
				actualURL := ""

				app.GET("/app", func(c echo.Context) error {
					actualURL = BaseURL(c)
					return nil
				})

				req := httptest.NewRequest("GET", "/app", nil)
				req.Header.Set("X-Forwarded-Prefix", header)
				rec := httptest.NewRecorder()
				app.ServeHTTP(rec, req)

				Expect(rec.Code).To(Equal(200), "response status code")
				Expect(actualURL).To(Equal("http://example.com/"), "base URL")
			},
			Entry("protocol-relative URL", "//evil.com"),
			Entry("protocol-relative URL with path", "//evil.com/assets"),
			Entry("backslash path", `/foo\bar`),
			Entry("embedded NUL", "/foo\x00bar"),
			Entry("CR injection", "/foo\rbar"),
			Entry("LF injection", "/foo\nbar"),
			Entry("missing leading slash", "evil"),
		)
	})
})
