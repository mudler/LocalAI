package p2psplit_test

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2psplit"
)

// addrStrings returns the addresses of h as strings.
func addrStrings(h *p2pnet.Host) []string {
	var out []string
	for _, a := range h.Addrs() {
		out = append(out, a.String())
	}
	return out
}

// startManager starts a manager with an Elsewhere hook.
func startManager(h *p2pnet.Host, devs func() []p2psplit.Device, elsewhere func(context.Context, p2pnet.ID) (p2psplit.Move, bool), llama, mlx *recorder) *p2psplit.Manager {
	GinkgoHelper()
	m, err := p2psplit.NewManager(context.Background(), p2psplit.Config{
		Host:      h,
		Devices:   func(context.Context) ([]p2psplit.Device, error) { return devs(), nil },
		Elsewhere: elsewhere,
		OnLlama:   llama.set,
		OnMLX:     mlx.set,
	})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(m.Close)
	return m
}

var _ = Describe("a split worker attached to a backend node", func() {
	It("moves to the node that the server names and serves llama.cpp RPC there", func() {
		worker, workerGate := newHost()
		server, _ := newHost(worker.ID())
		node, _ := newHost(worker.ID())
		workerGate.Allow(server.ID())
		rpc := echoServer("127.0.0.1:0", "rpc:")
		dev := p2psplit.Device{ID: "d1", Label: "attic", Peer: worker.ID(), Engine: p2psplit.EngineLlama, CreatedAt: time.Now()}

		var serverLlama, serverMLX, nodeLlama, nodeMLX recorder
		startManager(server, func() []p2psplit.Device { return nil }, func(_ context.Context, p p2pnet.ID) (p2psplit.Move, bool) {
			if p != worker.ID() {
				return p2psplit.Move{}, false
			}
			return p2psplit.Move{Peer: node.ID(), Addrs: addrStrings(node)}, true
		}, &serverLlama, &serverMLX)
		nm := startManager(node, func() []p2psplit.Device { return []p2psplit.Device{dev} }, nil, &nodeLlama, &nodeMLX)

		runAgent(p2psplit.AgentConfig{
			Host: worker, Server: server.ID(), Dial: []p2pnet.DialOption{p2pnet.WithAddrs(server.Addrs()...)},
			Engine: p2psplit.EngineLlama, Name: "attic", MemoryBytes: 8 << 30, RPCAddr: rpc,
		})

		Eventually(nodeLlama.get, 15*time.Second).Should(HaveLen(1))
		Expect(roundTrip(nodeLlama.get()[0], "hello")).To(Equal("rpc:hello"))
		Expect(serverLlama.get()).To(BeEmpty(), "the server forwards nothing for a worker it moved")
		Expect(workerGate.Allowed(node.ID())).To(BeTrue(), "the worker admits the node it moved to")
		ws := nm.Workers()
		Expect(ws).To(HaveLen(1))
		Expect(ws[0].State).To(Equal(p2psplit.StateOnline))
		Expect(ws[0].MemoryBytes).To(Equal(uint64(8 << 30)))
	})

	It("goes back to the server when the node lets it go, and follows a new answer", func() {
		worker, workerGate := newHost()
		server, _ := newHost(worker.ID())
		node, _ := newHost(worker.ID())
		workerGate.Allow(server.ID())
		rpc := echoServer("127.0.0.1:0", "rpc:")
		dev := p2psplit.Device{ID: "d1", Peer: worker.ID(), Engine: p2psplit.EngineLlama}

		var mu sync.Mutex
		onNode := true
		attached := func() bool { mu.Lock(); defer mu.Unlock(); return onNode }
		var serverLlama, serverMLX, nodeLlama, nodeMLX recorder
		sm := startManager(server, func() []p2psplit.Device {
			if attached() {
				return nil
			}
			return []p2psplit.Device{dev}
		}, func(_ context.Context, p p2pnet.ID) (p2psplit.Move, bool) {
			if p != worker.ID() || !attached() {
				return p2psplit.Move{}, false
			}
			return p2psplit.Move{Peer: node.ID(), Addrs: addrStrings(node)}, true
		}, &serverLlama, &serverMLX)
		nm := startManager(node, func() []p2psplit.Device {
			if attached() {
				return []p2psplit.Device{dev}
			}
			return nil
		}, func(context.Context, p2pnet.ID) (p2psplit.Move, bool) {
			return p2psplit.Move{Wait: "this node does not have the worker"}, true
		}, &nodeLlama, &nodeMLX)

		runAgent(p2psplit.AgentConfig{
			Host: worker, Server: server.ID(), Dial: []p2pnet.DialOption{p2pnet.WithAddrs(server.Addrs()...)},
			Engine: p2psplit.EngineLlama, RPCAddr: rpc,
		})
		Eventually(nodeLlama.get, 15*time.Second).Should(HaveLen(1))

		// The administrator attaches the worker to the server again.
		mu.Lock()
		onNode = false
		mu.Unlock()
		nm.Refresh()
		sm.Refresh()
		Eventually(nodeLlama.get, 10*time.Second).Should(BeEmpty())
		Eventually(serverLlama.get, 30*time.Second).Should(HaveLen(1))
		Expect(roundTrip(serverLlama.get()[0], "back")).To(Equal("rpc:back"))
		Eventually(func() bool { return workerGate.Allowed(node.ID()) }, 10*time.Second).Should(BeFalse(),
			"the worker stops admitting the node it left")
	})

	It("waits, and is not refused, while the node has not reported", func() {
		worker, workerGate := newHost()
		server, _ := newHost(worker.ID())
		workerGate.Allow(server.ID())
		var asked atomic.Int32
		var llama, mlx recorder
		startManager(server, func() []p2psplit.Device { return nil }, func(context.Context, p2pnet.ID) (p2psplit.Move, bool) {
			asked.Add(1)
			return p2psplit.Move{Wait: "the node has not reported yet"}, true
		}, &llama, &mlx)
		done := runAgent(p2psplit.AgentConfig{
			Host: worker, Server: server.ID(), Dial: []p2pnet.DialOption{p2pnet.WithAddrs(server.Addrs()...)},
			Engine: p2psplit.EngineLlama, RPCAddr: "127.0.0.1:1",
		})
		Eventually(asked.Load, 15*time.Second).Should(BeNumerically(">=", 2), "the worker asks again")
		Consistently(done, 2*time.Second).ShouldNot(Receive())
	})

	It("builds the MLX ring with the node as rank 0", func() {
		worker, workerGate := newHost()
		server, _ := newHost(worker.ID())
		node, _ := newHost(worker.ID())
		workerGate.Allow(server.ID())
		dev := p2psplit.Device{ID: "m1", Peer: worker.ID(), Engine: p2psplit.EngineMLX}
		var serverLlama, serverMLX, nodeLlama, nodeMLX recorder
		startManager(server, func() []p2psplit.Device { return nil }, func(context.Context, p2pnet.ID) (p2psplit.Move, bool) {
			return p2psplit.Move{Peer: node.ID(), Addrs: addrStrings(node)}, true
		}, &serverLlama, &serverMLX)
		startManager(node, func() []p2psplit.Device { return []p2psplit.Device{dev} }, nil, &nodeLlama, &nodeMLX)

		assigned := make(chan p2psplit.Assignment, 4)
		runAgent(p2psplit.AgentConfig{
			Host: worker, Server: server.ID(), Dial: []p2pnet.DialOption{p2pnet.WithAddrs(server.Addrs()...)},
			Engine: p2psplit.EngineMLX, RingDir: GinkgoT().TempDir(),
			OnAssign: func(a p2psplit.Assignment) { assigned <- a },
		})
		Eventually(nodeMLX.get, 15*time.Second).Should(HaveLen(2))
		var a p2psplit.Assignment
		Eventually(assigned, 15*time.Second).Should(Receive(&a))
		Expect(a.Rank).To(Equal(1))
		hostfile := nodeMLX.get()
		h1 := readHostfile(a.Hostfile)
		echoServer(hostfile[0], "rank0:")
		echoServer(h1[1], "rank1:")
		Expect(roundTrip(hostfile[1], "x")).To(Equal("rank1:x"))
		Expect(roundTrip(h1[0], "y")).To(Equal("rank0:y"), "rank 1 reaches the node, not the server")
		Expect(serverMLX.get()).To(BeEmpty())
	})
})
