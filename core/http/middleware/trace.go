package middleware

import (
	"bufio"
	"bytes"
	"io"
	"mime"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/emirpasic/gods/v2/queues/circularbuffer"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/xlog"
)

type APIExchangeRequest struct {
	Method        string       `json:"method"`
	Path          string       `json:"path"`
	Headers       *http.Header `json:"headers"`
	Body          *[]byte      `json:"body"`
	BodyTruncated bool         `json:"body_truncated,omitempty"`
	BodyBytes     int          `json:"body_bytes,omitempty"` // original size before truncation
}

type APIExchangeResponse struct {
	Status        int          `json:"status"`
	Headers       *http.Header `json:"headers"`
	Body          *[]byte      `json:"body"`
	BodyTruncated bool         `json:"body_truncated,omitempty"`
	BodyBytes     int          `json:"body_bytes,omitempty"` // original size before truncation
}

type APIExchange struct {
	Timestamp time.Time           `json:"timestamp"`
	Duration  time.Duration       `json:"duration"`
	Request   APIExchangeRequest  `json:"request"`
	Response  APIExchangeResponse `json:"response"`
	Error     string              `json:"error,omitempty"`
	UserID    string              `json:"user_id,omitempty"`
	UserName  string              `json:"user_name,omitempty"`
	// ClientIP is the caller's address as resolved by echo (honours
	// X-Forwarded-For / X-Real-IP behind a trusted proxy), and UserAgent
	// is the raw User-Agent header. Both are surfaced in the admin Traces
	// UI so an operator can tell who/what issued each request.
	ClientIP  string `json:"client_ip,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
}

var traceBuffer *circularbuffer.Queue[APIExchange]
var mu sync.Mutex
var logChan = make(chan APIExchange, 100)
var tracingMaxItems int

var doInitializeTracing = sync.OnceFunc(func() {
	maxItems := tracingMaxItems
	if maxItems <= 0 {
		maxItems = 100
	}
	mu.Lock()
	traceBuffer = circularbuffer.New[APIExchange](maxItems)
	mu.Unlock()

	go func() {
		for exchange := range logChan {
			mu.Lock()
			if traceBuffer != nil {
				traceBuffer.Enqueue(exchange)
			}
			mu.Unlock()
		}
	}()
})

type bodyWriter struct {
	http.ResponseWriter
	body       *bytes.Buffer
	maxBytes   int // 0 = unlimited capture
	truncated  bool
	totalBytes int // bytes the upstream handler wrote, even past the cap
}

func (w *bodyWriter) Write(b []byte) (int, error) {
	// Capture into the trace buffer up to maxBytes, then drop the overflow
	// so a chatty endpoint can't grow the buffer without bound. The full
	// payload still flows through to the real client below.
	w.totalBytes += len(b)
	if w.maxBytes <= 0 {
		w.body.Write(b)
	} else if remain := w.maxBytes - w.body.Len(); remain > 0 {
		if remain >= len(b) {
			w.body.Write(b)
		} else {
			w.body.Write(b[:remain])
			w.truncated = true
		}
	} else {
		w.truncated = true
	}
	return w.ResponseWriter.Write(b)
}

func (w *bodyWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// truncateForTrace returns a defensive copy of body capped at maxBytes,
// and a flag indicating whether the cap forced truncation. maxBytes <= 0
// disables the cap.
func truncateForTrace(body []byte, maxBytes int) ([]byte, bool) {
	if maxBytes <= 0 || len(body) <= maxBytes {
		out := make([]byte, len(body))
		copy(out, body)
		return out, false
	}
	out := make([]byte, maxBytes)
	copy(out, body[:maxBytes])
	return out, true
}

// Hijack lets WebSocket upgraders (gorilla/websocket) reach the
// underlying connection. Without this, gorilla's Hijacker type-assertion
// fails on the wrapped writer and the handshake returns 500.
func (w *bodyWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

func initializeTracing(maxItems int) {
	tracingMaxItems = maxItems
	doInitializeTracing()
}

// sensitiveTraceHeaders is the set of header names whose values must not
// land in the in-memory trace buffer. Keys are canonical — http.Header
// stores them that way, so range yields canonical keys directly.
var sensitiveTraceHeaders = map[string]struct{}{
	"Authorization":       {},
	"Proxy-Authorization": {},
	"Cookie":              {},
	"Set-Cookie":          {},
	"X-Api-Key":           {},
	"Xi-Api-Key":          {},
	"X-Auth-Token":        {},
}

func redactSensitiveHeaders(h http.Header) http.Header {
	out := h.Clone()
	for k := range out {
		if _, ok := sensitiveTraceHeaders[k]; ok {
			out[k] = []string{"[redacted]"}
		}
	}
	return out
}

// TraceMiddleware intercepts and logs JSON API requests and responses
func TraceMiddleware(app *application.Application) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if !app.ApplicationConfig().EnableTracing {
				return next(c)
			}

			initializeTracing(app.ApplicationConfig().TracingMaxItems)

			ct, _, _ := mime.ParseMediaType(c.Request().Header.Get("Content-Type"))
			if ct != "application/json" {
				return next(c)
			}

			body, err := io.ReadAll(c.Request().Body)
			if err != nil {
				xlog.Error("Failed to read request body")
				return err
			}

			// Restore the body for downstream handlers
			c.Request().Body = io.NopCloser(bytes.NewBuffer(body))

			startTime := time.Now()

			// Cap captured payload size. Without this, /embeddings and
			// streaming /chat/completions blow the in-memory buffer into the
			// tens of MB, which then locks the admin Traces UI fetching the
			// JSON dump faster than the 5s auto-refresh.
			maxBodyBytes := app.ApplicationConfig().TracingMaxBodyBytes

			// Wrap response writer to capture body
			resBody := new(bytes.Buffer)
			mw := &bodyWriter{
				ResponseWriter: c.Response().Writer,
				body:           resBody,
				maxBytes:       maxBodyBytes,
			}
			c.Response().Writer = mw

			handlerErr := next(c)

			// Restore original writer unconditionally
			c.Response().Writer = mw.ResponseWriter

			// Determine response status (use 500 if handler errored and no status was set)
			status := c.Response().Status
			if status == 0 && handlerErr != nil {
				status = http.StatusInternalServerError
			}

			// Create exchange log (always, even on error). Sensitive headers
			// (Authorization, API keys, cookies) are redacted before storage —
			// the trace endpoint is admin-only but the buffer is also reachable
			// via any heap-dump-style introspection, and tokens shouldn't
			// outlive the request that carried them.
			requestHeaders := redactSensitiveHeaders(c.Request().Header)
			requestBody, requestTruncated := truncateForTrace(body, maxBodyBytes)
			responseHeaders := redactSensitiveHeaders(c.Response().Header())
			responseBody := make([]byte, resBody.Len())
			copy(responseBody, resBody.Bytes())
			exchange := APIExchange{
				Timestamp: startTime,
				Duration:  time.Since(startTime),
				ClientIP:  c.RealIP(),
				UserAgent: c.Request().UserAgent(),
				Request: APIExchangeRequest{
					Method:        c.Request().Method,
					Path:          c.Path(),
					Headers:       &requestHeaders,
					Body:          &requestBody,
					BodyTruncated: requestTruncated,
					BodyBytes:     len(body),
				},
				Response: APIExchangeResponse{
					Status:        status,
					Headers:       &responseHeaders,
					Body:          &responseBody,
					BodyTruncated: mw.truncated,
					BodyBytes:     mw.totalBytes,
				},
			}
			if handlerErr != nil {
				exchange.Error = handlerErr.Error()
			}

			if user := auth.GetUser(c); user != nil {
				exchange.UserID = user.ID
				exchange.UserName = user.Name
			}

			select {
			case logChan <- exchange:
			default:
				xlog.Warn("Trace channel full, dropping trace")
			}

			return handlerErr
		}
	}
}

// GetTraces returns a copy of the logged API exchanges for display
func GetTraces() []APIExchange {
	mu.Lock()
	if traceBuffer == nil {
		mu.Unlock()
		return []APIExchange{}
	}
	traces := traceBuffer.Values()
	mu.Unlock()

	slices.SortFunc(traces, func(a, b APIExchange) int {
		return b.Timestamp.Compare(a.Timestamp)
	})

	return traces
}

// ClearTraces clears the in-memory logs
func ClearTraces() {
	mu.Lock()
	if traceBuffer != nil {
		traceBuffer.Clear()
	}
	mu.Unlock()
}
