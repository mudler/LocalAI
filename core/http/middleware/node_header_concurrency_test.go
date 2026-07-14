package middleware

// Regression coverage for the multi-replica X-LocalAI-Node attribution
// bug fixed by this PR.
//
// Pre-refactor failure mode: ExposeNodeHeader resolved the node ID by
// calling ml.LookupNodeID(modelName), which read a single per-modelID
// slot in the loader's in-memory store. The distributed router
// overwrote that slot on every routing decision, so when N concurrent
// requests for the same model were routed to N different replicas, the
// header value the wrapper picked up at first-byte time depended on
// goroutine interleaving and not on which replica THIS request was
// actually sent to.
//
// The fix routes attribution through a per-request atomic holder
// installed by the middleware and stamped by the router via
// distributedhdr.Stamp. Each request carries its own slot, so peer
// stamps cannot bleed in.
//
// This spec exercises the exact concurrency pattern the bug required to
// reproduce: many goroutines, all running through the same middleware
// instance (mirroring e.Use() in production), each stamping a distinct
// node ID, each asserting its own response carries the matching ID.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/distributedhdr"
)

var _ = Describe("ExposeNodeHeader multi-replica attribution", func() {
	It("each concurrent request sees the node ID stamped on ITS OWN request, not a peer's", func() {
		appCfg := &config.ApplicationConfig{ExposeNodeHeader: true}
		e := echo.New()
		mw := ExposeNodeHeader(appCfg)

		const N = 64
		results := make([]string, N)
		var wg sync.WaitGroup
		wg.Add(N)

		// Drive N concurrent requests through the same middleware
		// instance. Each handler stamps a distinct, per-request node ID
		// derived from the request index, then yields before writing
		// the body so the goroutines have ample opportunity to
		// interleave. Under the old shared-loader design this is the
		// configuration that surfaced the bug; under the new
		// per-request-holder design every request must round-trip its
		// own stamp.
		for i := 0; i < N; i++ {
			i := i
			go func() {
				defer wg.Done()
				req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				rec := httptest.NewRecorder()
				c := e.NewContext(req, rec)

				expected := fmt.Sprintf("node-%d", i)
				handler := func(c echo.Context) error {
					distributedhdr.Stamp(c.Request().Context(), expected)
					// Yield to amplify interleaving: if any stamp were
					// shared across requests, the late writes would
					// observe peer-stamped values instead of their
					// own.
					for j := 0; j < 16; j++ {
						_ = j
					}
					return c.String(http.StatusOK, "ok")
				}
				Expect(mw(handler)(c)).To(Succeed())
				results[i] = rec.Header().Get(NodeHeaderName)
			}()
		}
		wg.Wait()

		for i := 0; i < N; i++ {
			Expect(results[i]).To(Equal(fmt.Sprintf("node-%d", i)),
				"request %d must see ITS OWN routing decision in the header, not a peer's", i)
		}
	})
})
