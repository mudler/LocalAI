package middleware_test

// Route-level integration coverage for the X-LocalAI-Node middleware.
//
// What this file pins (and why a separate spec on top of the unit tests
// in node_header_test.go):
//
//   - The unit tests in node_header_test.go exercise the wrapper by
//     invoking `mw(handler)(c)` directly against a hand-built
//     echo.Context. That misses regressions where the contract between
//     the real Echo router and the wrapper breaks: e.g. middleware
//     installation via e.Use() loses the wrapper because the framework
//     re-decorates c.Response().Writer after middleware setup, or a
//     handler that bypasses c.Response().Writer (writing to some other
//     captured surface).
//
//   - This spec dispatches a real HTTP request through e.ServeHTTP into
//     a streaming handler shaped like chat.go's streaming branch: set
//     SSE headers, write chunks via c.Response().Write, Flush. It
//     proves that:
//       1. Middleware installed via e.Use() is on the writer chain
//          when the handler runs.
//       2. The per-request holder attached by ExposeNodeHeader is
//          visible to the handler (and, transitively, to anything that
//          shares the request context, including the SmartRouter).
//       3. The wrapper's lazy maybeSet fires on the first underlying
//          Write/Flush, so X-LocalAI-Node lands on the response map
//          BEFORE the first body byte is committed.
//       4. The header is present in the recorded response (i.e. it
//          isn't dropped because we tried to set it post-WriteHeader).
//
// Out of scope (and why):
//
//   - We do NOT wire core/http/endpoints/openai.ChatEndpoint
//     end-to-end. ChatEndpoint depends on templates.Evaluator, the
//     MCP NATS client, and the LocalAI Assistant holder; standing
//     those up just to assert header ordering is out of proportion to
//     the property under test. The handler used here mirrors
//     chat.go's streaming branch and exercises the SAME middleware ->
//     c.Response().Writer -> SSE write path as production. If
//     chat.go's streaming branch ever stops going through
//     c.Response().Writer (e.g. it starts using a captured raw
//     http.ResponseWriter from a different seam), this test will not
//     notice; guard that with a code review checklist on chat.go.
//
//   - We do NOT spin up the real SmartRouter. The contract between
//     router and middleware is "router calls distributedhdr.Stamp on
//     the request context; middleware reads the resulting holder on
//     first write". A synthetic stamp inside the handler exercises
//     the same code path; the router's own unit/integration tests
//     cover the routing decision itself.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/pkg/distributedhdr"
)

// orderRecorder snapshots the X-LocalAI-Node header value AT THE MOMENT
// the underlying writer is asked to commit each event. Any header set on
// the response map AFTER the first write/flush is dropped on the wire,
// so this is the ground-truth observation a real SSE client would see.
type orderRecorder struct {
	http.ResponseWriter
	mu     sync.Mutex
	events []string
}

func (o *orderRecorder) record(ev string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, ev)
}

func (o *orderRecorder) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]string, len(o.events))
	copy(out, o.events)
	return out
}

func (o *orderRecorder) WriteHeader(code int) {
	o.record(fmt.Sprintf("header:%d:node=%s", code, o.Header().Get(middleware.NodeHeaderName)))
	o.ResponseWriter.WriteHeader(code)
}

func (o *orderRecorder) Write(b []byte) (int, error) {
	o.record(fmt.Sprintf("write:node=%s", o.Header().Get(middleware.NodeHeaderName)))
	return o.ResponseWriter.Write(b)
}

func (o *orderRecorder) Flush() {
	o.record(fmt.Sprintf("flush:node=%s", o.Header().Get(middleware.NodeHeaderName)))
	if f, ok := o.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

var _ = Describe("ExposeNodeHeader middleware (route-level integration)", func() {
	const fakeNodeID = "node-route-7"

	var appCfg *config.ApplicationConfig

	BeforeEach(func() {
		appCfg = config.NewApplicationConfig()
		appCfg.ExposeNodeHeader = true
	})

	It("stamps X-LocalAI-Node before the first SSE byte via the real router + middleware chain", func() {
		// Build a real Echo router. We need the tracker to sit BELOW
		// the ExposeNodeHeader wrapper in the writer chain (so its
		// recorded snapshot reflects what bytes-on-the-wire see AFTER
		// the wrapper has had a chance to stamp the header). Install
		// the tracker via a middleware that runs BEFORE
		// ExposeNodeHeader; Echo's middleware execution order matches
		// e.Use() call order, so the first Use() wraps the OUTER
		// layer of the writer chain (i.e. the wrapper installed by
		// the second Use() wraps the tracker installed by the first).
		var (
			recorderMu sync.Mutex
			tracker    *orderRecorder
		)
		e := echo.New()
		e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				recorderMu.Lock()
				tracker = &orderRecorder{ResponseWriter: c.Response().Writer}
				c.Response().Writer = tracker
				recorderMu.Unlock()
				return next(c)
			}
		})
		e.Use(middleware.ExposeNodeHeader(appCfg))

		e.POST("/v1/chat/completions", func(c echo.Context) error {
			// Simulate the SmartRouter publishing the picked node ID
			// into the per-request holder installed by the middleware.
			// In production this happens inside ModelLoader.Load via
			// distributedhdr.Stamp(ctx, result.Node.ID).
			distributedhdr.Stamp(c.Request().Context(), fakeNodeID)

			// SSE response prelude (same shape as chat.go).
			c.Response().Header().Set("Content-Type", "text/event-stream")
			c.Response().Header().Set("Cache-Control", "no-cache")
			c.Response().Header().Set("Connection", "keep-alive")

			// Emit a handful of SSE chunks. The very first
			// Write/Flush is what triggers the middleware
			// wrapper's maybeSet, so the X-LocalAI-Node header
			// MUST already be on the response map by the time the
			// byte is committed.
			for i := 0; i < 3; i++ {
				_, err := c.Response().Write([]byte(fmt.Sprintf("data: chunk %d\n\n", i)))
				if err != nil {
					return err
				}
				c.Response().Flush()
			}
			return nil
		})

		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(""))
		rec := httptest.NewRecorder()

		e.ServeHTTP(rec, req)

		recorderMu.Lock()
		Expect(tracker).ToNot(BeNil(), "handler must run and install the order recorder")
		events := tracker.snapshot()
		recorderMu.Unlock()

		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Header().Get(middleware.NodeHeaderName)).To(Equal(fakeNodeID),
			"production contract: header must reach the wire on a streamed response")

		Expect(events).ToNot(BeEmpty(),
			"expected at least one underlying-writer event from the streaming handler")

		// The very first observed event is the moment the wrapper
		// commits to the wire. Its recorded node= value is what a
		// real HTTP client would actually see; anything that lands
		// AFTER this byte is invisible.
		first := events[0]
		Expect(first).To(ContainSubstring("node="+fakeNodeID),
			"first writer event must carry the X-LocalAI-Node header (chain: middleware.Use -> e.POST -> handler.Write/Flush); got events: %v", events)

		// Body sanity: SSE chunks made it to the recorder.
		Expect(rec.Body.String()).To(ContainSubstring("data: chunk 0"))
	})
})
