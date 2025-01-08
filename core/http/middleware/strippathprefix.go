package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"
)

// StripPathPrefix returns a middleware that strips a path prefix from the request path.
// The path prefix is obtained from the X-Forwarded-Prefix HTTP request header.
func StripPathPrefix() fiber.Handler {
	return func(c *fiber.Ctx) error {
		for _, prefix := range c.GetReqHeaders()["X-Forwarded-Prefix"] {
			if prefix != "" {
				path := c.Path()
				pos := len(prefix)

				if prefix[pos-1] == '/' {
					pos--
				} else {
					prefix += "/"
				}

				if strings.HasPrefix(path, prefix) {
					c.Path(path[pos:])
					break
				} else if prefix[:pos] == path {
					c.Redirect(prefix)
					return nil
				}
			}
		}

		return c.Next()
	}
}
