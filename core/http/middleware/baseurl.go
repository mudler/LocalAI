package middleware

import (
	"strings"

	"github.com/labstack/echo/v4"
)

// BasePathPrefix returns the URL path prefix that the request was reached
// under (e.g. "/myprefix/"). It always returns a value that starts and ends
// with `/`, defaulting to "/" when the app is not behind a path prefix.
//
// It first looks at the path StripPathPrefix removed (when the proxy forwards
// the prefix in the URL), then falls back to the X-Forwarded-Prefix header
// (when the proxy strips the prefix before forwarding, e.g. Caddy's
// handle_path).
//
// The header fallback is gated through SafeForwardedPrefix because the value
// flows into the SPA HTML response (both <base href> and the path-absolute
// asset URL rewrite in serveIndex). X-Forwarded-Prefix is attacker
// controllable on misconfigured proxy chains; without that gate a value like
// "//evil.com" turns the asset rewrite into a protocol-relative URL that
// loads JS from a foreign origin.
func BasePathPrefix(c echo.Context) string {
	path := c.Path()
	origPath := c.Request().URL.Path

	if storedPath, ok := c.Get("_original_path").(string); ok && storedPath != "" {
		origPath = storedPath
	}

	if path != origPath && strings.HasSuffix(origPath, path) && len(path) > 0 {
		prefixLen := len(origPath) - len(path)
		if prefixLen > 0 {
			pathPrefix := origPath[:prefixLen]
			if !strings.HasSuffix(pathPrefix, "/") {
				pathPrefix += "/"
			}
			return pathPrefix
		}
	}

	if validated, ok := SafeForwardedPrefix(c.Request().Header.Get("X-Forwarded-Prefix")); ok {
		if !strings.HasSuffix(validated, "/") {
			validated += "/"
		}
		return validated
	}

	return "/"
}

// BaseURL returns the base URL for the given HTTP request context.
// It takes into account that the app may be exposed by a reverse-proxy under a different protocol, host and path.
// The returned URL is guaranteed to end with `/`.
// The method should be used in conjunction with the StripPathPrefix middleware.
func BaseURL(c echo.Context) string {
	scheme := "http"
	if c.Request().Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	} else if c.Request().TLS != nil {
		scheme = "https"
	}

	host := c.Request().Host
	if forwardedHost := c.Request().Header.Get("X-Forwarded-Host"); forwardedHost != "" {
		host = forwardedHost
	}

	return scheme + "://" + host + BasePathPrefix(c)
}
