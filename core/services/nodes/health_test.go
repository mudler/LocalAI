package nodes

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/cluster"

	"github.com/mudler/LocalAI/core/services/testutil"
	"gorm.io/gorm"
)

var _ = Describe("HealthMonitor", func() {
	var (
		db       *gorm.DB
		registry *NodeRegistry
		hm       *HealthMonitor
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())

		// Use a 30-second stale threshold for tests.
		// Pass nil db to avoid advisory lock path (no distributed mode in tests).
		hm = NewHealthMonitor(registry, nil, 15*time.Second, 30*time.Second, "", false, nil, 0)
	})

	makeNode := func(name, address string, vram uint64) *BackendNode {
		return &BackendNode{
			Name:          name,
			NodeType:      NodeTypeBackend,
			Address:       address,
			TotalVRAM:     vram,
			AvailableVRAM: vram,
		}
	}

	Describe("doCheckAll", func() {
		It("marks stale node offline", func() {
			node := makeNode("stale-worker", "10.0.0.1:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(node.Status).To(Equal(StatusHealthy))

			// Set LastHeartbeat to 2 minutes ago (well beyond 30s threshold)
			staleTime := time.Now().Add(-2 * time.Minute)
			Expect(db.Model(&BackendNode{}).Where("id = ?", node.ID).
				Update("last_heartbeat", staleTime).Error).ToNot(HaveOccurred())

			hm.doCheckAll(context.Background())

			fetched, err := registry.Get(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusOffline))
		})

		It("skips draining nodes", func() {
			node := makeNode("draining-worker", "10.0.0.2:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			// Set status to draining
			Expect(db.Model(&BackendNode{}).Where("id = ?", node.ID).
				Update("status", StatusDraining).Error).ToNot(HaveOccurred())

			// Make heartbeat stale
			staleTime := time.Now().Add(-2 * time.Minute)
			Expect(db.Model(&BackendNode{}).Where("id = ?", node.ID).
				Update("last_heartbeat", staleTime).Error).ToNot(HaveOccurred())

			hm.doCheckAll(context.Background())

			fetched, err := registry.Get(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusDraining))
		})

		It("skips idle nodes with no loaded models", func() {
			node := makeNode("idle-worker", "10.0.0.3:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			// Heartbeat is fresh (just registered), no models loaded.
			// doCheckAll should not change status (no gRPC check attempted).
			hm.doCheckAll(context.Background())

			fetched, err := registry.Get(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusHealthy))
		})

		It("recovers unhealthy node when heartbeat is fresh", func() {
			node := makeNode("unhealthy-worker", "10.0.0.5:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(node.Status).To(Equal(StatusHealthy))

			// Mark unhealthy
			Expect(registry.MarkUnhealthy(context.Background(), node.ID)).To(Succeed())
			fetched, err := registry.Get(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusUnhealthy))

			// Refresh heartbeat (simulates the worker sending a heartbeat)
			Expect(db.Model(&BackendNode{}).Where("id = ?", node.ID).
				Update("last_heartbeat", time.Now()).Error).ToNot(HaveOccurred())

			hm.doCheckAll(context.Background())

			fetched, err = registry.Get(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusHealthy))
		})

		It("does not change healthy nodes with fresh heartbeat", func() {
			node := makeNode("fresh-worker", "10.0.0.4:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			// Update heartbeat to now so it is definitely fresh
			Expect(db.Model(&BackendNode{}).Where("id = ?", node.ID).
				Update("last_heartbeat", time.Now()).Error).ToNot(HaveOccurred())

			hm.doCheckAll(context.Background())

			fetched, err := registry.Get(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusHealthy))
		})
	})
})

// --- Mock-based tests (no DB required) ---

var _ = Describe("HealthMonitor (mock-based)", func() {
	const staleThreshold = 30 * time.Second

	Describe("doCheckAll", func() {
		It("marks stale node offline when autoOffline=true", func() {
			store := newFakeNodeHealthStore()
			factory := newFakeBackendClientFactory()
			hm := newTestHealthMonitor(store, factory, true, staleThreshold)

			node := makeTestNode("node-1", "stale-worker", "10.0.0.1:50051", StatusHealthy, staleTime(staleThreshold))
			store.addNode(node)

			hm.doCheckAll(context.Background())

			Expect(store.getNode("node-1").Status).To(Equal(StatusOffline))
			Expect(store.getCalls()).To(ContainElement("MarkOffline:node-1"))
		})

		It("marks stale node unhealthy when autoOffline=false", func() {
			store := newFakeNodeHealthStore()
			factory := newFakeBackendClientFactory()
			hm := newTestHealthMonitor(store, factory, false, staleThreshold)

			node := makeTestNode("node-2", "stale-worker-2", "10.0.0.2:50051", StatusHealthy, staleTime(staleThreshold))
			store.addNode(node)

			hm.doCheckAll(context.Background())

			Expect(store.getNode("node-2").Status).To(Equal(StatusUnhealthy))
			Expect(store.getCalls()).To(ContainElement("MarkUnhealthy:node-2"))
		})

		It("skips draining nodes", func() {
			store := newFakeNodeHealthStore()
			factory := newFakeBackendClientFactory()
			hm := newTestHealthMonitor(store, factory, true, staleThreshold)

			node := makeTestNode("node-3", "draining-worker", "10.0.0.3:50051", StatusDraining, staleTime(staleThreshold))
			store.addNode(node)

			hm.doCheckAll(context.Background())

			// Should remain draining -- no MarkOffline or MarkUnhealthy
			Expect(store.getNode("node-3").Status).To(Equal(StatusDraining))
			calls := store.getCalls()
			Expect(calls).NotTo(ContainElement(ContainSubstring("MarkOffline")))
			Expect(calls).NotTo(ContainElement(ContainSubstring("MarkUnhealthy")))
		})

		It("skips idle nodes with no models", func() {
			store := newFakeNodeHealthStore()
			factory := newFakeBackendClientFactory()
			hm := newTestHealthMonitor(store, factory, true, staleThreshold)

			node := makeTestNode("node-4", "idle-worker", "10.0.0.4:50051", StatusHealthy, freshTime())
			store.addNode(node)
			// No models added for this node

			hm.doCheckAll(context.Background())

			// Should remain healthy -- no gRPC check attempted
			Expect(store.getNode("node-4").Status).To(Equal(StatusHealthy))
			calls := store.getCalls()
			Expect(calls).NotTo(ContainElement(ContainSubstring("MarkUnhealthy")))
			Expect(calls).NotTo(ContainElement(ContainSubstring("MarkOffline")))
		})

		It("keeps node healthy when heartbeat is fresh (with models loaded)", func() {
			store := newFakeNodeHealthStore()
			factory := newFakeBackendClientFactory()
			hm := newTestHealthMonitor(store, factory, true, staleThreshold)

			node := makeTestNode("node-5", "active-worker", "10.0.0.5:50051", StatusHealthy, freshTime())
			store.addNode(node)
			store.addNodeModel("node-5", NodeModel{NodeID: "node-5", ModelName: "llama-7b"})

			// No gRPC client needed — health is determined by heartbeat, not gRPC probe
			hm.doCheckAll(context.Background())

			Expect(store.getNode("node-5").Status).To(Equal(StatusHealthy))
			calls := store.getCalls()
			Expect(calls).NotTo(ContainElement(ContainSubstring("MarkUnhealthy")))
		})

		It("recovers unhealthy node when heartbeat is fresh", func() {
			store := newFakeNodeHealthStore()
			factory := newFakeBackendClientFactory()
			hm := newTestHealthMonitor(store, factory, true, staleThreshold)

			node := makeTestNode("node-6", "recovering-worker", "10.0.0.6:50051", StatusUnhealthy, freshTime())
			store.addNode(node)

			hm.doCheckAll(context.Background())

			Expect(store.getCalls()).To(ContainElement("MarkHealthy:node-6"))
			Expect(store.getNode("node-6").Status).To(Equal(StatusHealthy))
		})

		It("node stays healthy when gRPC backend crashes but heartbeat is fresh", func() {
			store := newFakeNodeHealthStore()
			factory := newFakeBackendClientFactory()
			hm := newTestHealthMonitor(store, factory, true, staleThreshold)

			// Worker has a model loaded but the backend process crashed —
			// node should remain healthy because heartbeat is fresh
			node := makeTestNode("node-crash", "crash-worker", "10.0.0.9:50051", StatusHealthy, freshTime())
			store.addNode(node)
			store.addNodeModel("node-crash", NodeModel{NodeID: "node-crash", ModelName: "piper-model", WorkerLocalAddress: "10.0.0.9:50053"})

			// gRPC backend is dead — but health is heartbeat-based, not gRPC-based
			factory.setClient("10.0.0.9:50051", &fakeBackendClient{healthy: false, err: fmt.Errorf("connection refused")})

			hm.doCheckAll(context.Background())

			Expect(store.getNode("node-crash").Status).To(Equal(StatusHealthy))
			calls := store.getCalls()
			Expect(calls).NotTo(ContainElement(ContainSubstring("MarkUnhealthy")))
		})

		It("removes stale model via per-model health check after consecutive failures", func() {
			store := newFakeNodeHealthStore()
			factory := newFakeBackendClientFactory()
			hm := newTestHealthMonitor(store, factory, true, staleThreshold)
			hm.perModelHealthCheck = true

			node := makeTestNode("node-model", "model-worker", "10.0.0.10:50051", StatusHealthy, freshTime())
			store.addNode(node)
			store.addNodeModel("node-model", NodeModel{NodeID: "node-model", ModelName: "piper-model", WorkerLocalAddress: "10.0.0.10:50053"})

			// Model backend is dead
			factory.setClient("10.0.0.10:50053", &fakeBackendClient{healthy: false, err: fmt.Errorf("connection refused")})

			// First (perModelMissThreshold-1) probes must NOT remove the row —
			// a single failure could be a transient blip.
			for i := 0; i < perModelMissThreshold-1; i++ {
				hm.doCheckAll(context.Background())
				Expect(store.getCalls()).NotTo(ContainElement(ContainSubstring("RemoveNodeModel")),
					"removed too early at miss %d", i+1)
			}

			// Threshold-th consecutive miss triggers removal.
			hm.doCheckAll(context.Background())

			// Node should remain healthy — only the specific replica record is removed.
			Expect(store.getNode("node-model").Status).To(Equal(StatusHealthy))
			Expect(store.getCalls()).To(ContainElement("RemoveNodeModel:node-model:piper-model:0"))
			Expect(store.getCalls()).NotTo(ContainElement(ContainSubstring("MarkUnhealthy")))
		})

		It("probes a model through its NODE, never by dialling the stored address", func() {
			// The address on a NodeModel row is a port inside the worker. This
			// frontend reaches it over the worker's tunnel, so the node has to
			// be part of every probe; a probe built from the address alone is
			// the direct dial the tunnel replaces.
			store := newFakeNodeHealthStore()
			factory := newFakeBackendClientFactory()
			hm := newTestHealthMonitor(store, factory, true, staleThreshold)
			hm.perModelHealthCheck = true

			node := makeTestNode("node-tun", "tun-worker", "10.0.0.20:50051", StatusHealthy, freshTime())
			store.addNode(node)
			store.addNodeModel("node-tun", NodeModel{NodeID: "node-tun", ModelName: "m", WorkerLocalAddress: "10.0.0.20:50053"})

			hm.doCheckAll(context.Background())
			Expect(factory.nodesSeen()).To(ContainElement("node-tun"))
		})

		It("leaves a model row alone when it cannot reach the worker at all", func() {
			// Not a miss. A frontend with no way to reach a worker has learned
			// nothing about that worker's backends, and counting it as a failed
			// probe would reap every model in the fleet the moment the tunnel
			// wiring broke.
			store := newFakeNodeHealthStore()
			factory := newFakeBackendClientFactory()
			factory.refuseForNode = fmt.Errorf("no tunnel for you")
			hm := newTestHealthMonitor(store, factory, true, staleThreshold)
			hm.perModelHealthCheck = true

			node := makeTestNode("node-cut", "cut-worker", "10.0.0.21:50051", StatusHealthy, freshTime())
			store.addNode(node)
			store.addNodeModel("node-cut", NodeModel{NodeID: "node-cut", ModelName: "m", WorkerLocalAddress: "10.0.0.21:50053"})

			for i := 0; i < perModelMissThreshold+1; i++ {
				hm.doCheckAll(context.Background())
			}
			Expect(store.getCalls()).NotTo(ContainElement(ContainSubstring("RemoveNodeModel")))
			Expect(store.getNode("node-cut").Status).To(Equal(StatusHealthy))
		})

		It("leaves a model row alone when the probe never reached the worker", func() {
			// The sibling of the factory case above, and the likelier one. The
			// client is built fine and the tunnel DIAL fails, which gRPC
			// reports with the same code as a dead backend. Counted as a miss
			// it would delete every model row in the fleet after three passes
			// of a peer link blip, while the models kept serving.
			store := newFakeNodeHealthStore()
			factory := newFakeBackendClientFactory()
			hm := newTestHealthMonitor(store, factory, true, staleThreshold)
			hm.perModelHealthCheck = true

			node := makeTestNode("node-blip", "blip-worker", "10.0.0.22:50051", StatusHealthy, freshTime())
			store.addNode(node)
			store.addNodeModel("node-blip", NodeModel{NodeID: "node-blip", ModelName: "m", WorkerLocalAddress: "10.0.0.22:50053"})
			factory.setClient("10.0.0.22:50053", &fakeBackendClient{
				healthy: false,
				err:     fmt.Errorf("connection error"),
				dialErr: fmt.Errorf("%w: %w", cluster.ErrNoRoute, cluster.ErrPeerUnreachable),
			})

			for i := 0; i < perModelMissThreshold+2; i++ {
				hm.doCheckAll(context.Background())
			}
			Expect(store.getCalls()).NotTo(ContainElement(ContainSubstring("RemoveNodeModel")))
		})

		It("still reaps a backend that died on a worker it CAN reach", func() {
			// The other direction, so the new check cannot pass by never
			// reaping. A dial that succeeded and an RPC that failed is a dead
			// process, and its row must still go.
			store := newFakeNodeHealthStore()
			factory := newFakeBackendClientFactory()
			hm := newTestHealthMonitor(store, factory, true, staleThreshold)
			hm.perModelHealthCheck = true

			node := makeTestNode("node-dead", "dead-worker", "10.0.0.23:50051", StatusHealthy, freshTime())
			store.addNode(node)
			store.addNodeModel("node-dead", NodeModel{NodeID: "node-dead", ModelName: "m", WorkerLocalAddress: "10.0.0.23:50053"})
			// No dialErr: the transport was fine.
			factory.setClient("10.0.0.23:50053", &fakeBackendClient{healthy: false, err: fmt.Errorf("connection refused")})

			for i := 0; i < perModelMissThreshold; i++ {
				hm.doCheckAll(context.Background())
			}
			Expect(store.getCalls()).To(ContainElement("RemoveNodeModel:node-dead:m:0"))
		})

		It("preserves model row when an intermittent failure is followed by a success", func() {
			store := newFakeNodeHealthStore()
			factory := newFakeBackendClientFactory()
			hm := newTestHealthMonitor(store, factory, true, staleThreshold)
			hm.perModelHealthCheck = true

			node := makeTestNode("node-flap", "flap-worker", "10.0.0.11:50051", StatusHealthy, freshTime())
			store.addNode(node)
			store.addNodeModel("node-flap", NodeModel{NodeID: "node-flap", ModelName: "piper-model", WorkerLocalAddress: "10.0.0.11:50053"})

			deadClient := &fakeBackendClient{healthy: false, err: fmt.Errorf("connection refused")}
			liveClient := &fakeBackendClient{healthy: true}

			// Two failing probes then a recovery — should NOT remove the row,
			// and should reset the miss counter so two more failures don't tip
			// it over.
			factory.setClient("10.0.0.11:50053", deadClient)
			hm.doCheckAll(context.Background())
			hm.doCheckAll(context.Background())
			factory.setClient("10.0.0.11:50053", liveClient)
			hm.doCheckAll(context.Background())

			Expect(store.getCalls()).NotTo(ContainElement(ContainSubstring("RemoveNodeModel")))

			// Counter is reset; two more failures must not be enough to remove.
			factory.setClient("10.0.0.11:50053", deadClient)
			hm.doCheckAll(context.Background())
			hm.doCheckAll(context.Background())
			Expect(store.getCalls()).NotTo(ContainElement(ContainSubstring("RemoveNodeModel")))
		})
	})
})

// The wedge this check exists to end.
//
// A worker's heartbeat says its supervisor is alive. It says nothing about
// whether anything in this deployment can reach the worker's BACKENDS, because
// those are reached over the worker's tunnel. Before these specs the two were
// conflated, and a worker that heartbeated with a permanently dead tunnel (a
// proxy that stopped upgrading WebSockets, a rotated credential, a reconnect
// loop longer than the grace) stayed listed HEALTHY forever while every request
// for a model already loaded on it failed "no route to that worker". No reaper
// was scheduled for it: every reaper keys on the heartbeat, and the heartbeat
// was fine.
//
// Driven through a real cluster.Registry with the departures aged on the
// DATABASE clock, because a double that answers a Presence value cannot show
// that the window is measured where every replica agrees on it.
var _ = Describe("HealthMonitor and a worker whose tunnel is gone", func() {
	var (
		ctx      context.Context
		db       *gorm.DB
		registry *NodeRegistry
		clusterR *cluster.Registry
		hm       *HealthMonitor
	)

	const (
		grace    = 60 * time.Second
		instance = "inst-health"
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		ctx = context.Background()
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		clusterR = cluster.NewRegistry(db)
		Expect(clusterR.Register(ctx, instance, "10.0.0.1:8080", "v1")).To(Succeed())
		hm = NewHealthMonitor(registry, nil, 15*time.Second, 30*time.Second, "", false, clusterR, grace)
	})

	// register creates a heartbeating backend worker. Its heartbeat stays fresh
	// for the whole spec, which is the precondition the wedge needs: a stale
	// heartbeat would take the OTHER branch and prove nothing about this one.
	register := func(name string) *BackendNode {
		GinkgoHelper()
		node := &BackendNode{Name: name, NodeType: NodeTypeBackend, TotalVRAM: 8_000_000_000, AvailableVRAM: 8_000_000_000}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		Expect(node.Status).To(Equal(StatusHealthy))
		return node
	}

	statusOf := func(id string) string {
		GinkgoHelper()
		n, err := registry.Get(ctx, id)
		Expect(err).ToNot(HaveOccurred())
		return n.Status
	}

	// departTunnel gives a node a connection row, releases it, and ages the
	// departure by `by` ON THE DATABASE CLOCK. A Go-side timestamp would be
	// compared against the database's, which is the skew this window is written
	// to be immune to.
	departTunnel := func(nodeID string, by time.Duration) {
		GinkgoHelper()
		epoch, err := clusterR.Claim(ctx, nodeID, instance)
		Expect(err).ToNot(HaveOccurred())
		Expect(clusterR.Release(ctx, nodeID, instance, epoch)).To(Succeed())
		res := db.WithContext(ctx).Exec(
			`UPDATE node_connections SET disconnected_at = now() - make_interval(secs => ?) WHERE node_id = ?`,
			by.Seconds(), nodeID)
		Expect(res.Error).ToNot(HaveOccurred())
		Expect(res.RowsAffected).To(Equal(int64(1)),
			"precondition: the departure this spec ages must exist, or the spec proves nothing")
	}

	It("stops reporting a heartbeating node healthy once its tunnel has been gone past the grace", func() {
		node := register("wedged-worker")
		departTunnel(node.ID, grace+5*time.Second)

		hm.doCheckAll(ctx)

		Expect(statusOf(node.ID)).To(Equal(StatusUnhealthy))
	})

	It("does not delete the node's model rows, because a demotion is enough to unwedge it", func() {
		// Routing and eviction both select on status=healthy, so demoting stops
		// the model being served from here and the next request places it
		// somewhere reachable. Deleting rows would give any future defect in
		// the presence read the largest blast radius in the system, for nothing
		// the demotion does not already deliver.
		node := register("wedged-with-models")
		Expect(registry.SetNodeModel(ctx, node.ID, "llama", 0, "loaded", "127.0.0.1:50100", 0)).To(Succeed())
		departTunnel(node.ID, grace+5*time.Second)

		hm.doCheckAll(ctx)

		models, err := registry.GetNodeModels(ctx, node.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(models).To(HaveLen(1))
	})

	It("does not re-promote a demoted node whose tunnel is still gone, however fresh its heartbeat", func() {
		// The re-promotion branch used to fire on a fresh heartbeat alone, so
		// the scheduler's demotion was undone on the next tick, every tick. Its
		// cross-replica value was about one health interval.
		node := register("still-gone")
		departTunnel(node.ID, grace+5*time.Second)
		Expect(registry.MarkUnhealthy(ctx, node.ID)).To(Succeed())

		hm.doCheckAll(ctx)

		Expect(statusOf(node.ID)).To(Equal(StatusUnhealthy))
	})

	It("re-promotes a demoted node once its tunnel is back", func() {
		// The negative control for the spec above: without this, refusing to
		// re-promote ANYTHING would satisfy it.
		node := register("recovered")
		departTunnel(node.ID, grace+5*time.Second)
		Expect(registry.MarkUnhealthy(ctx, node.ID)).To(Succeed())
		_, err := clusterR.Claim(ctx, node.ID, instance)
		Expect(err).ToNot(HaveOccurred())

		hm.doCheckAll(ctx)

		Expect(statusOf(node.ID)).To(Equal(StatusHealthy))
	})

	It("leaves a node whose tunnel went inside the grace alone", func() {
		// A worker re-dialling the load balancer right now. Demoting it is the
		// fleet-wide eviction on a rolling frontend restart.
		node := register("re-homing")
		departTunnel(node.ID, grace/2)

		hm.doCheckAll(ctx)

		Expect(statusOf(node.ID)).To(Equal(StatusHealthy))
	})

	It("leaves a node that has never dialled a tunnel alone", func() {
		// No connection row at all, which is also every worker in the window
		// between registering and dialling.
		node := register("never-dialled")

		hm.doCheckAll(ctx)

		Expect(statusOf(node.ID)).To(Equal(StatusHealthy))
	})

	It("leaves an AGENT node alone even when its tunnel would read as gone", func() {
		// Agent workers hold no tunnel and still take their one verb over the
		// bus. A departure row for one is not a fact about it.
		node := &BackendNode{Name: "agent-worker", NodeType: NodeTypeAgent}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		departTunnel(node.ID, grace+5*time.Second)

		hm.doCheckAll(ctx)

		Expect(statusOf(node.ID)).To(Equal(StatusHealthy))
	})

	It("leaves every node alone when it has no presence reader", func() {
		// A single-node deployment has nothing that can say a worker is gone.
		node := register("no-cluster-registry")
		departTunnel(node.ID, grace+5*time.Second)
		plain := NewHealthMonitor(registry, nil, 15*time.Second, 30*time.Second, "", false, nil, 0)

		plain.doCheckAll(ctx)

		Expect(statusOf(node.ID)).To(Equal(StatusHealthy))
		Expect(plain.ReadsAbsence()).To(BeFalse())
	})

	It("falls back to the documented grace when built with a presence reader and no grace", func() {
		// Symmetric with the scheduler's default. A zero window here would make
		// every departure a verdict the instant it was stamped, and every spec
		// above passes an explicit grace, so nothing else reaches this.
		node := register("no-grace")
		stub := &stubPresence{answer: cluster.PresenceConnected}
		defaulted := NewHealthMonitor(registry, nil, 15*time.Second, 30*time.Second, "", false, stub, 0)

		defaulted.doCheckAll(ctx)

		Expect(stub.nodes).To(ContainElement(node.ID))
		Expect(stub.graces).To(ContainElement(config.DefaultWorkerReconnectGrace))
	})

	It("leaves a node alone when the presence query fails", func() {
		// A database hiccup must not demote the fleet. Driven with a stub,
		// because a real registry cannot be made to fail on demand.
		node := register("query-fails")
		broken := NewHealthMonitor(registry, nil, 15*time.Second, 30*time.Second, "", false,
			&stubPresence{err: errors.New("connection reset")}, grace)

		broken.doCheckAll(ctx)

		Expect(statusOf(node.ID)).To(Equal(StatusHealthy))
	})
})
