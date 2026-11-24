package worker

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2prelay"
	"github.com/mudler/LocalAI/core/services/p2prelaymgr"
	"github.com/mudler/LocalAI/core/services/p2psplit"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// gatedRelay starts a gated relay on loopback with one admin and returns its
// address info.
func gatedRelay(admin peer.ID) peer.AddrInfo {
	GinkgoHelper()
	cfg := p2prelay.DefaultConfig(false)
	cfg.Admins = []peer.ID{admin}
	cfg.AllowPrivateAddrs = true
	cfg.DataDir = GinkgoT().TempDir()
	r, err := p2prelay.New(cfg)
	Expect(err).ToNot(HaveOccurred())
	opts, err := r.HostOptions()
	Expect(err).ToNot(HaveOccurred())
	h, err := libp2p.New(append(opts, libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))...)
	Expect(err).ToNot(HaveOccurred())
	Expect(r.Start(h)).To(Succeed())
	DeferCleanup(func() { _ = r.Close(); _ = h.Close() })
	return peer.AddrInfo{ID: h.ID(), Addrs: h.Addrs()}
}

func relayAddr(ai peer.AddrInfo) string {
	GinkgoHelper()
	as, err := peer.AddrInfoToP2pAddrs(&ai)
	Expect(err).ToNot(HaveOccurred())
	return as[0].String()
}

// peerList is a list of peer IDs that a spec changes.
type peerList struct {
	mu  sync.Mutex
	ids []string
}

func (l *peerList) get() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.ids...)
}
func (l *peerList) set(ids ...string) { l.mu.Lock(); l.ids = ids; l.mu.Unlock() }

var _ = Describe("A token node reached by its split workers through a gated relay", func() {
	It("reserves on the relays of the answer once the relay admits it, and loses the path when it is no longer admitted", func() {
		// The frontend: an admin of the relay that vouches for the paired
		// split worker and, once the node reports, for the node.
		front, err := p2pnet.New(context.Background(), p2pnet.Config{
			IdentityPath: filepath.Join(GinkgoT().TempDir(), "front.key"),
			ListenAddrs:  []string{"/ip4/127.0.0.1/tcp/0"},
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(front.Close)
		relay := gatedRelay(front.ID())

		// The split worker: no listen address, so only the relay reaches it.
		workerGate := p2pnet.NewAllowlist()
		worker, err := p2pnet.New(context.Background(), p2pnet.Config{
			IdentityPath: filepath.Join(GinkgoT().TempDir(), "worker.key"),
			ListenAddrs:  []string{},
			StaticRelays: []peer.AddrInfo{relay},
			RelayRetry:   time.Second,
			Gate:         workerGate,
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(worker.Close)

		var paired, nodePeers peerList
		paired.set(worker.ID().String())
		mgr, err := p2prelaymgr.New(p2prelaymgr.Config{
			Host: front, PairedPeers: paired.get, NodePeers: nodePeers.get,
			Tick: 200 * time.Millisecond, SyncInterval: 500 * time.Millisecond, Retry: 200 * time.Millisecond,
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(mgr.Close)
		r, err := mgr.Add(context.Background(), relayAddr(relay), "home", "")
		Expect(err).ToNot(HaveOccurred())
		Expect(r.State).To(Equal(p2prelaymgr.StateInSync))
		Expect(mgr.Start(context.Background())).To(Succeed())

		// The node, registered with the token: no listen address either.
		front2node := &splitFrontend{}
		llama := &addrList{}
		hostCh := make(chan *p2pnet.Host, 4)
		nodeKey := filepath.Join(GinkgoT().TempDir(), "node.key")
		node := newSplitNode(splitConfig{
			Sync:     front2node.sync,
			Interval: 300 * time.Millisecond,
			NewHost: func(ctx context.Context, gate *p2pnet.Allowlist, relays []p2pnet.AddrInfo) (*p2pnet.Host, error) {
				h, err := p2pnet.New(ctx, p2pnet.Config{
					IdentityPath: nodeKey,
					ListenAddrs:  []string{},
					StaticRelays: relays,
					RelayRetry:   time.Second,
					Gate:         gate,
				})
				if err == nil {
					hostCh <- h
				}
				return h, err
			},
			OnLlama: llama.set,
			OnMLX:   func([]string) {},
		})
		front2node.set(func(f *splitFrontend) {
			f.assign = workerctl.SplitAssignment{
				SplitServer: true,
				Workers: []workerctl.SplitAssigned{{
					DeviceID: "d1", Label: "attic", PeerID: worker.ID().String(), Engine: "llama",
					AllowSlowPath: true, CreatedAt: time.Now(),
				}},
				OwnRelays: []string{relay.ID.String()},
			}
		})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); node.run(ctx) }()
		DeferCleanup(func() { cancel(); Eventually(done, 10*time.Second).Should(BeClosed()) })

		var h *p2pnet.Host
		Eventually(hostCh, 10*time.Second).Should(Receive(&h))
		Eventually(func() string { return front2node.report().PeerID }, 5*time.Second).Should(Equal(h.ID().String()))
		nodeID := h.ID().String()

		By("refusing the node while the frontend does not vouch for it")
		dialNode := func() error {
			c, err := worker.Dial(dialCtx(5*time.Second), h.ID(), p2psplit.ProtoControl, p2pnet.WithRelays(relay))
			if err == nil {
				_ = c.Close()
			}
			return err
		}
		Consistently(dialNode, 3*time.Second, time.Second).ShouldNot(Succeed())

		By("admitting the node: it reserves and reports a path through the relay")
		nodePeers.set(nodeID)
		mgr.Resync()
		// The frontend names the relays that admit the node.
		Eventually(func() string {
			for _, a := range mgr.NodeAdmission(nodeID) {
				return a.State
			}
			return ""
		}, 10*time.Second).Should(Equal(p2prelaymgr.AdmissionAdmitted))
		front2node.set(func(f *splitFrontend) { f.assign.Relays = []string{relayAddr(relay)} })
		Eventually(func() string { return strings.Join(front2node.report().Addrs, " ") }, 20*time.Second).Should(ContainSubstring("/p2p/" + relay.ID.String() + "/p2p-circuit"))

		workerGate.Allow(h.ID())
		dial, err := p2pnet.AddrsOption(front2node.report().Addrs)
		Expect(err).ToNot(HaveOccurred())
		agent, err := p2psplit.NewAgent(p2psplit.AgentConfig{
			Host: worker, Server: h.ID(), Dial: []p2pnet.DialOption{dial},
			Engine: p2psplit.EngineLlama, RPCAddr: splitRPCEcho(), MemoryBytes: 4 << 30,
		})
		Expect(err).ToNot(HaveOccurred())
		actx, acancel := context.WithCancel(context.Background())
		adone := make(chan struct{})
		go func() { defer close(adone); _ = agent.Run(actx) }()
		DeferCleanup(func() { acancel(); Eventually(adone, 10*time.Second).Should(BeClosed()) })

		Eventually(llama.get, 30*time.Second).Should(HaveLen(1))
		Expect(splitAsk(llama.get()[0], "hi")).To(Equal("rpc:hi"))
		Eventually(func() []workerctl.SplitWorkerReport { return front2node.report().Workers }, 5*time.Second).Should(
			ContainElement(And(HaveField("State", "online"), HaveField("Path.Kind", "relayed"), HaveField("OwnRelay", true))))

		By("dropping the node from the relay when the frontend no longer vouches for it")
		nodePeers.set()
		mgr.Resync()
		front2node.set(func(f *splitFrontend) { f.assign.Relays = nil })
		Eventually(llama.get, 30*time.Second).Should(BeEmpty())
		Consistently(dialNode, 2*time.Second, time.Second).ShouldNot(Succeed())
	})

	It("starts its host again with the new relays, with the same identity", func() {
		key := filepath.Join(GinkgoT().TempDir(), "node.key")
		front := &splitFrontend{}
		got := make(chan []p2pnet.AddrInfo, 4)
		ids := make(chan string, 4)
		node := newSplitNode(splitConfig{
			Sync:     front.sync,
			Interval: 200 * time.Millisecond,
			NewHost: func(ctx context.Context, gate *p2pnet.Allowlist, relays []p2pnet.AddrInfo) (*p2pnet.Host, error) {
				h, err := p2pnet.New(ctx, p2pnet.Config{IdentityPath: key, ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}, Gate: gate})
				if err == nil {
					got <- relays
					ids <- h.ID().String()
				}
				return h, err
			},
			OnLlama: func([]string) {},
			OnMLX:   func([]string) {},
		})
		w := workerctl.SplitAssigned{DeviceID: "d1", PeerID: splitTestHost(p2pnet.NewAllowlist()).ID().String(), Engine: "llama"}
		front.set(func(f *splitFrontend) {
			f.assign = workerctl.SplitAssignment{SplitServer: true, Workers: []workerctl.SplitAssigned{w}}
		})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); node.run(ctx) }()
		DeferCleanup(func() { cancel(); Eventually(done, 10*time.Second).Should(BeClosed()) })

		var first []p2pnet.AddrInfo
		Eventually(got, 10*time.Second).Should(Receive(&first))
		Expect(first).To(BeEmpty())
		firstID := <-ids

		relay := gatedRelay(splitTestHost(p2pnet.NewAllowlist()).ID())
		front.set(func(f *splitFrontend) {
			f.assign = workerctl.SplitAssignment{SplitServer: true, Workers: []workerctl.SplitAssigned{w}, Relays: []string{relayAddr(relay)}}
		})
		var second []p2pnet.AddrInfo
		Eventually(got, 10*time.Second).Should(Receive(&second))
		Expect(second).To(HaveLen(1))
		Expect(second[0].ID).To(Equal(relay.ID))
		Expect(<-ids).To(Equal(firstID), "the same peer: the frontend keeps the node bound to it")

		By("keeping the host when the relays go away")
		front.set(func(f *splitFrontend) {
			f.assign = workerctl.SplitAssignment{SplitServer: true, Workers: []workerctl.SplitAssigned{w}}
		})
		Consistently(got, time.Second).ShouldNot(Receive())
	})
})

var _ = Describe("A token node that is given a relay with a bad address", func() {
	It("starts its host once and keeps it at every heartbeat", func() {
		front := &splitFrontend{}
		started := make(chan struct{}, 8)
		node := newSplitNode(splitConfig{
			Sync:     front.sync,
			Interval: 100 * time.Millisecond,
			NewHost: func(ctx context.Context, gate *p2pnet.Allowlist, relays []p2pnet.AddrInfo) (*p2pnet.Host, error) {
				h, err := p2pnet.New(ctx, p2pnet.Config{
					IdentityPath: filepath.Join(GinkgoT().TempDir(), "node.key"),
					ListenAddrs:  []string{"/ip4/127.0.0.1/tcp/0"}, Gate: gate,
				})
				if err == nil {
					started <- struct{}{}
				}
				return h, err
			},
			OnLlama: func([]string) {},
			OnMLX:   func([]string) {},
		})
		relay := gatedRelay(splitTestHost(p2pnet.NewAllowlist()).ID())
		front.set(func(f *splitFrontend) {
			f.assign = workerctl.SplitAssignment{SplitServer: true, Relays: []string{relayAddr(relay), "not an address"}}
		})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); node.run(ctx) }()
		DeferCleanup(func() { cancel(); Eventually(done, 10*time.Second).Should(BeClosed()) })

		Eventually(started, 10*time.Second).Should(Receive())
		Consistently(started, 1500*time.Millisecond).ShouldNot(Receive(), "the host restarted although the relays did not change")
	})
})

func dialCtx(d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	DeferCleanup(cancel)
	return ctx
}
