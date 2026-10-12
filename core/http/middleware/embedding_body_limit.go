// SPDX-License-Identifier: MIT
package middleware

import (
	"bytes"
	"io"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/pkg/embeddingmedia"
)

// EmbeddingBodyLimit must precede every body-reading middleware, including auth.
// Eager validation prevents auth's model peek from swallowing a reader overflow
// and replacing the body with a partial buffer. Other routes remain untouched.
func EmbeddingBodyLimit(globalMB int) echo.MiddlewareFunc {
	limit := int64(embeddingmedia.MaxBodyBytes)
	if globalMB > 0 && globalMB < int(limit>>20) {
		limit = int64(globalMB) << 20
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if !isEmbeddingRoute(c.Path()) {
				return next(c)
			}
			if c.Request().ContentLength > limit {
				return echo.ErrStatusRequestEntityTooLarge
			}
			body, err := io.ReadAll(io.LimitReader(c.Request().Body, limit+1))
			if int64(len(body)) > limit {
				return echo.ErrStatusRequestEntityTooLarge
			}
			if err != nil {
				return echo.NewHTTPError(http.StatusBadRequest, "invalid_embedding_request")
			}
			c.Request().Body = io.NopCloser(bytes.NewReader(body))
			return next(c)
		}
	}
}
