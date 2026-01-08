package middleware

import (
	"strings"

	"github.com/labstack/echo/v4"
)

// StripPathPrefix returns middleware that strips a path prefix from the request path.
// The path prefix is obtained from the X-Forwarded-Prefix HTTP request header.
// This must be registered as Pre middleware (using e.Pre()) to modify the path before routing.
func StripPathPrefix() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			prefixes := c.Request().Header.Values("X-Forwarded-Prefix")
			originalPath := c.Request().URL.Path

			for _, prefix := range prefixes {
				if prefix != "" {
					normalizedPrefix := prefix
					if !strings.HasSuffix(prefix, "/") {
						normalizedPrefix = prefix + "/"
					}

					if strings.HasPrefix(originalPath, normalizedPrefix) {
						// Update the request path by stripping the normalized prefix
						newPath := originalPath[len(normalizedPrefix):]
						if newPath == "" {
							newPath = "/"
						}
						// Ensure path starts with / for proper routing
						if !strings.HasPrefix(newPath, "/") {
							newPath = "/" + newPath
						}
						// Update the URL path - Echo's router uses URL.Path for routing
						c.Request().URL.Path = newPath
						c.Request().URL.RawPath = ""
						// Update RequestURI to match the new path (needed for proper routing)
						if c.Request().URL.RawQuery != "" {
							c.Request().RequestURI = newPath + "?" + c.Request().URL.RawQuery
						} else {
							c.Request().RequestURI = newPath
						}
						// Store original path for BaseURL utility
						c.Set("_original_path", originalPath)
						break
					} else if originalPath == prefix || originalPath == prefix+"/" {
						// Redirect to prefix with trailing slash (use 302 to match test expectations)
						return c.Redirect(302, normalizedPrefix)
					}
				}
			}

			return next(c)
		}
	}
}
