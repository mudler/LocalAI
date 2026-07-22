package middleware

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

// DefaultCompressionMinLength is the response size (in bytes) below which
// gzip is skipped. Anything smaller tends to grow once the ~20 byte gzip
// envelope and the CPU cost on both ends are accounted for.
const DefaultCompressionMinLength = 1024

// streamingPathPrefixes lists routes that either stream incrementally
// (SSE / chunked token deltas) or hand the connection off entirely
// (WebSocket, WebRTC signalling). Buffering those behind a gzip writer
// defeats incremental flushing: the client sits on an empty buffer until
// enough bytes accumulate to fill a deflate block, which reads as a hung
// stream. They are excluded by path because whether a completion request
// streams is decided by the request BODY (`"stream": true`), which the
// compression middleware runs too early to see.
// The unversioned aliases (/chat/completions, /audio/transcriptions, ...) are
// registered alongside the /v1 forms, so both spellings are listed.
var streamingPathPrefixes = []string{
	"/v1/chat/completions",
	"/chat/completions",
	"/v1/completions",
	"/completions",
	"/v1/engines/",
	"/v1/responses",
	"/v1/messages",
	"/v1/realtime",
	"/v1/audio/speech",
	"/audio/speech",
	"/v1/audio/transcriptions",
	"/audio/transcriptions",
	"/api/chat",
	"/api/generate",
	"/api/agent/jobs",
	"/api/backend-logs",
	"/api/node-backend-logs",
	"/ws/",
}

// streamingPathSubstrings catches SSE bridges that carry a variable
// segment before the streaming suffix, e.g. /api/agents/:name/sse.
var streamingPathSubstrings = []string{
	"/sse",
	"/progress",
	"/stream",
	"/events",
}

// precompressedExtensions are formats that already carry their own
// compression. Running deflate over them costs CPU on both ends and makes the
// response marginally LARGER (measured: woff2 font files grow by ~60 bytes),
// so they are served as-is.
var precompressedExtensions = []string{
	".woff", ".woff2", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif",
	".ico", ".mp3", ".mp4", ".webm", ".ogg", ".zip", ".gz", ".br", ".zst",
}

// skipCompression reports whether a request must bypass gzip.
func skipCompression(c echo.Context) bool {
	req := c.Request()

	// An explicit SSE Accept header is the strongest signal available
	// before the handler runs.
	if strings.Contains(req.Header.Get("Accept"), "text/event-stream") {
		return true
	}
	// WebSocket upgrades never carry a compressible body.
	if strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
		return true
	}

	path := req.URL.Path
	for _, p := range streamingPathPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	for _, s := range streamingPathSubstrings {
		if strings.Contains(path, s) {
			return true
		}
	}

	ext := strings.ToLower(filepath.Ext(path))
	if ext != "" && slices.Contains(precompressedExtensions, ext) {
		return true
	}
	return false
}

// Compression returns gzip middleware tuned for LocalAI's traffic mix. The
// React UI ships ~1.8 MB of JS/CSS that compresses to roughly a fifth of
// that, and the admin JSON endpoints are similarly text-heavy, so the win
// is large; streaming routes are excluded via skipCompression.
//
// minLength <= 0 falls back to DefaultCompressionMinLength.
func Compression(minLength int) echo.MiddlewareFunc {
	if minLength <= 0 {
		minLength = DefaultCompressionMinLength
	}
	return middleware.GzipWithConfig(middleware.GzipConfig{
		Skipper:   skipCompression,
		MinLength: minLength,
	})
}
