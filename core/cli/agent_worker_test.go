package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// The agent worker answers its MCP verbs on ONE carrier now: the control route
// on the tunnel it holds. The queue-group subjects these used to arrive on are
// gone, because a queue group was only ever a way of SELECTING a worker, and
// the frontend now makes that selection itself (nodes.AgentSelector).
//
// What these specs pin is the split between an ANSWER and a FAILURE TO SERVE. A
// tool that ran and failed is the worker's own verdict and travels inside the
// reply, on a 200; a verb this worker could not serve at all is a Go error,
// which becomes a non-2xx, and which nothing may read as evidence about
// anything.
var _ = Describe("The agent worker's MCP verbs", func() {
	It("answers a tool request it could not decode, rather than failing to serve it", func() {
		// The decode happened on this worker and its outcome is something the
		// worker LEARNED, so it belongs in the reply. Returned as an error it
		// would become "no route to that worker" at the frontend.
		raw, err := serveMCPToolRequest(context.Background(), json.RawMessage(`{"tool_name":`))
		Expect(err).ToNot(HaveOccurred())

		var resp mcpRemote.MCPToolResponse
		Expect(json.Unmarshal(raw, &resp)).To(Succeed())
		Expect(resp.Error).To(ContainSubstring("unmarshal error"))
		Expect(resp.Result).To(BeEmpty())
	})

	It("answers a discovery request it could not decode the same way", func() {
		raw, err := serveMCPDiscoveryRequest(context.Background(), json.RawMessage(`not json`))
		Expect(err).ToNot(HaveOccurred())

		var resp mcpRemote.MCPDiscoveryResponse
		Expect(json.Unmarshal(raw, &resp)).To(Succeed())
		Expect(resp.Error).To(ContainSubstring("unmarshal error"))
	})

})

var _ = Describe("The agent worker's backend stop", func() {
	// One carrier now: the tunnel's control route. The node subject this used
	// to arrive on is gone, so this implementation is reached one way only.

	It("treats a stop that names no backend as a no-op rather than a failure", func() {
		// A malformed request must not become a non-2xx the frontend reads as
		// a worker it could not reach.
		Expect(dropMCPSessionsForBackend(context.Background(), messaging.BackendStopRequest{})).To(Succeed())
	})

	It("succeeds for a backend it holds no sessions for", func() {
		// The ordinary case on a worker that never touched that backend. An
		// error here would be reported as this worker failing to serve the
		// verb, on every stop of every backend it does not know about.
		Expect(dropMCPSessionsForBackend(context.Background(),
			messaging.BackendStopRequest{Backend: "a-backend-this-worker-never-saw"})).To(Succeed())
	})
})

// The wiring, pinned through the transport rather than by reading the struct.
//
// Every verb below has exactly one carrier: the control route. Dropping a field
// from agentWorkerControlHandlers mounts nothing for that path, the catch-all
// answers 404, and the frontend reads that 404 as a worker too old to serve the
// verb rather than as a wiring mistake. Nothing else in this repo would notice.
var _ = Describe("The agent worker's control-plane wiring", func() {
	var base string

	BeforeEach(func() {
		mux := http.NewServeMux()
		agentWorkerControlHandlers().Register(mux)
		srv := httptest.NewServer(mux)
		DeferCleanup(srv.Close)
		base = srv.URL
	})

	DescribeTable("mounts the verb an agent worker is the only server of",
		func(path string) {
			resp, err := http.Post(base+path, "application/json", strings.NewReader(`{}`)) //nolint:gosec,noctx // httptest server, no redirects to follow
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = resp.Body.Close() })
			Expect(resp.StatusCode).ToNot(Equal(http.StatusNotFound),
				"%s is not mounted; agentWorkerControlHandlers does not wire its handler", path)
		},
		Entry("backend stop", workerctl.PathBackendStop),
		Entry("mcp tool execute", workerctl.PathMCPToolExecute),
		Entry("mcp discovery", workerctl.PathMCPDiscovery),
	)
})
