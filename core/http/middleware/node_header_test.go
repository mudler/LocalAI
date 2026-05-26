package middleware

import (
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/distributedhdr"
)

// orderedWriter records the order in which header-snapshot vs body-byte
// events happen. Used by the streaming spec to assert that the X-LocalAI-Node
// header lands on the response BEFORE the first body byte is committed to
// the underlying writer.
type orderedWriter struct {
	http.ResponseWriter
	events []string
}

func (o *orderedWriter) WriteHeader(code int) {
	o.events = append(o.events, "header:"+http.StatusText(code))
	o.ResponseWriter.WriteHeader(code)
}

func (o *orderedWriter) Write(b []byte) (int, error) {
	// Snapshot the X-LocalAI-Node header value AT THE INSTANT the underlying
	// writer is asked to commit bytes. This is what real HTTP clients
	// effectively observe: anything set on the header map AFTER this point
	// would be silently dropped.
	o.events = append(o.events, "write:node="+o.Header().Get(NodeHeaderName))
	return o.ResponseWriter.Write(b)
}

func (o *orderedWriter) Flush() {
	o.events = append(o.events, "flush:node="+o.Header().Get(NodeHeaderName))
	if f, ok := o.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

var _ = Describe("ExposeNodeHeader middleware", func() {
	const (
		fakeNodeID = "node-abcdef"
	)

	var (
		e      *echo.Echo
		appCfg *config.ApplicationConfig
	)

	BeforeEach(func() {
		e = echo.New()
		appCfg = &config.ApplicationConfig{}
	})

	// run executes the middleware against a fake handler. The handler may
	// reach into the per-request context to stamp the holder (simulating
	// what the distributed router does in production); the wrapper reads
	// the holder lazily on the first underlying write.
	run := func(handler echo.HandlerFunc) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		mw := ExposeNodeHeader(appCfg)
		Expect(mw(handler)(c)).To(Succeed())
		return rec
	}

	When("ExposeNodeHeader is false", func() {
		It("does not set the X-LocalAI-Node header", func() {
			appCfg.ExposeNodeHeader = false

			rec := run(func(c echo.Context) error {
				// Even if a router were to stamp, with the flag off
				// there is no holder on the context so Stamp is a no-op.
				distributedhdr.Stamp(c.Request().Context(), fakeNodeID)
				return c.String(http.StatusOK, "ok")
			})

			Expect(rec.Header().Get(NodeHeaderName)).To(BeEmpty())
		})

		It("does not even install the wrapper (writer is unchanged)", func() {
			appCfg.ExposeNodeHeader = false
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			origWriter := c.Response().Writer

			handler := func(c echo.Context) error {
				// Pass-through must leave the writer identity intact so
				// no overhead is added on the hot path when the feature
				// is off.
				Expect(c.Response().Writer).To(BeIdenticalTo(origWriter))
				// And no holder is attached to the request context.
				Expect(distributedhdr.Holder(c.Request().Context())).To(BeNil())
				return c.String(http.StatusOK, "ok")
			}
			mw := ExposeNodeHeader(appCfg)
			Expect(mw(handler)(c)).To(Succeed())
		})
	})

	When("ExposeNodeHeader is true and the router stamps a node ID", func() {
		It("sets the X-LocalAI-Node header on a buffered response", func() {
			appCfg.ExposeNodeHeader = true

			rec := run(func(c echo.Context) error {
				distributedhdr.Stamp(c.Request().Context(), fakeNodeID)
				return c.String(http.StatusOK, "ok")
			})

			Expect(rec.Header().Get(NodeHeaderName)).To(Equal(fakeNodeID))
		})

		It("sets the header even on a 500 error response (Write still triggers maybeSet)", func() {
			appCfg.ExposeNodeHeader = true

			rec := run(func(c echo.Context) error {
				distributedhdr.Stamp(c.Request().Context(), fakeNodeID)
				return c.String(http.StatusInternalServerError, "boom")
			})

			Expect(rec.Code).To(Equal(http.StatusInternalServerError))
			Expect(rec.Header().Get(NodeHeaderName)).To(Equal(fakeNodeID))
		})

		It("installs a holder on the request context that the router can find", func() {
			appCfg.ExposeNodeHeader = true

			var observed *string
			rec := run(func(c echo.Context) error {
				h := distributedhdr.Holder(c.Request().Context())
				Expect(h).ToNot(BeNil(), "middleware must attach a per-request holder when the flag is on")

				distributedhdr.Stamp(c.Request().Context(), fakeNodeID)
				got := distributedhdr.Load(h)
				observed = &got
				return c.String(http.StatusOK, "ok")
			})

			Expect(observed).ToNot(BeNil())
			Expect(*observed).To(Equal(fakeNodeID))
			Expect(rec.Header().Get(NodeHeaderName)).To(Equal(fakeNodeID))
		})
	})

	When("ExposeNodeHeader is true but the router never stamps", func() {
		It("does not set the header (in-process model, not distributed)", func() {
			appCfg.ExposeNodeHeader = true

			rec := run(func(c echo.Context) error {
				// Holder is present but nothing ever stamps it - this is
				// the in-process / non-distributed path.
				Expect(distributedhdr.Holder(c.Request().Context())).ToNot(BeNil())
				return c.String(http.StatusOK, "ok")
			})

			Expect(rec.Header().Get(NodeHeaderName)).To(BeEmpty())
		})
	})

	When("the handler streams via Flush before any Write", func() {
		It("sets the header BEFORE the first byte hits the underlying writer", func() {
			appCfg.ExposeNodeHeader = true

			// Wrap the recorder with an order-tracking writer so we can
			// assert that the header is on the response map by the time
			// the first body byte is committed. This is the property
			// that protected the pre-refactor streaming bug: if the
			// wrapper stamped lazily but AFTER the byte commit, real
			// SSE clients would see the body without the header.
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			rec := httptest.NewRecorder()
			tracker := &orderedWriter{ResponseWriter: rec}
			c := e.NewContext(req, rec)
			c.Response().Writer = tracker

			handler := func(c echo.Context) error {
				// Simulate the router publishing the picked node ID
				// mid-request, then an SSE stream emitting chunks.
				distributedhdr.Stamp(c.Request().Context(), fakeNodeID)
				c.Response().Header().Set("Content-Type", "text/event-stream")
				c.Response().Flush()
				_, err := c.Response().Write([]byte("data: chunk\n\n"))
				return err
			}

			mw := ExposeNodeHeader(appCfg)
			Expect(mw(handler)(c)).To(Succeed())

			// First recorded event on the underlying writer must show
			// the header already populated. The first event is either
			// flush or write; either way the node ID must be on it.
			Expect(tracker.events).ToNot(BeEmpty())
			Expect(tracker.events[0]).To(HavePrefix("flush:node=" + fakeNodeID))
			Expect(rec.Header().Get(NodeHeaderName)).To(Equal(fakeNodeID))
		})
	})

	When("the handler writes a body without an explicit WriteHeader", func() {
		It("still stamps the header before the implicit 200 commit", func() {
			appCfg.ExposeNodeHeader = true

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			rec := httptest.NewRecorder()
			tracker := &orderedWriter{ResponseWriter: rec}
			c := e.NewContext(req, rec)
			c.Response().Writer = tracker

			handler := func(c echo.Context) error {
				distributedhdr.Stamp(c.Request().Context(), fakeNodeID)
				_, err := c.Response().Write([]byte("body"))
				return err
			}

			mw := ExposeNodeHeader(appCfg)
			Expect(mw(handler)(c)).To(Succeed())

			// Echo's Response.Write calls WriteHeader on the underlying
			// writer first, then Write. Both must see the header
			// already populated (the wrapper's maybeSet ran inside both
			// WriteHeader and Write before they hit `tracker`).
			Expect(len(tracker.events)).To(BeNumerically(">=", 2))
			Expect(tracker.events[0]).To(HavePrefix("header:"))
			Expect(tracker.events[1]).To(Equal("write:node=" + fakeNodeID))
			Expect(rec.Header().Get(NodeHeaderName)).To(Equal(fakeNodeID))
		})
	})

	When("the router stamps after request entry but before first write", func() {
		It("uses the value present AT the first write (late binding)", func() {
			appCfg.ExposeNodeHeader = true

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := func(c echo.Context) error {
				// Simulate the router making a routing decision after
				// the handler has already started running but before the
				// first byte hits the wire. The wrapper must read the
				// holder lazily, not eagerly at request entry.
				distributedhdr.Stamp(c.Request().Context(), "fresh-node-B")
				return c.String(http.StatusOK, "ok")
			}

			mw := ExposeNodeHeader(appCfg)
			Expect(mw(handler)(c)).To(Succeed())

			Expect(rec.Header().Get(NodeHeaderName)).To(Equal("fresh-node-B"),
				"the wrapper must read the node ID lazily at first write, not eagerly at entry")
		})
	})

})
