package middleware

import (
	"bufio"
	"fmt"
	"net"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/distributedhdr"
)

// NodeHeaderName is the HTTP response header that, when --expose-node-header
// is enabled, carries the ID of the distributed-mode worker node that served
// the inference request. Off by default: node IDs reveal internal topology
// and should not be exposed on a public endpoint.
const NodeHeaderName = "X-LocalAI-Node"

// nodeHeaderWriter wraps an http.ResponseWriter and stamps the X-LocalAI-Node
// header lazily on the first Write / WriteHeader / Flush call. The lazy
// resolve is what makes this work for streaming: the picked node ID is only
// known AFTER the router runs (i.e. on the first SSE chunk), so resolving at
// request entry would attach the previous request's routing decision (or
// nothing on a cold cache).
type nodeHeaderWriter struct {
	http.ResponseWriter
	resolve func() string
	set     bool
}

func (w *nodeHeaderWriter) maybeSet() {
	if w.set {
		return
	}
	w.set = true
	if id := w.resolve(); id != "" {
		w.Header().Set(NodeHeaderName, id)
	}
}

func (w *nodeHeaderWriter) Write(b []byte) (int, error) {
	w.maybeSet()
	return w.ResponseWriter.Write(b)
}

func (w *nodeHeaderWriter) WriteHeader(code int) {
	w.maybeSet()
	w.ResponseWriter.WriteHeader(code)
}

// Flush keeps SSE handlers working: Echo's Response.Flush goes through
// http.NewResponseController which walks Unwrap() chains and invokes Flush
// on the first wrapper that implements http.Flusher. By implementing it
// here we both stamp the header before the underlying writer flushes AND
// keep the streaming path alive.
func (w *nodeHeaderWriter) Flush() {
	w.maybeSet()
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack preserves WebSocket / raw-conn handlers that need to take over the
// underlying TCP connection (e.g. /v1/realtime). Without this the wrapper
// would silently break those endpoints.
//
// When the underlying writer does not implement http.Hijacker we return
// http.ErrNotSupported so callers using errors.Is (notably
// http.NewResponseController.Hijack) detect the condition through the
// standard sentinel rather than a string-matched custom error.
func (w *nodeHeaderWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("hijack not supported: %w", http.ErrNotSupported)
}

// Unwrap lets http.NewResponseController reach through us to find optional
// interfaces (CloseNotifier, SetReadDeadline, etc.) on the real writer.
func (w *nodeHeaderWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// ExposeNodeHeader installs a per-request response writer wrapper that
// stamps the X-LocalAI-Node header from the per-request holder published
// by the distributed router on the first write. Off by default; opted in
// via --expose-node-header / LOCALAI_EXPOSE_NODE_HEADER.
//
// Attribution is per-request correct: the middleware creates a fresh
// holder per request, plumbs it through context.Context, and the router
// writes the picked node ID for THIS request's routing decision. No
// shared loader state, no overwriting across concurrent requests for the
// same model on multiple replicas.
func ExposeNodeHeader(appCfg *config.ApplicationConfig) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if appCfg == nil || !appCfg.ExposeNodeHeader {
				return next(c)
			}

			// One holder per request. The pointer is captured both in
			// the wrapper closure (read side) and in the request
			// context (write side, accessed by the router via
			// distributedhdr.Stamp). Both sides point at the same
			// atomic slot.
			holder := distributedhdr.NewHolder()

			req := c.Request()
			c.SetRequest(req.WithContext(distributedhdr.WithHolder(req.Context(), holder)))

			orig := c.Response().Writer
			wrapper := &nodeHeaderWriter{
				ResponseWriter: orig,
				resolve: func() string {
					return distributedhdr.Load(holder)
				},
			}
			c.Response().Writer = wrapper
			defer func() {
				c.Response().Writer = orig
			}()
			return next(c)
		}
	}
}
