package nodes

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// fakePresence says what each node's tunnel is, and counts the questions.
type fakePresence struct {
	mu     sync.Mutex
	by     map[string]cluster.Presence
	err    error
	graces []time.Duration
}

func (f *fakePresence) Presence(_ context.Context, nodeID string, grace time.Duration) (cluster.Presence, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.graces = append(f.graces, grace)
	if f.err != nil {
		return cluster.PresenceUnknown, f.err
	}
	return f.by[nodeID], nil
}

var _ = Describe("The health monitor when the tunnel is the active carrier", func() {
	const (
		staleThreshold = 30 * time.Second
		grace          = 45 * time.Second
	)
	var (
		store    *fakeNodeHealthStore
		factory  *fakeBackendClientFactory
		presence *fakePresence
		active   bool
		hm       *HealthMonitor
	)

	BeforeEach(func() {
		store = newFakeNodeHealthStore()
		factory = newFakeBackendClientFactory()
		presence = &fakePresence{by: map[string]cluster.Presence{}}
		active = true
		hm = newTestHealthMonitor(store, factory, true, staleThreshold)
		hm.perModelHealthCheck = true
		hm.UsePresence(presence, grace, func() bool { return active })
	})

	heartbeating := func(id, status string) {
		store.addNode(makeTestNode(id, id, "", status, freshTime()))
		store.addNodeModel(id, NodeModel{NodeID: id, ModelName: "m", Address: "127.0.0.1:50053"})
	}

	It("demotes a node that heartbeats and whose tunnel has been gone longer than the grace, and reaps nothing", func() {
		heartbeating("n1", StatusHealthy)
		presence.by["n1"] = cluster.PresenceGone

		hm.doCheckAll(context.Background())

		Expect(store.getNode("n1").Status).To(Equal(StatusUnhealthy))
		calls := store.getCalls()
		Expect(calls).To(ContainElement("MarkUnhealthy:n1"))
		Expect(calls).ToNot(ContainElement(ContainSubstring("MarkOffline")), "status only: a read of presence must not delete rows")
		Expect(calls).ToNot(ContainElement(ContainSubstring("RemoveNodeModel")))
		Expect(presence.graces).To(Equal([]time.Duration{grace}))
	})

	It("does not probe the backends of a node it cannot reach, so no probe can count against a model", func() {
		heartbeating("n1", StatusHealthy)
		presence.by["n1"] = cluster.PresenceGone
		factory.setClient("127.0.0.1:50053", &fakeBackendClient{healthy: false})
		for range perModelMissThreshold + 1 {
			hm.doCheckAll(context.Background())
		}
		Expect(store.getCalls()).ToNot(ContainElement(ContainSubstring("RemoveNodeModel")))
		Expect(hm.misses).To(BeEmpty())
	})

	DescribeTable("leaves a node alone for every answer that is not a departure older than the grace",
		func(p cluster.Presence) {
			heartbeating("n1", StatusHealthy)
			presence.by["n1"] = p
			hm.doCheckAll(context.Background())
			Expect(store.getNode("n1").Status).To(Equal(StatusHealthy))
			Expect(store.getCalls()).ToNot(ContainElement(ContainSubstring("MarkUnhealthy")))
		},
		Entry("a tunnel that a live replica holds", cluster.PresenceConnected),
		Entry("a worker that is reconnecting now", cluster.PresenceReconnecting),
		Entry("a worker that never dialled, or whose departure aged out", cluster.PresenceUnknown),
	)

	It("leaves a node alone when presence cannot be read, because a failed read is not an answer", func() {
		heartbeating("n1", StatusHealthy)
		presence.err = errors.New("database is away")
		hm.doCheckAll(context.Background())
		Expect(store.getNode("n1").Status).To(Equal(StatusHealthy))
		Expect(store.getCalls()).ToNot(ContainElement(ContainSubstring("MarkUnhealthy")))
	})

	It("does not ask about presence when NATS is the active carrier, where no node holds a tunnel", func() {
		active = false
		heartbeating("n1", StatusHealthy)
		presence.by["n1"] = cluster.PresenceGone
		hm.doCheckAll(context.Background())
		Expect(store.getNode("n1").Status).To(Equal(StatusHealthy))
		Expect(presence.graces).To(BeEmpty())
	})

	It("does not mark a node unhealthy again on every tick, and does not promote it while its tunnel is gone", func() {
		heartbeating("n1", StatusHealthy)
		presence.by["n1"] = cluster.PresenceGone
		for range 4 {
			hm.doCheckAll(context.Background())
		}
		count := 0
		for _, c := range store.getCalls() {
			if c == "MarkUnhealthy:n1" {
				count++
			}
			Expect(c).ToNot(Equal("MarkHealthy:n1"))
		}
		Expect(count).To(Equal(1))
		Expect(store.getNode("n1").Status).To(Equal(StatusUnhealthy))
	})

	It("promotes the node again when its tunnel is back", func() {
		heartbeating("n1", StatusUnhealthy)
		presence.by["n1"] = cluster.PresenceConnected
		hm.doCheckAll(context.Background())
		Expect(store.getCalls()).To(ContainElement("MarkHealthy:n1"))
	})

	It("reads the departure of an agent worker as it reads that of a backend worker, because an agent worker holds a tunnel too", func() {
		agent := makeTestNode("a1", "a1", "", StatusHealthy, freshTime())
		agent.NodeType = NodeTypeAgent
		store.addNode(agent)
		presence.by["a1"] = cluster.PresenceGone
		hm.doCheckAll(context.Background())
		Expect(store.getNode("a1").Status).To(Equal(StatusUnhealthy))
	})

	It("still takes a stale heartbeat offline, as before", func() {
		store.addNode(makeTestNode("n2", "n2", "", StatusHealthy, staleTime(staleThreshold)))
		hm.doCheckAll(context.Background())
		Expect(store.getCalls()).To(ContainElement("MarkOffline:n2"))
	})

	It("leaves a draining node to the operator", func() {
		store.addNode(makeTestNode("n3", "n3", "", StatusDraining, freshTime()))
		presence.by["n3"] = cluster.PresenceGone
		hm.doCheckAll(context.Background())
		Expect(store.getNode("n3").Status).To(Equal(StatusDraining))
	})

	It("takes the default grace of the deployment when it is built with none", func() {
		other := newTestHealthMonitor(store, factory, true, staleThreshold)
		other.UsePresence(presence, 0, func() bool { return true })
		heartbeating("n1", StatusHealthy)
		presence.by["n1"] = cluster.PresenceGone
		other.doCheckAll(context.Background())
		Expect(presence.graces).ToNot(BeEmpty())
		Expect(presence.graces[0]).To(BeNumerically(">", 0))
	})
})

var _ = Describe("The health monitor and the connection rows of a real cluster", func() {
	It("demotes a heartbeating node whose tunnel left longer ago than the grace, and promotes it when the tunnel is held again", func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		ctx := context.Background()
		db := testutil.SetupTestDB()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		reg, err := NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		clusterR := cluster.NewRegistry(db)

		node := &BackendNode{Name: "w1", NodeType: NodeTypeBackend}
		Expect(reg.Register(ctx, node, true)).To(Succeed())
		Expect(clusterR.Register(ctx, "replica-a", "test", 0, "")).To(Succeed())

		hm := NewHealthMonitor(reg, nil, 15*time.Second, time.Hour, "", false)
		hm.UsePresence(clusterR, time.Minute, func() bool { return true })

		epoch, err := clusterR.Claim(ctx, node.ID, "replica-a")
		Expect(err).ToNot(HaveOccurred())
		hm.doCheckAll(ctx)
		got, err := reg.Get(ctx, node.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(got.Status).To(Equal(StatusHealthy))

		Expect(clusterR.Release(ctx, node.ID, "replica-a", epoch)).To(Succeed())
		hm.doCheckAll(ctx)
		got, _ = reg.Get(ctx, node.ID)
		Expect(got.Status).To(Equal(StatusHealthy), "inside the grace the worker is reconnecting, and nobody acts on that")

		Expect(db.Exec(`UPDATE node_connections SET disconnected_at = now() - interval '10 minutes' WHERE node_id = ?`, node.ID).Error).To(Succeed())
		hm.doCheckAll(ctx)
		got, _ = reg.Get(ctx, node.ID)
		Expect(got.Status).To(Equal(StatusUnhealthy))

		_, err = clusterR.Claim(ctx, node.ID, "replica-a")
		Expect(err).ToNot(HaveOccurred())
		hm.doCheckAll(ctx)
		got, _ = reg.Get(ctx, node.ID)
		Expect(got.Status).To(Equal(StatusHealthy))
	})
})

var _ = Describe("The health monitor and the carriers a worker reports", func() {
	var (
		store   *fakeNodeHealthStore
		factory *fakeBackendClientFactory
		active  cluster.Carrier
		settled bool
		hm      *HealthMonitor
	)

	BeforeEach(func() {
		store = newFakeNodeHealthStore()
		factory = newFakeBackendClientFactory()
		active, settled = cluster.CarrierNATS, true
		hm = newTestHealthMonitor(store, factory, true, 30*time.Second)
		hm.perModelHealthCheck = true
		hm.UseCarrier(func() (cluster.Carrier, bool) { return active, settled })
	})

	node := func(id, status, attached, follow string) {
		n := makeTestNode(id, id, "10.0.0.1:50051", status, freshTime())
		n.Attached, n.Follow = attached, follow
		store.addNode(n)
		store.addNodeModel(id, NodeModel{NodeID: id, ModelName: "m", Address: "127.0.0.1:50053"})
	}

	It("demotes a worker that reports it is not attached to the active carrier, and reaps nothing", func() {
		node("n1", StatusHealthy, "tunnel", "tunnel")
		hm.doCheckAll(context.Background())
		Expect(store.getNode("n1").Status).To(Equal(StatusUnhealthy))
		Expect(store.getCalls()).To(ContainElement("MarkUnhealthy:n1"))
		Expect(store.getCalls()).ToNot(ContainElement(ContainSubstring("MarkOffline")))
		Expect(store.getCalls()).ToNot(ContainElement(ContainSubstring("RemoveNodeModel")))
	})

	It("demotes it once and does not promote it while it stays off the active carrier", func() {
		node("n1", StatusHealthy, "tunnel", "tunnel")
		for range 4 {
			hm.doCheckAll(context.Background())
		}
		count := 0
		for _, c := range store.getCalls() {
			if c == "MarkUnhealthy:n1" {
				count++
			}
			Expect(c).ToNot(Equal("MarkHealthy:n1"))
		}
		Expect(count).To(Equal(1))
	})

	It("promotes it when it reports the active carrier", func() {
		node("n1", StatusUnhealthy, "nats", "nats,tunnel")
		hm.doCheckAll(context.Background())
		Expect(store.getCalls()).To(ContainElement("MarkHealthy:n1"))
	})

	It("leaves a worker that is attached to both carriers alone", func() {
		node("n1", StatusHealthy, "nats,tunnel", "nats,tunnel")
		hm.doCheckAll(context.Background())
		Expect(store.getNode("n1").Status).To(Equal(StatusHealthy))
	})

	It("leaves every worker alone while a change is under way or a carrier drains, because the window routes them", func() {
		settled = false
		node("n1", StatusHealthy, "tunnel", "tunnel")
		hm.doCheckAll(context.Background())
		Expect(store.getNode("n1").Status).To(Equal(StatusHealthy))
	})

	It("leaves a worker that reports nothing alone: it predates carrier switching", func() {
		node("n1", StatusHealthy, "", "")
		hm.doCheckAll(context.Background())
		Expect(store.getNode("n1").Status).To(Equal(StatusHealthy))
	})

	It("does not probe the backends of a worker that it demoted", func() {
		node("n1", StatusHealthy, "tunnel", "tunnel")
		factory.setClient("127.0.0.1:50053", &fakeBackendClient{healthy: false})
		for range perModelMissThreshold + 1 {
			hm.doCheckAll(context.Background())
		}
		Expect(store.getCalls()).ToNot(ContainElement(ContainSubstring("RemoveNodeModel")))
		Expect(hm.misses).To(BeEmpty())
	})

	It("does nothing when it was not told where to read the carrier", func() {
		hm.carrier = nil
		node("n1", StatusHealthy, "tunnel", "tunnel")
		hm.doCheckAll(context.Background())
		Expect(store.getNode("n1").Status).To(Equal(StatusHealthy))
	})
})
