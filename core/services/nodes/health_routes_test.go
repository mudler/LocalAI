package nodes

import (
	"context"
	"runtime"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// routeClock is a clock the tests move by hand.
type routeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *routeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *routeClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// The kinds of node the specs build: the routes the operator gave the worker.
const (
	kindNATS   = "nats only"
	kindWS     = "websocket only"
	kindP2P    = "p2p only"
	kindNATSWS = "nats and websocket"
)

var _ = Describe("The health monitor judges a node by the routes it holds", func() {
	const (
		staleThreshold = 5 * time.Minute
		grace          = time.Minute
	)
	var (
		store    *fakeNodeHealthStore
		factory  *fakeBackendClientFactory
		presence *fakePresence
		clock    *routeClock
		hm       *HealthMonitor
	)

	BeforeEach(func() {
		store = newFakeNodeHealthStore()
		factory = newFakeBackendClientFactory()
		presence = &fakePresence{by: map[string]cluster.Presence{}}
		clock = &routeClock{t: time.Now()}
		hm = newTestHealthMonitor(store, factory, true, staleThreshold)
		hm.now = clock.now
		hm.UsePresence(presence, grace)
	})

	// node registers a worker of the given kind with a fresh heartbeat. natsUp
	// is whether its report says it holds NATS; tunnel is the presence of its
	// tunnel, and whether its report says it holds the tunnel follows from it.
	node := func(id, kind string, natsUp bool, tunnel cluster.Presence) *BackendNode {
		n := makeTestNode(id, id, "10.0.0.1:50051", StatusHealthy, clock.now())
		var attached []string
		switch kind {
		case kindNATS:
			n.Follow = "nats"
		case kindWS:
			n.Follow, n.Address, n.TunnelTransport = "tunnel", "", TunnelWebSocket
		case kindP2P:
			n.Follow, n.Address, n.PeerID = "tunnel", "", "12D3KooWpeer"
		case kindNATSWS:
			n.Follow, n.TunnelTransport = "nats,tunnel", TunnelWebSocket
		}
		if natsUp && kind != kindWS && kind != kindP2P {
			attached = append(attached, "nats")
		} else if kind == kindNATS || kind == kindNATSWS {
			n.FollowError = "nats: connecting"
		}
		if kind != kindNATS {
			presence.by[id] = tunnel
			if tunnel == cluster.PresenceConnected || tunnel == cluster.PresenceReconnecting {
				attached = append(attached, "tunnel")
			}
		}
		n.Attached = joinStrings(attached)
		store.addNode(n)
		return n
	}

	// tickPastGrace runs a check at each interval until the grace has passed,
	// keeping the heartbeat fresh, so a route that is down has been down for
	// longer than the grace.
	tickPastGrace := func(id string) {
		hm.doCheckAll(context.Background())
		for elapsed := time.Duration(0); elapsed <= grace; elapsed += hm.checkInterval {
			clock.add(hm.checkInterval)
			store.mu.Lock()
			store.nodes[id].LastHeartbeat = clock.now()
			store.mu.Unlock()
			hm.doCheckAll(context.Background())
		}
	}

	DescribeTable("keeps a node healthy while one route that carries work is live",
		func(kind string, natsUp bool, tunnel cluster.Presence, want string) {
			node("n1", kind, natsUp, tunnel)
			tickPastGrace("n1")
			Expect(store.getNode("n1").Status).To(Equal(want))
			Expect(store.getCalls()).ToNot(ContainElement(ContainSubstring("MarkOffline")), "a node with a fresh heartbeat is never reaped")
		},
		Entry("nats only, nats live", kindNATS, true, cluster.PresenceUnknown, StatusHealthy),
		Entry("nats only, nats lost", kindNATS, false, cluster.PresenceUnknown, StatusUnhealthy),
		Entry("websocket only, tunnel held", kindWS, false, cluster.PresenceConnected, StatusHealthy),
		Entry("websocket only, tunnel reconnecting", kindWS, false, cluster.PresenceReconnecting, StatusHealthy),
		Entry("websocket only, tunnel gone", kindWS, false, cluster.PresenceGone, StatusUnhealthy),
		Entry("p2p only, tunnel held", kindP2P, false, cluster.PresenceConnected, StatusHealthy),
		Entry("p2p only, tunnel gone", kindP2P, false, cluster.PresenceGone, StatusUnhealthy),
		Entry("nats and websocket, both live", kindNATSWS, true, cluster.PresenceConnected, StatusHealthy),
		Entry("nats and websocket, nats lost while the tunnel lives", kindNATSWS, false, cluster.PresenceConnected, StatusHealthy),
		Entry("nats and websocket, tunnel gone while nats lives", kindNATSWS, true, cluster.PresenceGone, StatusHealthy),
		Entry("nats and websocket, both lost", kindNATSWS, false, cluster.PresenceGone, StatusUnhealthy),
	)

	It("detects a gone tunnel of a tunnel-only node at once, with no setting of the cluster involved", func() {
		node("n1", kindWS, false, cluster.PresenceGone)
		hm.doCheckAll(context.Background())
		Expect(store.getNode("n1").Status).To(Equal(StatusUnhealthy), "presence already waited the grace")
		Expect(store.getCalls()).To(ContainElement("MarkUnhealthy:n1"))
	})

	It("reports a lost route of a node that keeps another live route, and leaves its status alone", func() {
		n := node("n1", kindNATSWS, false, cluster.PresenceConnected)
		tickPastGrace("n1")
		Expect(store.getCalls()).ToNot(ContainElement("MarkUnhealthy:n1"))

		states := hm.RouteJudge().States(context.Background(), n, clock.now())
		Expect(states).To(HaveLen(3))
		Expect(states[0].Route).To(Equal(RouteHeartbeat))
		Expect(states[0].State).To(Equal(RouteLive))
		Expect(states[1].Route).To(Equal(RouteNATS))
		Expect(states[1].State).To(Equal(RouteDown))
		Expect(states[1].Reason).To(Equal("connecting"))
		Expect(states[2].Route).To(Equal(RouteTunnelID))
		Expect(states[2].Transport).To(Equal(TunnelWebSocket))
		Expect(states[2].State).To(Equal(RouteLive))
	})

	It("does not flap a node whose only route reconnects inside the grace", func() {
		n := node("n1", kindNATS, false, cluster.PresenceUnknown)
		hm.doCheckAll(context.Background())
		clock.add(grace / 2)
		hm.doCheckAll(context.Background())
		Expect(store.getNode("n1").Status).To(Equal(StatusHealthy))

		store.mu.Lock()
		n = store.nodes["n1"]
		n.Attached, n.FollowError, n.LastHeartbeat = "nats", "", clock.now()
		store.mu.Unlock()
		hm.doCheckAll(context.Background())

		// The route was lost again: the grace starts again from now, not from
		// the first loss.
		store.mu.Lock()
		store.nodes["n1"].Attached = ""
		store.mu.Unlock()
		clock.add(grace / 2)
		hm.doCheckAll(context.Background())
		clock.add(grace / 2)
		hm.doCheckAll(context.Background())

		for _, c := range store.getCalls() {
			Expect(c).ToNot(Equal("MarkUnhealthy:n1"))
			Expect(c).ToNot(Equal("MarkHealthy:n1"))
		}
	})

	It("demotes a node with no live route once, does not probe it, and promotes it when a route is back", func() {
		hm.perModelHealthCheck = true
		node("n1", kindNATS, false, cluster.PresenceUnknown)
		store.addNodeModel("n1", NodeModel{NodeID: "n1", ModelName: "m", Address: "127.0.0.1:50053"})
		factory.setClient("127.0.0.1:50053", &fakeBackendClient{healthy: false})
		tickPastGrace("n1")
		for range perModelMissThreshold {
			hm.doCheckAll(context.Background())
		}
		count := 0
		for _, c := range store.getCalls() {
			if c == "MarkUnhealthy:n1" {
				count++
			}
		}
		Expect(count).To(Equal(1))
		Expect(store.getCalls()).ToNot(ContainElement(ContainSubstring("RemoveNodeModel")))
		Expect(hm.misses).To(BeEmpty())

		store.mu.Lock()
		store.nodes["n1"].Attached = "nats"
		store.mu.Unlock()
		hm.doCheckAll(context.Background())
		Expect(store.getCalls()).To(ContainElement("MarkHealthy:n1"))
	})

	It("takes a node offline as before when the heartbeat is stale and no route is live", func() {
		n := node("n1", kindNATS, true, cluster.PresenceUnknown)
		n.LastHeartbeat = clock.now().Add(-2 * staleThreshold)
		store.addNode(n)
		hm.doCheckAll(context.Background())
		Expect(store.getCalls()).To(ContainElement("MarkOffline:n1"), "the NATS report rides on the heartbeat, so a stale one does not count")
	})

	It("marks such a node unhealthy instead when auto-offline is off, as before", func() {
		hm.autoOffline = false
		n := node("n1", kindWS, false, cluster.PresenceGone)
		n.LastHeartbeat = clock.now().Add(-2 * staleThreshold)
		store.addNode(n)
		hm.doCheckAll(context.Background())
		Expect(store.getCalls()).To(ContainElement("MarkUnhealthy:n1"))
		Expect(store.getCalls()).ToNot(ContainElement(ContainSubstring("MarkOffline")))
	})

	It("keeps a node whose heartbeat is stale while a frontend holds its tunnel", func() {
		n := node("n1", kindWS, false, cluster.PresenceConnected)
		n.LastHeartbeat = clock.now().Add(-2 * staleThreshold)
		store.addNode(n)
		hm.doCheckAll(context.Background())
		Expect(store.getNode("n1").Status).To(Equal(StatusHealthy))
		Expect(store.getCalls()).ToNot(ContainElement(ContainSubstring("MarkOffline")))
		Expect(store.getCalls()).ToNot(ContainElement(ContainSubstring("MarkUnhealthy")))
	})

	It("does not count a reconnecting tunnel when the heartbeat is stale too", func() {
		n := node("n1", kindWS, false, cluster.PresenceReconnecting)
		n.LastHeartbeat = clock.now().Add(-2 * staleThreshold)
		store.addNode(n)
		hm.doCheckAll(context.Background())
		Expect(store.getCalls()).To(ContainElement("MarkOffline:n1"))
	})

	It("judges a node that has not sent its route report by its heartbeat alone", func() {
		store.addNode(makeTestNode("n1", "n1", "10.0.0.1:50051", StatusUnhealthy, clock.now()))
		presence.by["n1"] = cluster.PresenceGone
		hm.doCheckAll(context.Background())
		Expect(store.getCalls()).To(ContainElement("MarkHealthy:n1"))
		Expect(presence.graces).To(BeEmpty())
	})

	It("starts the grace again when this frontend has not looked at the node for several ticks", func() {
		node("n1", kindNATS, false, cluster.PresenceUnknown)
		hm.doCheckAll(context.Background())
		// Another frontend held the lock for these ticks.
		clock.add(grace + time.Second)
		store.mu.Lock()
		store.nodes["n1"].LastHeartbeat = clock.now()
		store.mu.Unlock()
		hm.doCheckAll(context.Background())
		Expect(store.getCalls()).ToNot(ContainElement("MarkUnhealthy:n1"))
		tickPastGrace("n1")
		Expect(store.getCalls()).To(ContainElement("MarkUnhealthy:n1"))
	})

	It("forgets the grace of a node that is no longer listed", func() {
		node("n1", kindNATS, false, cluster.PresenceUnknown)
		hm.doCheckAll(context.Background())
		Expect(hm.noRouteSince).To(HaveKey("n1"))
		store.mu.Lock()
		delete(store.nodes, "n1")
		store.mu.Unlock()
		hm.doCheckAll(context.Background())
		Expect(hm.noRouteSince).To(BeEmpty())
		Expect(hm.downLogged).To(BeEmpty())
	})
})

var _ = Describe("The route state of a node", func() {
	now := time.Now()
	judge := RouteJudge{StaleThreshold: time.Minute}

	It("names the transport of a tunnel from the report, or P2P for a node pinned to a peer", func() {
		n := &BackendNode{ID: "n", LastHeartbeat: now, Follow: "tunnel", Attached: "tunnel", PeerID: "12D3KooWpeer"}
		states := judge.States(context.Background(), n, now)
		Expect(states).To(HaveLen(2))
		Expect(states[1].Transport).To(Equal(TunnelP2P))
		Expect(states[1].State).To(Equal(RouteLive))
	})

	It("takes the reason of each route from the route error of the worker", func() {
		n := &BackendNode{ID: "n", LastHeartbeat: now, Follow: "nats,tunnel", FollowError: "nats: connecting; tunnel: the frontend refused the token"}
		states := judge.States(context.Background(), n, now)
		Expect(states[1].Reason).To(Equal("connecting"))
		Expect(states[2].Reason).To(Equal("the frontend refused the token"))
	})

	It("says not attached when the worker gave no reason", func() {
		n := &BackendNode{ID: "n", LastHeartbeat: now, Follow: "nats"}
		Expect(judge.States(context.Background(), n, now)[1].Reason).To(Equal("not attached"))
	})

	It("marks every route down when the heartbeat is stale and no presence answers", func() {
		n := &BackendNode{ID: "n", LastHeartbeat: now.Add(-time.Hour), Follow: "nats,tunnel", Attached: "nats,tunnel"}
		for _, s := range judge.States(context.Background(), n, now) {
			Expect(s.State).To(Equal(RouteDown), s.Route)
		}
	})

	It("uses the report of the worker when presence cannot be read", func() {
		j := judge
		j.Presence = &fakePresence{err: context.DeadlineExceeded}
		n := &BackendNode{ID: "n", LastHeartbeat: now, Follow: "tunnel", Attached: "tunnel"}
		Expect(j.States(context.Background(), n, now)[1].State).To(Equal(RouteLive))
	})
})

var _ = Describe("The health monitor in a real cluster", func() {
	It("judges each node by its routes", func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		ctx := context.Background()
		db := testutil.SetupTestDB()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		reg, err := NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		clusterR := cluster.NewRegistry(db)
		Expect(clusterR.Register(ctx, "replica-a", "test")).To(Succeed())

		natsNode := &BackendNode{Name: "nats-worker", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051"}
		Expect(reg.Register(ctx, natsNode, true)).To(Succeed())
		Expect(reg.SetRouteReport(ctx, natsNode.ID, RouteReport{
			Attached: []string{RouteNATS}, Follow: []string{RouteNATS},
		})).To(Succeed())

		wsNode := &BackendNode{Name: "ws-worker", NodeType: NodeTypeBackend}
		Expect(reg.Register(ctx, wsNode, true)).To(Succeed())
		Expect(reg.SetRouteReport(ctx, wsNode.ID, RouteReport{
			Attached: []string{RouteTunnelID}, Follow: []string{RouteTunnelID},
			Tunnel: TunnelRoute{Transport: TunnelWebSocket},
		})).To(Succeed())
		epoch, err := clusterR.Claim(ctx, wsNode.ID, "replica-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(clusterR.Release(ctx, wsNode.ID, "replica-a", epoch)).To(Succeed())
		Expect(db.Exec(`UPDATE node_connections SET disconnected_at = now() - interval '10 minutes' WHERE node_id = ?`, wsNode.ID).Error).To(Succeed())

		hm := NewHealthMonitor(reg, nil, 15*time.Second, time.Hour, "", false)
		hm.UsePresence(clusterR, time.Minute)

		hm.doCheckAll(ctx)
		got, err := reg.Get(ctx, natsNode.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(got.Status).To(Equal(StatusHealthy), "a NATS worker")
		got, err = reg.Get(ctx, wsNode.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(got.Status).To(Equal(StatusUnhealthy), "a gone tunnel")
	})
})

func joinStrings(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ","
		}
		out += p
	}
	return out
}
