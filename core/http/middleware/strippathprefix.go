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
						c.Request().URL.Path = originalPath[len(normalizedPrefix):]
						if c.Request().URL.Path == "" {
							c.Request().URL.Path = "/"
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
