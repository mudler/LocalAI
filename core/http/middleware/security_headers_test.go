package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SecurityHeaders", func() {
	var e *echo.Echo

	BeforeEach(func() {
		e = echo.New()
		e.Use(SecurityHeaders())
		e.GET("/", func(c echo.Context) error {
			return c.String(http.StatusOK, "ok")
		})
	})

	It("sets Content-Security-Policy", func() {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))
		csp := rec.Header().Get("Content-Security-Policy")
		Expect(csp).ToNot(BeEmpty())
		Expect(csp).To(ContainSubstring("default-src 'self'"))
		Expect(csp).To(ContainSubstring("frame-ancestors 'self'"))
		Expect(csp).To(ContainSubstring("object-src 'none'"))
		Expect(csp).To(ContainSubstring("base-uri 'self'"))
	})

	It("sets X-Content-Type-Options: nosniff", func() {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Header().Get("X-Content-Type-Options")).To(Equal("nosniff"))
	})

	It("sets X-Frame-Options: SAMEORIGIN", func() {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Header().Get("X-Frame-Options")).To(Equal("SAMEORIGIN"))
	})

	It("sets Referrer-Policy", func() {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Header().Get("Referrer-Policy")).To(Equal("strict-origin-when-cross-origin"))
	})

	It("does not overwrite a header a later handler set explicitly", func() {
		// Reset router so we can install a handler that sets CSP itself.
		e = echo.New()
		e.Use(SecurityHeaders())
		e.GET("/", func(c echo.Context) error {
			c.Response().Header().Set("Content-Security-Policy", "default-src 'none'")
			return c.String(http.StatusOK, "ok")
		})
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		// The middleware runs first (sets default), but a later handler may
		// want a tighter CSP for a specific response. The middleware should
		// only set headers that aren't already present — but since Echo
		// middleware runs around the handler, the middleware's Set calls
		// happen before the handler runs. So this is more of a smoke test
		// that the middleware doesn't actively clobber on the way out.
		Expect(rec.Header().Get("Content-Security-Policy")).To(Equal("default-src 'none'"))
	})
})

var _ = Describe("SecureBaseHref", func() {
	It("escapes attribute-breaking characters", func() {
		out := SecureBaseHref(`"><script>alert(1)</script>`)
		Expect(out).ToNot(ContainSubstring(`"`))
		Expect(out).ToNot(ContainSubstring("<"))
		Expect(out).ToNot(ContainSubstring(">"))
		Expect(out).To(ContainSubstring("script"))
	})

	It("escapes ampersands", func() {
		Expect(SecureBaseHref("https://example.com/?a=1&b=2")).
			To(Equal("https://example.com/?a=1&amp;b=2"))
	})

	It("escapes single quotes", func() {
		Expect(SecureBaseHref(`x' onload='alert(1)`)).
			To(ContainSubstring("&#39;"))
	})

	It("leaves benign URLs alone", func() {
		Expect(SecureBaseHref("https://example.com/app/")).
			To(Equal("https://example.com/app/"))
	})

	It("encloses safely inside double-quoted attribute", func() {
		// The realistic attack: attacker sets X-Forwarded-Host: foo.com" onload="x.
		// Confirm the escaped form can't break out of the surrounding quotes.
		hostile := `foo.com" onload="alert(1)`
		out := SecureBaseHref(hostile)
		Expect(out).ToNot(ContainSubstring(`"`))
		// Wrapped in attribute context — no raw quote means no breakout.
		full := `<base href="` + out + `" />`
		Expect(strings.Count(full, `"`)).To(Equal(2))
	})
})
