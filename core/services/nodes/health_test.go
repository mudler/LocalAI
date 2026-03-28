package nodes

import (
	"context"
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
		hm = NewHealthMonitor(registry, nil, 15*time.Second, 30*time.Second)
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
			Expect(registry.Register(node, true)).To(Succeed())
			Expect(node.Status).To(Equal(StatusHealthy))

			// Set LastHeartbeat to 2 minutes ago (well beyond 30s threshold)
			staleTime := time.Now().Add(-2 * time.Minute)
			Expect(db.Model(&BackendNode{}).Where("id = ?", node.ID).
				Update("last_heartbeat", staleTime).Error).ToNot(HaveOccurred())

			hm.doCheckAll(context.Background())

			fetched, err := registry.Get(node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusOffline))
		})

		It("skips draining nodes", func() {
			node := makeNode("draining-worker", "10.0.0.2:50051", 8_000_000_000)
			Expect(registry.Register(node, true)).To(Succeed())

			// Set status to draining
			Expect(db.Model(&BackendNode{}).Where("id = ?", node.ID).
				Update("status", StatusDraining).Error).ToNot(HaveOccurred())

			// Make heartbeat stale
			staleTime := time.Now().Add(-2 * time.Minute)
			Expect(db.Model(&BackendNode{}).Where("id = ?", node.ID).
				Update("last_heartbeat", staleTime).Error).ToNot(HaveOccurred())

			hm.doCheckAll(context.Background())

			fetched, err := registry.Get(node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusDraining))
		})

		It("skips idle nodes with no loaded models", func() {
			node := makeNode("idle-worker", "10.0.0.3:50051", 8_000_000_000)
			Expect(registry.Register(node, true)).To(Succeed())

			// Heartbeat is fresh (just registered), no models loaded.
			// doCheckAll should not change status (no gRPC check attempted).
			hm.doCheckAll(context.Background())

			fetched, err := registry.Get(node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusHealthy))
		})

		It("does not change healthy nodes with fresh heartbeat", func() {
			node := makeNode("fresh-worker", "10.0.0.4:50051", 8_000_000_000)
			Expect(registry.Register(node, true)).To(Succeed())

			// Update heartbeat to now so it is definitely fresh
			Expect(db.Model(&BackendNode{}).Where("id = ?", node.ID).
				Update("last_heartbeat", time.Now()).Error).ToNot(HaveOccurred())

			hm.doCheckAll(context.Background())

			fetched, err := registry.Get(node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusHealthy))
		})
	})
})
