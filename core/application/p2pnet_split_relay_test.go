//go:build auth

package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/mudler/LocalAI/core/http/auth"
	p2papi "github.com/mudler/LocalAI/core/http/endpoints/p2p"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2ppair/pairclient"
	"github.com/mudler/LocalAI/core/services/p2prelay"
	"github.com/mudler/LocalAI/core/services/p2prelay/relayproto"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// startGatedRelay starts a gated relay on loopback with the admins and
// returns its address.
func startGatedRelay(maxPeers int, admins ...peer.ID) string {
	GinkgoHelper()
	cfg := p2prelay.DefaultConfig(false)
	cfg.Admins = admins
	cfg.AllowPrivateAddrs = true
	cfg.DataDir = GinkgoT().TempDir()
	if maxPeers > 0 {
		cfg.MaxPeersPerAdmin = maxPeers
	}
	r, err := p2prelay.New(cfg)
	Expect(err).ToNot(HaveOccurred())
	opts, err := r.HostOptions()
	Expect(err).ToNot(HaveOccurred())
	h, err := libp2p.New(append(opts, libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))...)
	Expect(err).ToNot(HaveOccurred())
	Expect(r.Start(h)).To(Succeed())
	DeferCleanup(func() { _ = r.Close(); _ = h.Close() })
	ais, err := peer.AddrInfoToP2pAddrs(&peer.AddrInfo{ID: h.ID(), Addrs: h.Addrs()})
	Expect(err).ToNot(HaveOccurred())
	return ais[0].String()
}

// relayMembers lists the permanent members of the frontend's list on the
// relay, read as that frontend.
func relayMembers(a *Application, relayAddr string) []string {
	rt, _ := a.p2p().runtime()
	if rt == nil {
		return nil
	}
	ai, err := p2pnet.ParsePeerAddr(relayAddr)
	Expect(err).ToNot(HaveOccurred())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = rt.host.Libp2p().Connect(ctx, *ai)
	resp, err := relayproto.Call(ctx, rt.host.Libp2p(), ai.ID, relayproto.Request{Op: relayproto.OpList})
	if err != nil {
		return nil
	}
	var out []string
	for _, p := range resp.Peers {
		if !p.Pending {
			out = append(out, p.ID)
		}
	}
	return out
}

// relaySeq is the sequence number of the frontend's list on the relay.
func relaySeq(a *Application, relayAddr string) int64 {
	rt, _ := a.p2p().runtime()
	ai, err := p2pnet.ParsePeerAddr(relayAddr)
	Expect(err).ToNot(HaveOccurred())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := relayproto.Call(ctx, rt.host.Libp2p(), ai.ID, relayproto.Request{Op: relayproto.OpStatus})
	Expect(err).ToNot(HaveOccurred())
	return resp.Seq
}

// splitCluster is two frontends of one cluster: one pairing store, one node
// registry, each with its own P2P identity.
type splitCluster struct {
	a, b *Application
	reg  *nodes.NodeRegistry
}

func newSplitCluster(token string) *splitCluster {
	GinkgoHelper()
	db, err := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
	Expect(err).ToNot(HaveOccurred())
	ndb, err := sqlitedb.Open("file:"+filepath.Join(GinkgoT().TempDir(), "nodes.db")+"?_busy_timeout=5000", &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	Expect(err).ToNot(HaveOccurred())
	reg, err := nodes.NewNodeRegistry(ndb)
	Expect(err).ToNot(HaveOccurred())
	c := &splitCluster{reg: reg}
	for _, slot := range []**Application{&c.a, &c.b} {
		a := newP2PTestApp("share", GinkgoT().TempDir(), db)
		a.p2p().nodeRegistry = reg
		a.ApplicationConfig().Distributed.RegistrationToken = token
		redeem := echo.New()
		redeem.POST(redeemPath, p2papi.RedeemEndpoint(a.P2P()))
		a.ServeP2PHTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "main") }), redeem)
		Expect(a.StartP2PNet()).To(Succeed())
		*slot = a
	}
	return c
}

func (c *splitCluster) hostIDs() []peer.ID {
	ra, _ := c.a.p2p().runtime()
	rb, _ := c.b.p2p().runtime()
	return []peer.ID{ra.host.ID(), rb.host.ID()}
}

// pairSplitWorker pairs a split worker through frontend a with an invite
// that names node, and returns its device ID.
func (c *splitCluster) pairSplitWorker(node string) string {
	GinkgoHelper()
	ctx := context.Background()
	m, err := c.a.P2P().MintInvite(ctx, "admin", p2papi.MintRequest{Kind: "split", Label: "attic", Labels: map[string]string{"node": node}, Relays: p2papi.RelayChoice{Mode: "none"}})
	Expect(err).ToNot(HaveOccurred())
	link, err := p2ppair.ParseLink(m.Link)
	Expect(err).ToNot(HaveOccurred())
	rt, _ := c.a.p2p().runtime()
	link.Addrs = rt.host.Addrs()
	worker, err := p2pnet.New(ctx, p2pnet.Config{
		IdentityPath: filepath.Join(GinkgoT().TempDir(), "id.key"),
		ListenAddrs:  []string{"/ip4/127.0.0.1/tcp/0"},
		Gate:         p2pnet.NewAllowlist(rt.host.ID()),
	})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(worker.Close)
	_, err = pairclient.Redeem(ctx, worker, link, pairclient.Request{Name: "attic", Platform: "linux"})
	Expect(err).ToNot(HaveOccurred())
	list, err := c.a.P2PSplit().ListSplitWorkers(ctx)
	Expect(err).ToNot(HaveOccurred())
	Expect(list.Workers).To(HaveLen(1))
	return list.Workers[0].ID
}

// report is what the node route does with a report of the token node: bind
// the peer, store the report, and answer with the assignment of frontend a.
func (c *splitCluster) report(nodeID, nodePeer string) workerctl.SplitAssignment {
	GinkgoHelper()
	ctx := context.Background()
	Expect(c.reg.PinSplitPeer(ctx, nodeID, nodePeer)).To(Succeed())
	Expect(c.reg.SetSplitReport(ctx, nodeID, workerctl.SplitReport{PeerID: nodePeer})).To(Succeed())
	as, err := c.a.SplitAssignment(ctx, nodeID)
	Expect(err).ToNot(HaveOccurred())
	return as
}

func nodeRelayOf(a *Application, deviceID string) *p2papi.SplitNodeRelay {
	list, err := a.P2PSplit().ListSplitWorkers(context.Background())
	Expect(err).ToNot(HaveOccurred())
	for _, w := range list.Workers {
		if w.ID == deviceID {
			return w.NodeRelay
		}
	}
	return nil
}

func nodeRelayState(a *Application, deviceID string) func() string {
	return func() string {
		if r := nodeRelayOf(a, deviceID); r != nil {
			return r.State
		}
		return ""
	}
}

func newPeerID() string {
	h, err := p2pnet.New(context.Background(), p2pnet.Config{IdentityPath: filepath.Join(GinkgoT().TempDir(), "p.key"), ListenAddrs: []string{}})
	Expect(err).ToNot(HaveOccurred())
	id := h.ID().String()
	_ = h.Close()
	return id
}

var _ = Describe("relay admission of the backend nodes that serve split workers", func() {
	Describe("which nodes the frontends vouch for", func() {
		now := time.Now()
		dev := func(node string) p2ppair.Device {
			return p2ppair.Device{ID: "d-" + node, Kind: p2ppair.KindSplit, PeerID: "w", NodeID: node}
		}
		node := func(id string, mod func(*nodes.BackendNode)) nodes.BackendNode {
			n := nodes.BackendNode{ID: id, Name: id, NodeType: nodes.NodeTypeBackend, Status: nodes.StatusHealthy, TokenHash: tokenHash("tok")}
			if mod != nil {
				mod(&n)
			}
			return n
		}
		rep := func(id, peer string, age time.Duration) nodes.StoredSplitReport {
			return nodes.StoredSplitReport{NodeID: id, PinnedPeer: peer, Report: workerctl.SplitReport{PeerID: peer}, UpdatedAt: now.Add(-age)}
		}

		It("vouches only for a healthy token node with an attached worker and a bound, fresh report", func() {
			ns := []nodes.BackendNode{
				node("ok", nil),
				node("idle", nil),
				node("p2p", func(n *nodes.BackendNode) { n.PeerID = "joined" }),
				node("pending", func(n *nodes.BackendNode) { n.Status = nodes.StatusPending }),
				node("offline", func(n *nodes.BackendNode) { n.Status = nodes.StatusOffline }),
				node("revoked", func(n *nodes.BackendNode) { n.TokenHash = tokenHash("old") }),
				node("stale", nil),
				node("unbound", nil),
			}
			reps := map[string]nodes.StoredSplitReport{
				"ok": rep("ok", "peer-ok", 0), "idle": rep("idle", "peer-idle", 0), "p2p": rep("p2p", "joined", 0),
				"pending": rep("pending", "peer-p", 0), "offline": rep("offline", "peer-o", 0), "revoked": rep("revoked", "peer-r", 0),
				"stale": rep("stale", "peer-s", 5*time.Minute), "unbound": {NodeID: "unbound", Report: workerctl.SplitReport{PeerID: "x"}, UpdatedAt: now},
			}
			devs := []p2ppair.Device{dev("ok"), dev("p2p"), dev("pending"), dev("offline"), dev("revoked"), dev("stale"), dev("unbound"), dev("gone")}
			revoked := dev("idle")
			at := now
			revoked.RevokedAt = &at
			devs = append(devs, revoked)

			got := splitRelayCandidates(devs, ns, reps, tokenHash("tok"), now)
			Expect(got).ToNot(HaveKey("idle"), "a node with no active split worker needs no relay")
			Expect(got).ToNot(HaveKey("gone"), "a deleted node is not vouched for")
			Expect(got["ok"]).To(Equal(splitRelayCandidate{Peer: "peer-ok"}))
			Expect(got["p2p"].State).To(Equal(p2papi.NodeRelayNotNeeded))
			Expect(got["pending"].State).To(Equal(p2papi.NodeRelayRefused))
			Expect(got["revoked"].State).To(Equal(p2papi.NodeRelayRefused))
			Expect(got["revoked"].Reason).To(ContainSubstring("token"))
			Expect(got["offline"].State).To(Equal(p2papi.NodeRelayPending))
			Expect(got["stale"].State).To(Equal(p2papi.NodeRelayPending))
			Expect(got["unbound"].State).To(Equal(p2papi.NodeRelayPending))
			for id, c := range got {
				if id != "ok" {
					Expect(c.Peer).To(BeEmpty(), id)
				}
			}
		})

		It("accepts any token hash when the cluster has no registration token", func() {
			ns := []nodes.BackendNode{node("ok", func(n *nodes.BackendNode) { n.TokenHash = "" })}
			got := splitRelayCandidates([]p2ppair.Device{dev("ok")}, ns, map[string]nodes.StoredSplitReport{"ok": rep("ok", "peer-ok", 0)}, "", now)
			Expect(got["ok"].Peer).To(Equal("peer-ok"))
		})
	})

	It("admits a token node on its first report on the relays of every frontend, once, and removes it when it is no longer vouched for", func() {
		ctx := context.Background()
		c := newSplitCluster("tok")
		gpu := &nodes.BackendNode{Name: "gpu-1", NodeType: nodes.NodeTypeBackend, TokenHash: tokenHash("tok")}
		Expect(c.reg.Register(ctx, gpu, true)).To(Succeed())
		relay := startGatedRelay(0, c.hostIDs()...)
		for _, a := range []*Application{c.a, c.b} {
			r, err := a.P2P().AddRelay(ctx, relay, "home", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(r.State).To(Equal("in_sync"))
		}
		dev := c.pairSplitWorker(gpu.ID)
		Eventually(func() []string { return relayMembers(c.a, relay) }, 10*time.Second).Should(HaveLen(1), "the paired split worker")

		By("waiting for the node to report its host")
		Expect(nodeRelayState(c.a, dev)()).To(Equal(p2papi.NodeRelayPending))

		By("admitting the node on its first report")
		nodePeer := newPeerID()
		as := c.report(gpu.ID, nodePeer)
		Expect(as.Relays).To(ConsistOf(relay), "the node reserves on the relays of the frontend")
		Eventually(func() []string { return relayMembers(c.a, relay) }, 10*time.Second).Should(ContainElement(nodePeer))
		Eventually(func() []string { return relayMembers(c.b, relay) }, 10*time.Second).Should(ContainElement(nodePeer), "the other frontend vouches for it too")
		Eventually(nodeRelayState(c.a, dev), 10*time.Second).Should(Equal(p2papi.NodeRelayAdmitted))
		Eventually(nodeRelayState(c.b, dev), 10*time.Second).Should(Equal(p2papi.NodeRelayAdmitted))
		r := nodeRelayOf(c.a, dev)
		Expect(r.Relays).To(ConsistOf(HaveField("State", "admitted")))

		By("writing nothing more on repeated reports")
		seqA, seqB := relaySeq(c.a, relay), relaySeq(c.b, relay)
		for range 3 {
			c.report(gpu.ID, nodePeer)
		}
		Consistently(func() []int64 { return []int64{relaySeq(c.a, relay), relaySeq(c.b, relay)} }, 2*time.Second, 400*time.Millisecond).
			Should(Equal([]int64{seqA, seqB}))

		By("refusing a spoofed peer for the node")
		Expect(c.reg.PinSplitPeer(ctx, gpu.ID, newPeerID())).To(MatchError(nodes.ErrSplitPeerPinned))

		By("removing the node when its worker is attached to the frontend again")
		none := ""
		_, err := c.a.P2PSplit().SetSplitWorker(ctx, dev, p2papi.SplitWorkerUpdate{NodeID: &none})
		Expect(err).ToNot(HaveOccurred())
		Eventually(func() []string { return relayMembers(c.a, relay) }, 10*time.Second).ShouldNot(ContainElement(nodePeer))
		Eventually(func() []string { return relayMembers(c.b, relay) }, 10*time.Second).ShouldNot(ContainElement(nodePeer))
		as, err = c.a.SplitAssignment(ctx, gpu.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(as.Relays).To(BeEmpty())

		By("admitting it again when the worker comes back")
		_, err = c.a.P2PSplit().SetSplitWorker(ctx, dev, p2papi.SplitWorkerUpdate{NodeID: &gpu.ID})
		Expect(err).ToNot(HaveOccurred())
		c.report(gpu.ID, nodePeer)
		Eventually(func() []string { return relayMembers(c.a, relay) }, 10*time.Second).Should(ContainElement(nodePeer))

		By("removing it when the registration token of the node is no longer valid")
		for _, a := range []*Application{c.a, c.b} {
			a.ApplicationConfig().Distributed.RegistrationToken = "rotated"
		}
		Eventually(func() []string { return relayMembers(c.a, relay) }, 10*time.Second).ShouldNot(ContainElement(nodePeer))
		Eventually(func() []string { return relayMembers(c.b, relay) }, 10*time.Second).ShouldNot(ContainElement(nodePeer))
		Expect(nodeRelayState(c.a, dev)()).To(Equal(p2papi.NodeRelayRefused))
		Expect(nodeRelayOf(c.a, dev).Reason).To(ContainSubstring("token"))
		for _, a := range []*Application{c.a, c.b} {
			a.ApplicationConfig().Distributed.RegistrationToken = "tok"
		}
		Eventually(func() []string { return relayMembers(c.a, relay) }, 10*time.Second).Should(ContainElement(nodePeer))

		By("removing it when the node is deleted")
		Expect(c.reg.Deregister(ctx, gpu.ID)).To(Succeed())
		Eventually(func() []string { return relayMembers(c.a, relay) }, 10*time.Second).ShouldNot(ContainElement(nodePeer))
		Eventually(func() []string { return relayMembers(c.b, relay) }, 10*time.Second).ShouldNot(ContainElement(nodePeer))

		By("removing it when the split worker is revoked")
		gpu2 := &nodes.BackendNode{Name: "gpu-2", NodeType: nodes.NodeTypeBackend, TokenHash: tokenHash("tok")}
		Expect(c.reg.Register(ctx, gpu2, true)).To(Succeed())
		_, err = c.a.P2PSplit().SetSplitWorker(ctx, dev, p2papi.SplitWorkerUpdate{NodeID: &gpu2.ID})
		Expect(err).ToNot(HaveOccurred())
		peer2 := newPeerID()
		c.report(gpu2.ID, peer2)
		Eventually(func() []string { return relayMembers(c.a, relay) }, 10*time.Second).Should(ContainElement(peer2))
		Expect(c.a.P2P().RevokeDevice(ctx, dev, "admin")).To(Succeed())
		Eventually(func() []string { return relayMembers(c.a, relay) }, 10*time.Second).ShouldNot(ContainElement(peer2))
		Eventually(func() []string { return relayMembers(c.b, relay) }, 10*time.Second).ShouldNot(ContainElement(peer2))
	})

	It("shows a node refused by a full relay, and a node that needs no relay", func() {
		ctx := context.Background()
		c := newSplitCluster("tok")
		gpu := &nodes.BackendNode{Name: "gpu-1", NodeType: nodes.NodeTypeBackend, TokenHash: tokenHash("tok")}
		Expect(c.reg.Register(ctx, gpu, true)).To(Succeed())
		dev := c.pairSplitWorker(gpu.ID)

		By("saying that no relay is needed while this server administers none")
		c.report(gpu.ID, newPeerID())
		Eventually(nodeRelayState(c.a, dev), 10*time.Second).Should(Equal(p2papi.NodeRelayNotNeeded))

		By("refusing the node on a relay whose list is full of paired devices")
		relay := startGatedRelay(1, c.hostIDs()...)
		_, err := c.a.P2P().AddRelay(ctx, relay, "small", "")
		Expect(err).ToNot(HaveOccurred())
		c.report(gpu.ID, mustPinned(c, gpu.ID))
		Eventually(nodeRelayState(c.a, dev), 10*time.Second).Should(Equal(p2papi.NodeRelayRefused))
		r := nodeRelayOf(c.a, dev)
		Expect(r.Reason).To(ContainSubstring("1"))
		Expect(r.Relays).To(ConsistOf(And(HaveField("Label", "small"), HaveField("State", "refused"))))
		Expect(relayMembers(c.a, relay)).To(HaveLen(1), "the paired split worker keeps its place")
	})
})

func mustPinned(c *splitCluster, nodeID string) string {
	GinkgoHelper()
	rep, ok, err := c.reg.SplitReport(context.Background(), nodeID)
	Expect(err).ToNot(HaveOccurred())
	Expect(ok).To(BeTrue())
	return rep.PinnedPeer
}
