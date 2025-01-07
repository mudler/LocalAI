package utils

import (
	"strings"

	"github.com/gofiber/fiber/v2"
)

// BaseURL returns the base URL for the given HTTP request context.
// It takes into account that the app may be exposed by a reverse-proxy under a different protocol, host and path.
// The returned URL is guaranteed to end with `/`.
// The method should be used in conjunction with the StripPathPrefix middleware.
func BaseURL(c *fiber.Ctx) string {
	path := c.Path()
	origPath := c.OriginalURL()

	if path != origPath && strings.HasSuffix(origPath, path) {
		pathPrefix := origPath[:len(origPath)-len(path)+1]

		return c.BaseURL() + pathPrefix
	}

	return c.BaseURL() + "/"
}
