package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/agents"
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
	var bridge *agents.EventBridge

	BeforeEach(func() {
		mux := http.NewServeMux()
		// Built exactly as Run builds it, from an executor and the MCP CI
		// timeout, so a field this function forgets to set is a 404 here.
		bridge = agents.NewWorkerEventBridge("agent-worker-spec")
		executor := agents.NewWorkerExecutor(bridge, nil, "http://127.0.0.1:1", "token")
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
		// The verb that removed this process's last reason to dial a bus.
		// Unwired it answers a 404, which the frontend reads as a worker too
		// old to serve it, and every cancel of an agent this worker is running
		// is then reported as one that could not be delivered - for ever.
		Entry("agent cancel", workerctl.PathAgentCancel),
	)

	// The cancel verb end to end through the mux, because the thing that must
	// be true is that the path reaches the SAME cancel registry the executor
	// registers a run on. Two bridges would compile, mount, answer 200 and
	// cancel nothing.
	It("cancels a run registered on the executor's own bridge, and says so", func() {
		cancelled := make(chan struct{})
		bridge.RegisterCancel("msg-1", func() { close(cancelled) })

		post := func(messageID string) messaging.AgentCancelReply {
			GinkgoHelper()
			body, err := json.Marshal(messaging.AgentCancelRequest{AgentName: "a1", UserID: "u1", MessageID: messageID})
			Expect(err).ToNot(HaveOccurred())
			resp, err := http.Post(base+workerctl.PathAgentCancel, "application/json", bytes.NewReader(body)) //nolint:gosec,noctx // httptest server, no redirects to follow
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = resp.Body.Close() })
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			var reply messaging.AgentCancelReply
			Expect(json.NewDecoder(resp.Body).Decode(&reply)).To(Succeed())
			return reply
		}

		Expect(post("msg-nobody-is-running").Cancelled).To(BeFalse(),
			"a worker answers only for itself, and false is that answer rather than a failure")
		Expect(post("msg-1").Cancelled).To(BeTrue())
		Eventually(cancelled, "20s").Should(BeClosed())
	})

	It("fails to serve a cancel it cannot read, rather than answering that it found nothing", func() {
		// The two are different facts. A 200 with cancelled false would be read
		// as this worker's own answer about the run; a body it could not decode
		// is not an answer about anything.
		resp, err := http.Post(base+workerctl.PathAgentCancel, "application/json", strings.NewReader(`{"message_id":`)) //nolint:gosec,noctx // httptest server, no redirects to follow
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = resp.Body.Close() })
		Expect(resp.StatusCode).To(Equal(http.StatusInternalServerError))
	})

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

// The agent worker dials NO message bus, pinned so that a flag reappearing as
// required is a decision rather than an accident.
//
// Every verb a frontend addresses to this worker arrives on the tunnel it
// dials, and that now includes the cancel: agent.<name>.cancel was the last
// family in the other direction, from a frontend replica to whichever worker
// held the execution, and it could not move to the broadcast carrier because
// that carrier rides PostgreSQL and this process has no database. It is a
// control verb on the worker's own tunnel instead.
//
// --nats-url is still ACCEPTED, and ignored, so an existing command line or
// unit file starts unchanged.
var _ = Describe("The agent worker's bus requirement", func() {
	parse := func(args ...string) error {
		// kong resolves env: tags from the process environment, so a
		// LOCALAI_NATS_URL inherited from a developer's shell would make the
		// first spec below pass for the wrong reason.
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

	It("starts with no bus named at all", func() {
		Expect(parse("--register-to", "http://frontend:8080")).To(Succeed(),
			"an agent worker connects to no message bus and must not demand the URL of one")
	})

	It("still accepts a command line that names one", func() {
		// Ignored, not rejected. An operator upgrading a fleet must not have to
		// edit every unit file in the same change.
		Expect(parse("--register-to", "http://frontend:8080", "--nats-url", "nats://bus:4222")).To(Succeed())
	})

	It("still accepts the broker credentials that came with it", func() {
		// The whole set, because an operator's unit file carries the whole set:
		// a command line that parses --nats-url and then dies on --nats-jwt has
		// bought the fleet nothing.
		Expect(parse("--register-to", "http://frontend:8080",
			"--nats-url", "nats://bus:4222",
			"--nats-jwt", "eyJ0",
			"--nats-user-seed", "SUUSER",
			"--nats-service-jwt", "eyJ0",
			"--nats-service-seed", "SUSERVICE",
			"--nats-require-auth")).To(Succeed())
	})

	It("does not stat the TLS material it no longer presents", func() {
		// These paths were validated as existing files while they were dialled
		// with. Keeping that on an ignored flag would fail a worker at startup
		// over a certificate for a broker the operator has already deleted,
		// which is the exact upgrade the acceptance exists to survive.
		missing := filepath.Join(GinkgoT().TempDir(), "a-broker-ca-that-was-deleted.pem")
		Expect(parse("--register-to", "http://frontend:8080",
			"--nats-tlsca", missing,
			"--nats-tls-cert", missing,
			"--nats-tls-key", missing)).To(Succeed())
	})

	It("keeps every accepted bus flag hidden from --help", func() {
		var cli struct {
			AgentWorker AgentWorkerCMD `cmd:""`
		}
		parser, err := kong.New(&cli)
		Expect(err).ToNot(HaveOccurred())
		var visible []string
		for _, node := range parser.Model.Children {
			for _, flag := range node.Flags {
				if strings.HasPrefix(flag.Name, "nats-") && !flag.Hidden {
					visible = append(visible, flag.Name)
				}
			}
		}
		Expect(visible).To(BeEmpty(),
			"%v are still offered in --help while doing nothing", visible)
	})
})

// Which flag makes an agent worker wait through admin approval.
//
// The docs promise this specifically, and it is the one behavioural change in
// the broker removal that an operator can be surprised by, so it is asserted
// both ways round. The positive half alone would stay green if the gate were
// widened back to OR --nats-require-auth; the negative half is what says the
// change actually happened.
var _ = Describe("The agent worker's approval gate", func() {
	It("waits when --distributed-require-auth is set", func() {
		cmd := &AgentWorkerCMD{DistributedRequireAuth: true}
		Expect(cmd.waitThroughApproval()).To(BeTrue())
	})

	It("does not wait for the broker flag that used to imply it", func() {
		// --nats-require-auth named a bus this worker does not dial. An
		// operator who set only that one now gets the historical default:
		// register, start, and be refused at every tunnel dial with 403 until
		// an admin approves. Documented in the migration section of
		// docs/content/features/distributed-mode.md.
		cmd := &AgentWorkerCMD{NatsRequireAuth: true}
		Expect(cmd.waitThroughApproval()).To(BeFalse(),
			"an ignored flag is gating a real behaviour again")
	})

	It("does not wait when neither is set", func() {
		Expect((&AgentWorkerCMD{}).waitThroughApproval()).To(BeFalse())
	})
})
