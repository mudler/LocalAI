package p2psplit_test

import (
	"context"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2psplit"
)

// recorder keeps the last list a callback got.
type recorder struct {
	mu   sync.Mutex
	last []string
}

func (r *recorder) set(v []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.last = v
}

func (r *recorder) get() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

// runAgent runs an agent until the spec ends and returns its result channel.
func runAgent(cfg p2psplit.AgentConfig) <-chan error {
	GinkgoHelper()
	a, err := p2psplit.NewAgent(cfg)
	Expect(err).ToNot(HaveOccurred())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		done <- a.Run(ctx)
	}()
	DeferCleanup(func() {
		cancel()
		Eventually(finished, 10*time.Second).Should(BeClosed())
	})
	return done
}

func newManager(h *p2pnet.Host, devs func() []p2psplit.Device, llama, mlx *recorder) *p2psplit.Manager {
	GinkgoHelper()
	m, err := p2psplit.NewManager(context.Background(), p2psplit.Config{
		Host:    h,
		Devices: func(context.Context) ([]p2psplit.Device, error) { return devs(), nil },
		OnLlama: llama.set,
		OnMLX:   mlx.set,
	})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(m.Close)
	return m
}

var _ = Describe("model splitting over P2P", func() {
	It("forwards llama.cpp RPC from a loopback port of the server to the worker", func() {
		worker, _ := newHost()
		server, serverGate := newHost(worker.ID())
		_ = serverGate
		workerGate := worker.Gate().(*p2pnet.Allowlist)
		workerGate.Allow(server.ID())

		rpc := echoServer("127.0.0.1:0", "rpc:")
		var llama, mlx recorder
		dev := p2psplit.Device{ID: "d1", Label: "attic", Peer: worker.ID(), Engine: p2psplit.EngineLlama, CreatedAt: time.Now()}
		m := newManager(server, func() []p2psplit.Device { return []p2psplit.Device{dev} }, &llama, &mlx)

		runAgent(p2psplit.AgentConfig{
			Host: worker, Server: server.ID(), Dial: []p2pnet.DialOption{p2pnet.WithAddrs(server.Addrs()...)},
			Engine: p2psplit.EngineLlama, Name: "attic", Version: "test", MemoryBytes: 24 << 30, RPCAddr: rpc,
		})

		Eventually(llama.get, 15*time.Second).Should(HaveLen(1))
		Expect(roundTrip(llama.get()[0], "hello")).To(Equal("rpc:hello"))

		ws := m.Workers()
		Expect(ws).To(HaveLen(1))
		Expect(ws[0].State).To(Equal(p2psplit.StateOnline))
		Expect(ws[0].MemoryBytes).To(Equal(uint64(24 << 30)))
		Expect(ws[0].Path.Kind).To(Equal(p2pnet.PathDirect))
		Expect(ws[0].Addr).To(Equal(llama.get()[0]))
		Expect(mlx.get()).To(BeEmpty())
	})

	It("keeps the address of a worker when its session comes back", func() {
		worker, _ := newHost()
		server, _ := newHost(worker.ID())
		worker.Gate().(*p2pnet.Allowlist).Allow(server.ID())
		rpc := echoServer("127.0.0.1:0", "rpc:")
		var llama, mlx recorder
		dev := p2psplit.Device{ID: "d1", Peer: worker.ID(), Engine: p2psplit.EngineLlama}
		ports := p2psplit.NewPortBook()
		cfg := p2psplit.Config{
			Host: server, Ports: ports, OnLlama: llama.set, OnMLX: mlx.set,
			Devices: func(context.Context) ([]p2psplit.Device, error) { return []p2psplit.Device{dev}, nil },
		}
		m, err := p2psplit.NewManager(context.Background(), cfg)
		Expect(err).ToNot(HaveOccurred())
		runAgent(p2psplit.AgentConfig{
			Host: worker, Server: server.ID(), Dial: []p2pnet.DialOption{p2pnet.WithAddrs(server.Addrs()...)},
			Engine: p2psplit.EngineLlama, RPCAddr: rpc,
		})
		Eventually(llama.get, 15*time.Second).Should(HaveLen(1))
		first := llama.get()[0]

		// A restart of the P2P layer builds a new manager with the same book.
		Expect(m.Close()).To(Succeed())
		Expect(llama.get()).To(BeEmpty())
		m2, err := p2psplit.NewManager(context.Background(), cfg)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(m2.Close)
		Eventually(llama.get, 45*time.Second).Should(Equal([]string{first}))
		Expect(roundTrip(first, "again")).To(Equal("rpc:again"))
	})

	It("refuses a worker that runs another engine than the invite named", func() {
		worker, _ := newHost()
		server, _ := newHost(worker.ID())
		worker.Gate().(*p2pnet.Allowlist).Allow(server.ID())
		var llama, mlx recorder
		dev := p2psplit.Device{ID: "d1", Peer: worker.ID(), Engine: p2psplit.EngineMLX}
		m := newManager(server, func() []p2psplit.Device { return []p2psplit.Device{dev} }, &llama, &mlx)

		done := runAgent(p2psplit.AgentConfig{
			Host: worker, Server: server.ID(), Dial: []p2pnet.DialOption{p2pnet.WithAddrs(server.Addrs()...)},
			Engine: p2psplit.EngineLlama, RPCAddr: "127.0.0.1:1",
		})
		var err error
		Eventually(done, 15*time.Second).Should(Receive(&err))
		Expect(err).To(MatchError(p2psplit.ErrRefused))
		Expect(err.Error()).To(ContainSubstring("MLX"))
		Eventually(func() p2psplit.State { return m.Workers()[0].State }).Should(Equal(p2psplit.StateRefused))
		Expect(llama.get()).To(BeEmpty())
	})

	It("refuses a peer that is not a split device", func() {
		worker, _ := newHost()
		server, _ := newHost(worker.ID())
		worker.Gate().(*p2pnet.Allowlist).Allow(server.ID())
		var llama, mlx recorder
		newManager(server, func() []p2psplit.Device { return nil }, &llama, &mlx)
		done := runAgent(p2psplit.AgentConfig{
			Host: worker, Server: server.ID(), Dial: []p2pnet.DialOption{p2pnet.WithAddrs(server.Addrs()...)},
			Engine: p2psplit.EngineLlama, RPCAddr: "127.0.0.1:1",
		})
		var err error
		Eventually(done, 15*time.Second).Should(Receive(&err))
		Expect(err).To(MatchError(p2psplit.ErrRefused))
	})

	It("drops a worker whose device is revoked", func() {
		worker, _ := newHost()
		server, _ := newHost(worker.ID())
		worker.Gate().(*p2pnet.Allowlist).Allow(server.ID())
		rpc := echoServer("127.0.0.1:0", "rpc:")
		var llama, mlx recorder
		var mu sync.Mutex
		devs := []p2psplit.Device{{ID: "d1", Peer: worker.ID(), Engine: p2psplit.EngineLlama}}
		m := newManager(server, func() []p2psplit.Device { mu.Lock(); defer mu.Unlock(); return devs }, &llama, &mlx)
		runAgent(p2psplit.AgentConfig{
			Host: worker, Server: server.ID(), Dial: []p2pnet.DialOption{p2pnet.WithAddrs(server.Addrs()...)},
			Engine: p2psplit.EngineLlama, RPCAddr: rpc,
		})
		Eventually(llama.get, 15*time.Second).Should(HaveLen(1))
		mu.Lock()
		devs = nil
		mu.Unlock()
		m.Refresh()
		Eventually(llama.get, 10*time.Second).Should(BeEmpty())
		Expect(m.Workers()).To(BeEmpty())
	})

	It("builds an MLX ring through the server, with no link between the workers", func() {
		w1, _ := newHost()
		w2, _ := newHost()
		server, _ := newHost(w1.ID(), w2.ID())
		for _, w := range []*p2pnet.Host{w1, w2} {
			w.Gate().(*p2pnet.Allowlist).Allow(server.ID())
		}
		var llama, mlx recorder
		t0 := time.Now()
		devs := []p2psplit.Device{
			{ID: "a", Peer: w1.ID(), Engine: p2psplit.EngineMLX, CreatedAt: t0},
			{ID: "b", Peer: w2.ID(), Engine: p2psplit.EngineMLX, CreatedAt: t0.Add(time.Second)},
		}
		m := newManager(server, func() []p2psplit.Device { return devs }, &llama, &mlx)

		// Each fake MLX process binds its own hostfile entry and answers
		// with its rank.
		assigned := map[int]chan p2psplit.Assignment{1: make(chan p2psplit.Assignment, 4), 2: make(chan p2psplit.Assignment, 4)}
		start := func(h *p2pnet.Host, slot int) {
			runAgent(p2psplit.AgentConfig{
				Host: h, Server: server.ID(), Dial: []p2pnet.DialOption{p2pnet.WithAddrs(server.Addrs()...)},
				Engine: p2psplit.EngineMLX, RingDir: GinkgoT().TempDir(),
				OnAssign: func(a p2psplit.Assignment) { assigned[slot] <- a },
			})
		}
		start(w1, 1)
		Eventually(mlx.get, 15*time.Second).Should(HaveLen(2))
		start(w2, 2)
		Eventually(mlx.get, 15*time.Second).Should(HaveLen(3))
		hostfile := mlx.get()

		var a1, a2 p2psplit.Assignment
		Eventually(func() int {
			select {
			case a1 = <-assigned[1]:
			default:
			}
			return a1.Size
		}, 15*time.Second).Should(Equal(3))
		Eventually(assigned[2], 15*time.Second).Should(Receive(&a2))
		Expect(a1.Rank).To(Equal(1))
		Expect(a2.Rank).To(Equal(2))
		Expect(a2.Size).To(Equal(3))

		h1 := readHostfile(a1.Hostfile)
		h2 := readHostfile(a2.Hostfile)
		echoServer(hostfile[0], "rank0:")
		echoServer(h1[1], "rank1:")
		echoServer(h2[2], "rank2:")

		// Rank 0 (the server) reaches both workers.
		Expect(roundTrip(hostfile[1], "x")).To(Equal("rank1:x"))
		Expect(roundTrip(hostfile[2], "y")).To(Equal("rank2:y"))
		// Rank 1 reaches rank 2 and rank 0 through the server.
		Expect(roundTrip(h1[2], "z")).To(Equal("rank2:z"))
		Expect(roundTrip(h1[0], "w")).To(Equal("rank0:w"))
		// Rank 2 reaches rank 1.
		Expect(roundTrip(h2[1], "v")).To(Equal("rank1:v"))

		ws := m.Workers()
		Expect(ws).To(HaveLen(2))
		Expect(ws[0].Rank).To(Equal(1))
		Expect(ws[1].Rank).To(Equal(2))
		Expect(llama.get()).To(BeEmpty())
	})
})
