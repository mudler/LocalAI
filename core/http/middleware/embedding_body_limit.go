// SPDX-License-Identifier: MIT
package middleware

import (
	"io"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/pkg/embeddingmedia"
)

const embeddingBodyContextKey = "localai_embedding_body_limit"

// embeddingLimitedBody caps a request body lazily. It never buffers: bytes are
// pulled only when a consumer reads, and at most limit+1 bytes ever leave the
// underlying body. Overflow is sticky and recorded, so a consumer that swallows
// the read error (for example a model-name peek) cannot hide it from the binder.
type embeddingLimitedBody struct {
	body     io.ReadCloser
	limit    int64
	read     int64
	overflow bool
}

func (b *embeddingLimitedBody) Read(p []byte) (int, error) {
	if b.overflow {
		return 0, echo.ErrStatusRequestEntityTooLarge
	}
	remaining := b.limit - b.read
	if remaining <= 0 {
		// Probe one byte to tell an exact fit from an overflow.
		var probe [1]byte
		n, err := b.body.Read(probe[:])
		if n > 0 {
			b.overflow = true
			return 0, echo.ErrStatusRequestEntityTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := b.body.Read(p)
	b.read += int64(n)
	return n, err
}

func (b *embeddingLimitedBody) Close() error { return b.body.Close() }

// embeddingBodyOverflowed reports whether the lazy cap was exceeded.
func embeddingBodyOverflowed(c echo.Context) bool {
	b, ok := c.Get(embeddingBodyContextKey).(*embeddingLimitedBody)
	return ok && b.overflow
}

// EmbeddingBodyLimit must precede every body-reading middleware, including auth.
// It reads nothing itself: a known oversized Content-Length is rejected at once,
// anything else is wrapped in a lazy reader that fails past the cap. The binder
// turns a recorded overflow into HTTP 413. Other routes remain untouched.
func EmbeddingBodyLimit(globalMB int) echo.MiddlewareFunc {
	limit := int64(embeddingmedia.MaxBodyBytes)
	// The global limiter parses "<N>M" as N*1,000,000 bytes. Use the same
	// figure so the lazy cap fires no later than the global one.
	if global := int64(globalMB) * 1000000; globalMB > 0 && global < limit {
		limit = global
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if !isBoundedMultimodalRoute(c.Path()) {
				return next(c)
			}
			if c.Request().ContentLength > limit {
				return echo.ErrStatusRequestEntityTooLarge
			}
			limited := &embeddingLimitedBody{body: c.Request().Body, limit: limit}
			c.Request().Body = limited
			c.Set(embeddingBodyContextKey, limited)
			return next(c)
		}
	}
}
