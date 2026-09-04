package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/agents"
	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
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
		// Built exactly as Run builds it, from an executor and the MCP CI
		// timeout, so a field this function forgets to set is a 404 here.
		executor := agents.NewWorkerExecutor(
			agents.NewEventBridge(testutil.NewFakeBus(), nil, "agent-worker-spec"),
			nil, "http://127.0.0.1:1", "token")
		agentWorkerControlHandlers(executor, "http://127.0.0.1:1", "token", time.Second).Register(mux)
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
		// The two verbs that replaced the queue groups. An unwired one answers
		// a 404, which is EXACTLY what an older worker answers, so nothing else
		// in the tree can tell the two apart and only this spec can.
		Entry("agent execute", workerctl.PathAgentExecute),
		Entry("mcp ci run", workerctl.PathMCPCIRun),
	)

	DescribeTable("answers a dispatched verb as a stream, so progress and the terminal line share one body",
		func(path string) {
			resp, err := http.Post(base+path, "application/json", strings.NewReader(`{}`)) //nolint:gosec,noctx // httptest server, no redirects to follow
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = resp.Body.Close() })
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(resp.Header.Get("Content-Type")).To(Equal(workerctl.ContentTypeStream))

			// Exactly one reply line, and it is the last thing on the body.
			// That is what the claiming replica stops reading on.
			var envs []workerctl.Envelope
			sc := bufio.NewScanner(resp.Body)
			for sc.Scan() {
				if strings.TrimSpace(sc.Text()) == "" {
					continue
				}
				var env workerctl.Envelope
				Expect(json.Unmarshal(sc.Bytes(), &env)).To(Succeed())
				envs = append(envs, env)
			}
			Expect(sc.Err()).ToNot(HaveOccurred())
			Expect(envs).ToNot(BeEmpty())
			Expect(envs[len(envs)-1].Reply).ToNot(BeEmpty(), "the reply line must be last")
			for _, env := range envs[:len(envs)-1] {
				Expect(env.Reply).To(BeEmpty(), "only the last line may be a reply")
			}
		},
		Entry("agent execute", workerctl.PathAgentExecute),
		Entry("mcp ci run", workerctl.PathMCPCIRun),
	)
})

// The last reason an agent worker dials a message bus, pinned so that removing
// it is a decision rather than an accident.
//
// Every verb a frontend addresses to THIS worker now arrives on the tunnel it
// dials, and the two queue groups it used to join are gone. One family is left,
// in the other direction: agent.<name>.cancel. Its publisher is a frontend
// replica and its ONLY subscriber is this process, and a worker has no database
// and so cannot join the PostgreSQL carrier every other family moved to. A
// worker that came up without a bus would register, serve, run agents, and
// ignore every cancel, returning nothing to say so - the cancel would be
// published, would succeed, and would reach nobody.
//
// So --nats-url stays required here, and it is required for this and for
// nothing else. When a cancel rides the tunnel as a control verb, this spec is
// what has to be deleted for the flag to become optional, and deleting it is
// then the visible half of that change.
var _ = Describe("The agent worker's remaining bus requirement", func() {
	parse := func(args ...string) error {
		// kong resolves env: tags from the process environment, and a
		// LOCALAI_NATS_URL that is SET BUT EMPTY satisfies a required flag.
		// Left in place, this spec would pass on a developer's shell and on
		// nothing else.
		if prior, had := os.LookupEnv("LOCALAI_NATS_URL"); had {
			Expect(os.Unsetenv("LOCALAI_NATS_URL")).To(Succeed())
			DeferCleanup(func() { _ = os.Setenv("LOCALAI_NATS_URL", prior) })
		}
		var cli struct {
			AgentWorker AgentWorkerCMD `cmd:""`
		}
		parser, err := kong.New(&cli)
		Expect(err).ToNot(HaveOccurred())
		_, err = parser.Parse(append([]string{"agent-worker"}, args...))
		return err
	}

	It("refuses to start without a bus to hear cancels on", func() {
		// Refused at parse time and not at first use. A worker that started
		// and only failed to subscribe would already have registered itself as
		// available to run agents nobody can cancel.
		Expect(parse("--register-to", "http://frontend:8080")).
			To(MatchError(ContainSubstring("--nats-url")),
				"the agent worker started with no bus: every cancel of an agent it runs would be published to nobody and reported as sent")
	})

	It("parses once the bus is named", func() {
		Expect(parse("--register-to", "http://frontend:8080", "--nats-url", "nats://bus:4222")).To(Succeed())
	})
})
