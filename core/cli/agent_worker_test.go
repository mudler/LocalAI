package cli

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/messaging"
)

// The agent worker answers its MCP verbs on two carriers at once: the NATS
// subject it has always answered, and the control route on the tunnel it now
// holds. These specs pin the two properties that stops those drifting apart.
//
// The first is that there is ONE implementation. A frontend that reaches a
// worker over the bus and one that reaches the same worker over its tunnel must
// get the same bytes, because during this migration both are live and which one
// is used is not a decision anybody makes deliberately.
//
// The second is the split between an ANSWER and a FAILURE TO SERVE. A tool that
// ran and failed is the worker's own verdict and travels inside the reply; a
// verb this worker could not serve at all is a Go error, which becomes a
// non-2xx over the tunnel and silence on the bus, and which nothing may read as
// evidence about anything.
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

	It("puts on the bus exactly the bytes the tunnel route returns", func() {
		// The one property that keeps the two carriers honest. A second
		// implementation for the bus is how a deployment ends up behaving
		// differently depending on which one a frontend happened to pick.
		request := json.RawMessage(`{"tool_name":`)
		overTunnel, err := serveMCPToolRequest(context.Background(), request)
		Expect(err).ToNot(HaveOccurred())

		sent := make(chan []byte, 1)
		replyOverNATS("mcp.tools.execute", serveMCPToolRequest)(request, func(b []byte) { sent <- b })

		var overBus []byte
		Eventually(sent).Should(Receive(&overBus))
		Expect(string(overBus)).To(Equal(string(overTunnel)))
	})

	It("sends nothing on the bus when the verb could not be served", func() {
		// A requester reads the silence as a timeout, which is the closest the
		// bus has to "this worker did not answer". Inventing a reply body would
		// put a failure to serve into the bucket reserved for the worker's own
		// verdict, which is the collapse this whole phase exists to prevent.
		sent := make(chan []byte, 1)
		failing := func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return nil, context.DeadlineExceeded
		}

		replyOverNATS("mcp.tools.execute", failing)(json.RawMessage(`{}`), func(b []byte) { sent <- b })

		Expect(sent).ToNot(Receive())
	})
})

var _ = Describe("The agent worker's backend stop", func() {
	// One implementation behind both carriers, for the same reason: the bus
	// subscription and the tunnel's control route both call this.

	It("treats a stop that names no backend as a no-op rather than a failure", func() {
		// A malformed publish must not become a non-2xx the frontend reads as
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
