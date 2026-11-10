package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	mcpremote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// fakeConnections says which nodes are connected, and to whom.
type fakeConnections struct {
	mu    sync.Mutex
	held  map[string]bool // node id -> held by the asking replica
	err   error
	asked [][]string
}

func (f *fakeConnections) ConnectedAmong(_ context.Context, ids []string, _ string) ([]string, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, append([]string(nil), ids...))
	if f.err != nil {
		return nil, nil, f.err
	}
	var held, own []string
	for _, id := range ids {
		byOwner, ok := f.held[id]
		if !ok {
			continue
		}
		held = append(held, id)
		if byOwner {
			own = append(own, id)
		}
	}
	return held, own, nil
}

// agentWorkers is a set of fake agent workers, one HTTP server each, reached by a
// dialer that routes by node id.
type agentWorkers struct {
	mu      sync.Mutex
	servers map[string]*httptest.Server
	calls   map[string]*atomic.Int32
}

func newAgentWorkers() *agentWorkers {
	return &agentWorkers{servers: map[string]*httptest.Server{}, calls: map[string]*atomic.Int32{}}
}

func (a *agentWorkers) add(id string, h http.HandlerFunc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	counter := &atomic.Int32{}
	a.calls[id] = counter
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Add(1)
		_, _ = io.ReadAll(r.Body)
		h(w, r)
	}))
	DeferCleanup(srv.Close)
	a.servers[id] = srv
}

func (a *agentWorkers) callsTo(id string) int { return int(a.calls[id].Load()) }

func (a *agentWorkers) dialerFor() WorkerNetDialerFor {
	return func(nodeID string) func(context.Context, string, string) (net.Conn, error) {
		return func(ctx context.Context, _, _ string) (net.Conn, error) {
			a.mu.Lock()
			srv, ok := a.servers[nodeID]
			a.mu.Unlock()
			if !ok {
				return nil, ErrNoRoute
			}
			var d net.Dialer
			return d.DialContext(ctx, "tcp", srv.Listener.Addr().String())
		}
	}
}

func replyWith(v any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
}

var _ = Describe("The agent control of the tunnel carrier", func() {
	var (
		ctx     context.Context
		db      *gorm.DB
		reg     *NodeRegistry
		conns   *fakeConnections
		workers *agentWorkers
		control *AgentControlClient
		ids     map[string]string
	)

	register := func(name, nodeType string, autoApprove bool) string {
		GinkgoHelper()
		n := &BackendNode{Name: name, NodeType: nodeType}
		Expect(reg.Register(ctx, n, autoApprove)).To(Succeed())
		return n.ID
	}

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		ctx = context.Background()
		db = testutil.SetupTestDB()
		var err error
		reg, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		conns = &fakeConnections{held: map[string]bool{}}
		workers = newAgentWorkers()
		ids = map[string]string{}
		sel := NewAgentSelector(reg, conns, "replica-a")
		control = NewAgentControlClient(sel, NewControlClient(workers.dialerFor(), "registration-token"))
	})

	connect := func(name string, byOwner bool, h http.HandlerFunc) {
		GinkgoHelper()
		ids[name] = register(name, NodeTypeAgent, true)
		conns.held[ids[name]] = byOwner
		workers.add(ids[name], h)
	}

	toolReq := mcpremote.MCPToolRequest{ModelName: "m", ToolName: "t"}

	Describe("choosing an agent worker", func() {
		It("reports no agent worker, as a missing route, when none is registered", func() {
			_, _, err := NewAgentSelector(reg, conns, "replica-a").PickConnectedExcluding(ctx, nil)
			Expect(err).To(MatchError(ErrNoAgentWorker))
			Expect(errors.Is(err, ErrNoRoute)).To(BeTrue())
		})

		It("reports no agent worker when none of the registered ones holds a tunnel", func() {
			register("idle", NodeTypeAgent, true)
			_, _, err := NewAgentSelector(reg, conns, "replica-a").PickConnectedExcluding(ctx, nil)
			Expect(err).To(MatchError(ErrNoAgentWorker))
		})

		It("keeps a database error apart from a missing route", func() {
			register("a1", NodeTypeAgent, true)
			conns.err = errors.New("database is away")
			_, _, err := NewAgentSelector(reg, conns, "replica-a").PickConnectedExcluding(ctx, nil)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, ErrNoAgentWorker)).To(BeFalse())
			Expect(errors.Is(err, ErrNoRoute)).To(BeFalse())
		})

		It("prefers a worker whose tunnel this replica holds, because that call needs no relay", func() {
			remote := register("remote", NodeTypeAgent, true)
			own := register("own", NodeTypeAgent, true)
			conns.held[remote], conns.held[own] = false, true
			for range 20 {
				id, nodeType, err := NewAgentSelector(reg, conns, "replica-a").PickConnectedExcluding(ctx, nil)
				Expect(err).ToNot(HaveOccurred())
				Expect(id).To(Equal(own))
				Expect(nodeType).To(Equal(NodeTypeAgent))
			}
		})

		It("falls back to a worker that another replica holds", func() {
			remote := register("remote", NodeTypeAgent, true)
			conns.held[remote] = false
			id, _, err := NewAgentSelector(reg, conns, "replica-a").PickConnectedExcluding(ctx, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(id).To(Equal(remote))
		})

		It("offers only agent workers that may take work", func() {
			backend := register("backend", NodeTypeBackend, true)
			pending := register("pending", NodeTypeAgent, false)
			draining := register("draining", NodeTypeAgent, true)
			Expect(reg.MarkDraining(ctx, draining)).To(Succeed())
			for _, id := range []string{backend, pending, draining} {
				conns.held[id] = true
			}
			_, _, err := NewAgentSelector(reg, conns, "replica-a").PickConnectedExcluding(ctx, nil)
			Expect(err).To(MatchError(ErrNoAgentWorker))
			Expect(conns.asked).ToNot(BeEmpty())
			for _, asked := range conns.asked {
				Expect(asked).To(BeEmpty(), "a node that may take no work is not even asked about")
			}
		})

		It("does not read a failed health probe as a reason to skip a worker that holds a tunnel", func() {
			id := register("flaky", NodeTypeAgent, true)
			Expect(reg.MarkUnhealthy(ctx, id)).To(Succeed())
			conns.held[id] = true
			picked, _, err := NewAgentSelector(reg, conns, "replica-a").PickConnectedExcluding(ctx, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(picked).To(Equal(id))
		})

		It("leaves out the workers that the caller has already tried", func() {
			a := register("a", NodeTypeAgent, true)
			b := register("b", NodeTypeAgent, true)
			conns.held[a], conns.held[b] = true, true
			picked, _, err := NewAgentSelector(reg, conns, "replica-a").PickConnectedExcluding(ctx, map[string]bool{a: true})
			Expect(err).ToNot(HaveOccurred())
			Expect(picked).To(Equal(b))
		})

		It("answers as a missing route when it was built with nothing", func() {
			var sel *AgentSelector
			_, _, err := sel.PickConnectedExcluding(ctx, nil)
			Expect(err).To(MatchError(ErrNoAgentWorker))
		})
	})

	Describe("a tool call", func() {
		It("returns the decoded reply with no error, even when the worker's own answer is an error", func() {
			connect("a1", true, replyWith(mcpremote.MCPToolResponse{Error: "the tool refused its arguments"}))
			reply, err := control.ExecuteMCPTool(ctx, toolReq)
			Expect(err).ToNot(HaveOccurred())
			Expect(reply.Error).To(Equal("the tool refused its arguments"))
			Expect(workers.callsTo(ids["a1"])).To(Equal(1), "an answer is never offered to a second worker")
		})

		It("sends the verb to its path with the registration token", func() {
			var path, auth atomic.Value
			connect("a1", true, func(w http.ResponseWriter, r *http.Request) {
				path.Store(r.URL.Path)
				auth.Store(r.Header.Get("Authorization"))
				replyWith(mcpremote.MCPToolResponse{Result: "ok"})(w, r)
			})
			reply, err := control.ExecuteMCPTool(ctx, toolReq)
			Expect(err).ToNot(HaveOccurred())
			Expect(reply.Result).To(Equal("ok"))
			Expect(path.Load()).To(Equal(workerctl.PathOf(workerctl.VerbMCPToolExecute)))
			Expect(auth.Load()).To(Equal("Bearer registration-token"))
		})

		It("sends discovery to the discovery path", func() {
			connect("a1", true, func(w http.ResponseWriter, r *http.Request) {
				Expect(r.URL.Path).To(Equal(workerctl.PathOf(workerctl.VerbMCPDiscovery)))
				replyWith(mcpremote.MCPDiscoveryResponse{Servers: []mcpremote.MCPServerInfo{{Name: "s"}}})(w, r)
			})
			reply, err := control.DiscoverMCPTools(ctx, mcpremote.MCPDiscoveryRequest{ModelName: "m"})
			Expect(err).ToNot(HaveOccurred())
			Expect(reply.Servers).To(HaveLen(1))
		})

		It("wraps ErrNoRoute when there is no agent worker to offer the call to", func() {
			_, err := control.ExecuteMCPTool(ctx, toolReq)
			Expect(err).To(MatchError(ErrNoRoute))
			_, err = control.DiscoverMCPTools(ctx, mcpremote.MCPDiscoveryRequest{})
			Expect(err).To(MatchError(ErrNoRoute))
		})

		It("offers the call to another worker when the first one has no tunnel any more", func() {
			// a1 is connected on paper and has no server: the dial finds no route.
			ids["gone"] = register("gone", NodeTypeAgent, true)
			conns.held[ids["gone"]] = true
			connect("a2", false, replyWith(mcpremote.MCPToolResponse{Result: "from a2"}))
			reply, err := control.ExecuteMCPTool(ctx, toolReq)
			Expect(err).ToNot(HaveOccurred())
			Expect(reply.Result).To(Equal("from a2"))
		})

		It("offers the call to another worker when the first does not serve the verb", func() {
			connect("old", true, func(w http.ResponseWriter, r *http.Request) { workerctl.WriteUnknownPath(w, r) })
			connect("new", false, replyWith(mcpremote.MCPToolResponse{Result: "from new"}))
			// The worker this replica holds is tried first; the older one answers 404.
			reply, err := control.ExecuteMCPTool(ctx, toolReq)
			Expect(err).ToNot(HaveOccurred())
			Expect(reply.Result).To(Equal("from new"))
			Expect(workers.callsTo(ids["old"])).To(Equal(1))
		})

		It("stops after three workers, and returns what the last one said", func() {
			for _, n := range []string{"o1", "o2", "o3", "o4"} {
				connect(n, true, func(w http.ResponseWriter, r *http.Request) { workerctl.WriteUnknownPath(w, r) })
			}
			_, err := control.ExecuteMCPTool(ctx, toolReq)
			Expect(err).To(HaveOccurred())
			total := 0
			for _, n := range []string{"o1", "o2", "o3", "o4"} {
				total += workers.callsTo(ids[n])
			}
			Expect(total).To(Equal(3))
			Expect(errors.Is(err, ErrNoRoute)).To(BeFalse(), "a worker that is too old is present, not missing")
		})

		It("does not offer a call to a second worker when the first one failed after it started", func() {
			connect("broken", true, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"result":`) // cut short
			})
			connect("other", false, replyWith(mcpremote.MCPToolResponse{Result: "x"}))
			_, err := control.ExecuteMCPTool(ctx, toolReq)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, ErrNoRoute)).To(BeFalse())
			Expect(workers.callsTo(ids["other"])).To(BeZero(), "the tool may have run, and a second run could do it twice")
		})

		It("reads the deadline of the caller and not its cancellation", func() {
			started := make(chan struct{})
			release := make(chan struct{})
			connect("slow", true, func(w http.ResponseWriter, r *http.Request) {
				close(started)
				<-release
				replyWith(mcpremote.MCPToolResponse{Result: "finished"})(w, r)
			})
			callCtx, cancel := context.WithCancel(ctx)
			type outcome struct {
				reply *mcpremote.MCPToolResponse
				err   error
			}
			done := make(chan outcome, 1)
			go func() {
				reply, err := control.ExecuteMCPTool(callCtx, toolReq)
				done <- outcome{reply, err}
			}()
			Eventually(started).Should(BeClosed())
			cancel() // a chat client that disconnects
			Consistently(done, "200ms").ShouldNot(Receive(), "the call on the worker goes on")
			close(release)
			var out outcome
			Eventually(done).Should(Receive(&out))
			Expect(out.err).ToNot(HaveOccurred())
			Expect(out.reply.Result).To(Equal("finished"))
		})

		It("ends the call at the deadline of the caller, and blames neither the worker nor the route", func() {
			connect("slow", true, func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
			callCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
			defer cancel()
			_, err := control.ExecuteMCPTool(callCtx, toolReq)
			Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
			Expect(errors.Is(err, ErrNoRoute)).To(BeFalse())
		})

		It("refuses at once when the budget of the caller is already spent", func() {
			connect("a1", true, replyWith(mcpremote.MCPToolResponse{}))
			callCtx, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
			defer cancel()
			_, err := control.ExecuteMCPTool(callCtx, toolReq)
			Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
			Expect(workers.callsTo(ids["a1"])).To(BeZero())
		})

		It("refuses a client built with nothing", func() {
			var nilClient *AgentControlClient
			_, err := nilClient.ExecuteMCPTool(ctx, toolReq)
			Expect(err).To(MatchError(ErrNoAgentControl))
			_, err = NewAgentControlClient(nil, nil).DiscoverMCPTools(ctx, mcpremote.MCPDiscoveryRequest{})
			Expect(err).To(MatchError(ErrNoAgentControl))
		})
	})
})
