package middleware

import (
	"strings"

	"github.com/labstack/echo/v4"
)

// BaseURL returns the base URL for the given HTTP request context.
// It takes into account that the app may be exposed by a reverse-proxy under a different protocol, host and path.
// The returned URL is guaranteed to end with `/`.
// The method should be used in conjunction with the StripPathPrefix middleware.
func BaseURL(c echo.Context) string {
	// Use the current request path (after StripPathPrefix has stripped the prefix)
	// NOT c.Path() which returns the route pattern (e.g., '/app/*')
	currentPath := c.Request().URL.Path
	
	// Get the original path before stripping
	origPath := currentPath

	// Check if StripPathPrefix middleware stored the original path
	if storedPath, ok := c.Get("_original_path").(string); ok && storedPath != "" {
		origPath = storedPath
	}

	// Check X-Forwarded-Proto for scheme
	scheme := "http"
	if c.Request().Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	} else if c.Request().TLS != nil {
		scheme = "https"
	}

	// Check X-Forwarded-Host for host
	host := c.Request().Host
	if forwardedHost := c.Request().Header.Get("X-Forwarded-Host"); forwardedHost != "" {
		host = forwardedHost
	}

	// Calculate the prefix by comparing original path with current (stripped) path
	// The prefix is what was stripped from the beginning of origPath to get currentPath
	if origPath != currentPath && strings.HasSuffix(origPath, currentPath) && len(origPath) > len(currentPath) {
		pathPrefix := origPath[:len(origPath)-len(currentPath)]
		if !strings.HasSuffix(pathPrefix, "/") {
			pathPrefix += "/"
		}
		return scheme + "://" + host + pathPrefix
	}

	return scheme + "://" + host + "/"
}
