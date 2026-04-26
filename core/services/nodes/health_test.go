package nodes

import (
	"context"
	"fmt"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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
		hm = NewHealthMonitor(registry, nil, 15*time.Second, 30*time.Second, "", false)
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
			store.addNodeModel("node-crash", NodeModel{NodeID: "node-crash", ModelName: "piper-model", Address: "10.0.0.9:50053"})

			// gRPC backend is dead — but health is heartbeat-based, not gRPC-based
			factory.setClient("10.0.0.9:50051", &fakeBackendClient{healthy: false, err: fmt.Errorf("connection refused")})

			hm.doCheckAll(context.Background())

			Expect(store.getNode("node-crash").Status).To(Equal(StatusHealthy))
			calls := store.getCalls()
			Expect(calls).NotTo(ContainElement(ContainSubstring("MarkUnhealthy")))
		})

		It("removes stale model via per-model health check without affecting node status", func() {
			store := newFakeNodeHealthStore()
			factory := newFakeBackendClientFactory()
			hm := newTestHealthMonitor(store, factory, true, staleThreshold)
			hm.perModelHealthCheck = true

			node := makeTestNode("node-model", "model-worker", "10.0.0.10:50051", StatusHealthy, freshTime())
			store.addNode(node)
			store.addNodeModel("node-model", NodeModel{NodeID: "node-model", ModelName: "piper-model", Address: "10.0.0.10:50053"})

			// Model backend is dead
			factory.setClient("10.0.0.10:50053", &fakeBackendClient{healthy: false, err: fmt.Errorf("connection refused")})

			hm.doCheckAll(context.Background())

			// Node should remain healthy — only the model record is removed
			Expect(store.getNode("node-model").Status).To(Equal(StatusHealthy))
			Expect(store.getCalls()).To(ContainElement("RemoveNodeModel:node-model:piper-model"))
			Expect(store.getCalls()).NotTo(ContainElement(ContainSubstring("MarkUnhealthy")))
		})
	})
})
