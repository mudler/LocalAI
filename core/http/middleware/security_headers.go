package middleware

import (
	"html"

	"github.com/labstack/echo/v4"
)

// SecurityHeaders sets headers that limit the blast radius of any XSS bug
// that slips through. The CSP keeps script-src permissive because the Vite
// bundle relies on inline + eval'd scripts; tightening it requires moving
// to a nonce-based policy.
func SecurityHeaders() echo.MiddlewareFunc {
	const csp = "default-src 'self'; " +
		"script-src 'self' 'unsafe-inline' 'unsafe-eval' blob:; " +
		"style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: blob: https:; " +
		"media-src 'self' data: blob:; " +
		"font-src 'self' data:; " +
		"connect-src 'self' ws: wss: https:; " +
		"frame-src 'self' blob:; " +
		"worker-src 'self' blob:; " +
		"object-src 'none'; " +
		"base-uri 'self'; " +
		"form-action 'self'; " +
		"frame-ancestors 'self'"

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			h := c.Response().Header()
			if h.Get("Content-Security-Policy") == "" {
				h.Set("Content-Security-Policy", csp)
			}
			if h.Get("X-Content-Type-Options") == "" {
				h.Set("X-Content-Type-Options", "nosniff")
			}
			if h.Get("X-Frame-Options") == "" {
				h.Set("X-Frame-Options", "SAMEORIGIN")
			}
			if h.Get("Referrer-Policy") == "" {
				h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			}
			return next(c)
		}
	}
}

// SecureBaseHref escapes a base URL value for safe interpolation into a
// `<base href="...">` attribute. baseURL is built from Host /
// X-Forwarded-Host, both attacker-controllable on most reverse-proxy setups.
func SecureBaseHref(s string) string {
	return html.EscapeString(s)
}
