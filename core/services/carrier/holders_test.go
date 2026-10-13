package carrier_test

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"
	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/workerctl"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// callCase calls one method of a holder.
type callCase[H any] struct {
	method string
	call   func(H)
}

var _ = Describe("Set", func() {
	It("accepts a complete set", func() {
		Expect(newFakeCarrier(cluster.CarrierNATS, 1).set.Validate()).To(Succeed())
	})

	It("names the first member that is missing", func() {
		for name, mutate := range map[string]func(*carrier.Set){
			"Broadcaster": func(s *carrier.Set) { s.Broadcaster = nil },
			"WorkQueue":   func(s *carrier.Set) { s.WorkQueue = nil },
			"Commands":    func(s *carrier.Set) { s.Commands = nil },
			"Files":       func(s *carrier.Set) { s.Files = nil },
			"Clients":     func(s *carrier.Set) { s.Clients = nil },
			"Dialer":      func(s *carrier.Set) { s.Dialer = nil },
			"Agents":      func(s *carrier.Set) { s.Agents = nil },
		} {
			s := newFakeCarrier(cluster.CarrierNATS, 1).set
			mutate(s)
			Expect(s.Validate()).To(MatchError(ContainSubstring(name)), name)
		}
	})

	It("refuses a carrier name the cluster does not know", func() {
		s := newFakeCarrier("carrier-pigeon", 1).set
		Expect(s.Validate()).To(MatchError(cluster.ErrInvalidCarrier))
	})
})

var _ = Describe("Forwarding holders", func() {
	var (
		a, b *fakeCarrier
		cur  atomic.Pointer[carrier.Set]
	)

	BeforeEach(func() {
		a = newFakeCarrier(cluster.CarrierNATS, 1)
		b = newFakeCarrier(cluster.CarrierTunnel, 2)
		cur = atomic.Pointer[carrier.Set]{}
		cur.Store(a.set)
	})

	ctx := context.Background()

	Describe("Commands", func() {
		var progress = func(workerctl.BackendInstallProgressEvent) {}
		cases := []callCase[*carrier.Commands]{
			{"InstallBackend", func(h *carrier.Commands) {
				_, _ = h.InstallBackend("n", "t", "m", "g", "u", "name", "alias", 1, "op", progress)
			}},
			{"UpgradeBackend", func(h *carrier.Commands) {
				_, _ = h.UpgradeBackend("n", "t", "g", "u", "name", "alias", 1, "op", progress)
			}},
			{"DeleteBackend", func(h *carrier.Commands) { _, _ = h.DeleteBackend("n", "b") }},
			{"ListBackends", func(h *carrier.Commands) { _, _ = h.ListBackends("n") }},
			{"StopBackend", func(h *carrier.Commands) { _ = h.StopBackend("n", "b") }},
			{"UnloadModelOnNode", func(h *carrier.Commands) { _ = h.UnloadModelOnNode("n", "m") }},
			{"PingNode", func(h *carrier.Commands) { _ = h.PingNode("n") }},
			{"InstallBackendOp", func(h *carrier.Commands) {
				_, _ = h.InstallBackendOp("n", "t", "m", "g", 1, "op", "oid", time.Second, progress)
			}},
			{"StopLoadOperation", func(h *carrier.Commands) {
				_, _ = h.StopLoadOperation(ctx, "n", workerctl.ModelStopRequest{})
			}},
			{"OperationControl", func(h *carrier.Commands) { _, _ = h.OperationControl("n", workerctl.OperationRequest{}) }},
			{"UnloadReplica", func(h *carrier.Commands) { _ = h.UnloadReplica("n", nodes.NodeModel{}) }},
			{"StopModelReplica", func(h *carrier.Commands) {
				_, _ = h.StopModelReplica(ctx, "n", nodes.NodeModel{}, true)
			}},
			{"ListRunningModels", func(h *carrier.Commands) { _, _ = h.ListRunningModels("n") }},
			{"UnloadRemoteModel", func(h *carrier.Commands) { _ = h.UnloadRemoteModel("m") }},
			{"UnloadRemoteModelContext", func(h *carrier.Commands) { _ = h.UnloadRemoteModelContext(ctx, "m", true) }},
			{"HasRemoteModel", func(h *carrier.Commands) { _, _ = h.HasRemoteModel(ctx, "m") }},
			{"InstallTimeout", func(h *carrier.Commands) { _ = h.InstallTimeout() }},
			{"DeleteModelFiles", func(h *carrier.Commands) { _ = h.DeleteModelFiles("m") }},
			{"InstallBackendForce", func(h *carrier.Commands) {
				_, _ = h.InstallBackendForce("n", "t", "g", "u", "name", "alias", 1, "op", progress)
			}},
		}

		It("covers every method of nodes.NodeControl", func() {
			// The compile-time assertion proves the holder has each method; this
			// table proves each one is forwarded to the method of the same name.
			Expect(cases).To(HaveLen(19))
		})

		for _, tc := range cases {
			It("forwards "+tc.method+" to the carrier the pointer names", func() {
				h := carrier.NewCommands(&cur)
				tc.call(h)
				Expect(a.commands.seen()).To(Equal([]string{tc.method}))
				Expect(b.commands.seen()).To(BeEmpty())

				cur.Store(b.set)
				tc.call(h)
				Expect(a.commands.seen()).To(Equal([]string{tc.method}))
				Expect(b.commands.seen()).To(Equal([]string{tc.method}))
			})
		}

		It("passes the arguments and the reply through untouched", func() {
			var got []string
			fake := &argCommands{fakeCommands: a.commands, got: &got}
			a.set.Commands = fake
			h := carrier.NewCommands(&cur)

			reply, err := h.OperationControl("node-7", workerctl.OperationRequest{Renew: []string{"op-9"}})
			Expect(err).ToNot(HaveOccurred())
			Expect(reply.Renewed).To(Equal([]string{"op-9"}))
			Expect(got).To(Equal([]string{"node-7/op-9"}))
		})
	})

	Describe("Files", func() {
		cases := []callCase[*carrier.Files]{
			{"EnsureRemote", func(h *carrier.Files) { _, _ = h.EnsureRemote(ctx, "n", "l", "k") }},
			{"FetchRemote", func(h *carrier.Files) { _ = h.FetchRemote(ctx, "n", "r", "d") }},
			{"FetchRemoteByKey", func(h *carrier.Files) { _ = h.FetchRemoteByKey(ctx, "n", "k", "d") }},
			{"AllocRemoteTemp", func(h *carrier.Files) { _, _ = h.AllocRemoteTemp(ctx, "n") }},
			{"StageRemoteToStore", func(h *carrier.Files) { _ = h.StageRemoteToStore(ctx, "n", "r", "k") }},
			{"ReleaseRemote", func(h *carrier.Files) { _ = h.ReleaseRemote(ctx, "n", "k") }},
			{"ListRemoteDir", func(h *carrier.Files) { _, _ = h.ListRemoteDir(ctx, "n", "p") }},
			{"ReleaseRemoteRequest", func(h *carrier.Files) { _ = h.ReleaseRemoteRequest(ctx, "n", "req", []string{"k"}) }},
		}

		for _, tc := range cases {
			It("forwards "+tc.method+" to the carrier the pointer names", func() {
				h := carrier.NewFiles(&cur)
				tc.call(h)
				Expect(a.files.seen()).To(Equal([]string{tc.method}))
				cur.Store(b.set)
				tc.call(h)
				Expect(b.files.seen()).To(Equal([]string{tc.method}))
				Expect(a.files.seen()).To(HaveLen(1))
			})
		}

		It("is the batch release the file staging client looks for", func() {
			var stager nodes.FileStager = carrier.NewFiles(&cur)
			_, ok := stager.(nodes.RequestFileReleaser)
			Expect(ok).To(BeTrue(), "a holder that hid the extension would silently turn batch release into one call per key")
		})
	})

	Describe("WorkQueue", func() {
		It("enqueues on the carrier the pointer names", func() {
			h := carrier.NewWorkQueue(&cur)
			Expect(h.Enqueue(ctx, messaging.WorkTask, "x")).To(Succeed())
			cur.Store(b.set)
			Expect(h.Enqueue(ctx, messaging.WorkMCPCI, "y")).To(Succeed())
			Expect(a.queue.seen()).To(HaveLen(1))
			Expect(b.queue.seen()).To(HaveLen(1))
		})
	})

	Describe("Clients", func() {
		It("builds the client with the factory the pointer names", func() {
			h := carrier.NewClients(&cur)
			_ = h.NewClient("node-1", "10.0.0.1:50051", true)
			Expect(a.clients.gotNode).To(Equal("node-1"))
			Expect(a.clients.gotAddr).To(Equal("10.0.0.1:50051"))
			Expect(a.clients.gotParallel).To(BeTrue())

			cur.Store(b.set)
			_ = h.NewClient("node-2", "x", false)
			Expect(a.clients.seen()).To(HaveLen(1))
			Expect(b.clients.gotNode).To(Equal("node-2"))
		})
	})

	Describe("WorkerDialer", func() {
		It("dials with the carrier the pointer names at the moment of each dial", func() {
			dialFor := carrier.NewWorkerDialer(&cur)
			dial := dialFor("node-1") // a client keeps this function for good

			_, _ = dial(ctx, "tcp", "x:1")
			cur.Store(b.set)
			_, _ = dial(ctx, "tcp", "x:1")

			Expect(a.dialed.seen()).To(Equal([]string{"dial:node-1"}))
			Expect(b.dialed.seen()).To(Equal([]string{"dial:node-1"}))
		})
	})

	Describe("Agents", func() {
		It("forwards both verbs to the carrier the pointer names", func() {
			h := carrier.NewAgents(&cur)
			_, _ = h.ExecuteMCPTool(ctx, mcpRemote.MCPToolRequest{})
			_, _ = h.DiscoverMCPTools(ctx, mcpRemote.MCPDiscoveryRequest{})
			cur.Store(b.set)
			_, _ = h.ExecuteMCPTool(ctx, mcpRemote.MCPToolRequest{})
			Expect(a.agents.seen()).To(Equal([]string{"ExecuteMCPTool", "DiscoverMCPTools"}))
			Expect(b.agents.seen()).To(Equal([]string{"ExecuteMCPTool"}))
		})
	})

	Describe("a call in flight", func() {
		It("finishes on the carrier it started on, and a new call goes to the new one", func() {
			gate := make(chan struct{})
			entered := make(chan struct{}, 1)
			a.set.Commands = &gatedCommands{fakeCommands: a.commands, gate: gate, entered: entered}
			h := carrier.NewCommands(&cur)

			done := make(chan error, 1)
			go func() {
				_, err := h.InstallBackendOp("n", "t", "m", "g", 0, "op", "oid", time.Minute, nil)
				done <- err
			}()
			Eventually(entered).Should(Receive())

			cur.Store(b.set)
			Expect(h.PingNode("n")).To(Succeed())
			Expect(b.commands.seen()).To(Equal([]string{"PingNode"}))

			close(gate)
			Eventually(done, time.Second).Should(Receive(BeNil()))
			Expect(a.commands.seen()).To(Equal([]string{"InstallBackendOp"}))
		})
	})

	Describe("hot path", func() {
		It("adds no allocation to a forwarded call", func() {
			h := carrier.NewCommands(&cur)
			wq := carrier.NewWorkQueue(&cur)
			cl := carrier.NewClients(&cur)
			dial := carrier.NewWorkerDialer(&cur)("node-1")

			cur.Store(newQuietCarrier().set)
			var payload any = "payload"

			Expect(testing.AllocsPerRun(1000, func() { _ = h.PingNode("node-1") })).To(BeZero())
			Expect(testing.AllocsPerRun(1000, func() { _ = wq.Enqueue(ctx, messaging.WorkTask, payload) })).To(BeZero())
			Expect(testing.AllocsPerRun(1000, func() { _ = cl.NewClient("node-1", "x:1", false) })).To(BeZero())
			Expect(testing.AllocsPerRun(1000, func() { _, _ = dial(ctx, "tcp", "x:1") })).To(BeZero())
		})
	})
})

// argCommands answers OperationControl with a reply that records its arguments.
type argCommands struct {
	*fakeCommands
	got *[]string
}

func (c *argCommands) OperationControl(nodeID string, req workerctl.OperationRequest) (*workerctl.OperationReply, error) {
	*c.got = append(*c.got, nodeID+"/"+req.Renew[0])
	return &workerctl.OperationReply{Renewed: req.Renew}, nil
}

// gatedCommands blocks InstallBackendOp until gate closes.
type gatedCommands struct {
	*fakeCommands
	gate    chan struct{}
	entered chan struct{}
}

func (c *gatedCommands) InstallBackendOp(nodeID, backendType, modelID, galleriesJSON string, replicaIndex int, opID, operationID string, deadline time.Duration, onProgress func(workerctl.BackendInstallProgressEvent)) (*workerctl.BackendInstallReply, error) {
	c.entered <- struct{}{}
	<-c.gate
	return c.fakeCommands.InstallBackendOp(nodeID, backendType, modelID, galleriesJSON, replicaIndex, opID, operationID, deadline, onProgress)
}

// quietCommands, quietQueue and quietClients answer without recording, so a
// spec can count the allocations the holder itself adds.
type quietCommands struct{ *fakeCommands }

func (quietCommands) PingNode(string) error { return nil }

type quietQueue struct{}

func (quietQueue) Enqueue(context.Context, messaging.WorkKind, any) error { return nil }

type quietClients struct{}

func (quietClients) NewClient(string, string, bool) grpc.Backend { return nil }

func newQuietCarrier() *fakeCarrier {
	f := newFakeCarrier(cluster.CarrierNATS, 9)
	f.set.Commands = quietCommands{f.commands}
	f.set.WorkQueue = quietQueue{}
	f.set.Clients = quietClients{}
	f.set.Dialer = func(string) func(context.Context, string, string) (net.Conn, error) { return quietDial }
	return f
}

func quietDial(context.Context, string, string) (net.Conn, error) { return nil, nil }
