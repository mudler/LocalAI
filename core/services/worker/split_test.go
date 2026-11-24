package worker

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2psplit"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// splitFrontend answers the split exchange of a node from a list the spec
// sets, and keeps the last report.
type splitFrontend struct {
	mu     sync.Mutex
	assign workerctl.SplitAssignment
	fail   bool
	last   workerctl.SplitReport
}

func (f *splitFrontend) sync(_ context.Context, rep workerctl.SplitReport) (*workerctl.SplitAssignment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.last = rep
	if f.fail {
		return nil, errors.New("frontend down")
	}
	a := f.assign
	return &a, nil
}

func (f *splitFrontend) set(fn func(*splitFrontend)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *splitFrontend) report() workerctl.SplitReport {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}

// addrList keeps the last list a callback got.
type addrList struct {
	mu   sync.Mutex
	last []string
}

func (l *addrList) set(v []string) { l.mu.Lock(); l.last = v; l.mu.Unlock() }
func (l *addrList) get() []string  { l.mu.Lock(); defer l.mu.Unlock(); return l.last }

func splitTestHost(gate *p2pnet.Allowlist) *p2pnet.Host {
	GinkgoHelper()
	h, err := p2pnet.New(context.Background(), p2pnet.Config{
		IdentityPath: filepath.Join(GinkgoT().TempDir(), "identity.key"),
		ListenAddrs:  []string{"/ip4/127.0.0.1/tcp/0"},
		Gate:         gate,
	})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(h.Close)
	return h
}

// rpcEcho answers every connection with "rpc:" and what it read.
func splitRPCEcho() string {
	GinkgoHelper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(l.Close)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				b := make([]byte, 64)
				n, _ := c.Read(b)
				_, _ = c.Write(append([]byte("rpc:"), b[:n]...))
			}()
		}
	}()
	return l.Addr().String()
}

func splitAsk(addr, msg string) (string, error) {
	c, err := net.Dial("tcp", addr)
	if err != nil {
		return "", err
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Write([]byte(msg)); err != nil {
		return "", err
	}
	_ = c.(*net.TCPConn).CloseWrite()
	b, err := io.ReadAll(c)
	return string(b), err
}

var _ = Describe("Model-split workers attached to a backend node", func() {
	var (
		front  *splitFrontend
		llama  *addrList
		node   *splitNode
		hostCh chan *p2pnet.Host
	)

	BeforeEach(func() {
		front = &splitFrontend{}
		llama = &addrList{}
		hostCh = make(chan *p2pnet.Host, 1)
		node = newSplitNode(splitConfig{
			Sync:     front.sync,
			Interval: 200 * time.Millisecond,
			NewHost: func(_ context.Context, gate *p2pnet.Allowlist, _ []p2pnet.AddrInfo) (*p2pnet.Host, error) {
				h := splitTestHost(gate)
				hostCh <- h
				return h, nil
			},
			OnLlama: llama.set,
			OnMLX:   func([]string) {},
		})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); node.run(ctx) }()
		DeferCleanup(func() { cancel(); Eventually(done, 10*time.Second).Should(BeClosed()) })
	})

	It("runs no host while it is not a split server, even when the frontend sends workers", func() {
		front.set(func(f *splitFrontend) {
			f.assign = workerctl.SplitAssignment{Workers: []workerctl.SplitAssigned{
				{DeviceID: "d1", PeerID: splitTestHost(p2pnet.NewAllowlist()).ID().String(), Engine: "llama"},
			}}
		})
		Consistently(hostCh, time.Second).ShouldNot(Receive())
		Expect(front.report().PeerID).To(BeEmpty())
		Expect(llama.get()).To(BeEmpty())
	})

	It("starts its host as soon as it is declared, before any worker is attached, and stops it when the declaration is cleared", func() {
		front.set(func(f *splitFrontend) { f.assign = workerctl.SplitAssignment{SplitServer: true} })
		var h *p2pnet.Host
		Eventually(hostCh, 10*time.Second).Should(Receive(&h))
		Eventually(func() string { return front.report().PeerID }, 5*time.Second).Should(Equal(h.ID().String()))

		front.set(func(f *splitFrontend) { f.assign = workerctl.SplitAssignment{} })
		Eventually(func() string { return front.report().PeerID }, 5*time.Second).Should(BeEmpty(), "no host, nothing to report")
		Eventually(func() int { return len(h.Libp2p().Network().ListenAddresses()) }, 5*time.Second).Should(BeZero(), "the host is closed")
	})

	It("starts its host for the first worker, serves it, reports it, and lets it go", func() {
		workerGate := p2pnet.NewAllowlist()
		worker := splitTestHost(workerGate)
		front.set(func(f *splitFrontend) {
			f.assign = workerctl.SplitAssignment{SplitServer: true, Workers: []workerctl.SplitAssigned{
				{DeviceID: "d1", Label: "attic", PeerID: worker.ID().String(), Engine: "llama", CreatedAt: time.Now()},
			}}
		})
		var h *p2pnet.Host
		Eventually(hostCh, 10*time.Second).Should(Receive(&h))
		Eventually(func() string { return front.report().PeerID }, 5*time.Second).Should(Equal(h.ID().String()))
		Expect(front.report().Addrs).ToNot(BeEmpty())

		// The worker reaches the node through the server's move; here it
		// dials the node directly with the addresses of the report.
		workerGate.Allow(h.ID())
		agent, err := p2psplit.NewAgent(p2psplit.AgentConfig{
			Host: worker, Server: h.ID(), Dial: []p2pnet.DialOption{p2pnet.WithAddrs(h.Addrs()...)},
			Engine: p2psplit.EngineLlama, RPCAddr: splitRPCEcho(), MemoryBytes: 4 << 30,
		})
		Expect(err).ToNot(HaveOccurred())
		actx, acancel := context.WithCancel(context.Background())
		adone := make(chan struct{})
		go func() { defer close(adone); _ = agent.Run(actx) }()
		DeferCleanup(func() { acancel(); Eventually(adone, 10*time.Second).Should(BeClosed()) })

		Eventually(llama.get, 15*time.Second).Should(HaveLen(1))
		Expect(splitAsk(llama.get()[0], "hi")).To(Equal("rpc:hi"))
		Eventually(func() []workerctl.SplitWorkerReport { return front.report().Workers }, 5*time.Second).Should(
			ContainElement(And(
				HaveField("DeviceID", "d1"), HaveField("State", "online"),
				HaveField("MemoryBytes", uint64(4<<30)), HaveField("Path.Kind", "direct"),
			)))

		// The frontend cannot be reached: the node keeps its worker.
		front.set(func(f *splitFrontend) { f.fail = true })
		Consistently(llama.get, time.Second).Should(HaveLen(1))

		// The administrator detaches the worker; the node stays a split server.
		front.set(func(f *splitFrontend) { f.fail = false; f.assign = workerctl.SplitAssignment{SplitServer: true} })
		Eventually(llama.get, 10*time.Second).Should(BeEmpty())
		Expect(h.Gate().(*p2pnet.Allowlist).Allowed(worker.ID())).To(BeFalse(), "the node stops admitting a worker that left")
		Eventually(func() []workerctl.SplitWorkerReport { return front.report().Workers }, 5*time.Second).Should(BeEmpty())
	})

	It("reads the slow-path choice and the relays of the frontend from the answer", func() {
		front.set(func(f *splitFrontend) {
			f.assign = workerctl.SplitAssignment{
				SplitServer: true,
				Workers:     []workerctl.SplitAssigned{{DeviceID: "d1", PeerID: splitTestHost(p2pnet.NewAllowlist()).ID().String(), Engine: "mlx", AllowSlowPath: true}},
				OwnRelays:   []string{"12D3KooWRelay"},
			}
		})
		Eventually(hostCh, 10*time.Second).Should(Receive())
		Eventually(func() int { d, _ := node.devices(context.Background()); return len(d) }, 5*time.Second).Should(Equal(1))
		d, _ := node.devices(context.Background())
		Expect(d[0].Engine).To(Equal(p2psplit.EngineMLX))
		Expect(d[0].AllowSlowPath).To(BeTrue())
	})
})
