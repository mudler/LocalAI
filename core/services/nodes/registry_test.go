package nodes

import (
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/testutil"
	"gorm.io/gorm"
)

var _ = Describe("NodeRegistry", func() {
	var (
		db       *gorm.DB
		registry *NodeRegistry
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
	})

	// Helper to build a minimal BackendNode.
	makeNode := func(name, address string, vram uint64) *BackendNode {
		return &BackendNode{
			Name:          name,
			NodeType:      NodeTypeBackend,
			Address:       address,
			TotalVRAM:     vram,
			AvailableVRAM: vram,
		}
	}

	Describe("Register", func() {
		It("sets StatusPending when autoApprove is false", func() {
			node := makeNode("worker-1", "10.0.0.1:50051", 8_000_000_000)
			Expect(registry.Register(node, false)).To(Succeed())
			Expect(node.Status).To(Equal(StatusPending))

			fetched, err := registry.GetByName("worker-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusPending))
		})

		It("sets StatusHealthy when autoApprove is true", func() {
			node := makeNode("worker-2", "10.0.0.2:50051", 4_000_000_000)
			Expect(registry.Register(node, true)).To(Succeed())
			Expect(node.Status).To(Equal(StatusHealthy))
		})
	})

	Describe("Re-registration", func() {
		It("keeps a pending node pending on re-register with autoApprove=false", func() {
			node := makeNode("re-pending", "10.0.0.3:50051", 4_000_000_000)
			Expect(registry.Register(node, false)).To(Succeed())
			Expect(node.Status).To(Equal(StatusPending))

			// Re-register same name, still no auto-approve
			node2 := makeNode("re-pending", "10.0.0.3:50052", 4_000_000_000)
			Expect(registry.Register(node2, false)).To(Succeed())
			Expect(node2.Status).To(Equal(StatusPending))

			// ID is preserved from original registration
			Expect(node2.ID).To(Equal(node.ID))
		})

		It("restores a previously approved node to healthy on re-register with autoApprove=false", func() {
			node := makeNode("re-approved", "10.0.0.4:50051", 8_000_000_000)
			Expect(registry.Register(node, true)).To(Succeed())
			Expect(node.Status).To(Equal(StatusHealthy))

			// Simulate the node becoming unhealthy
			Expect(registry.MarkUnhealthy(node.ID)).To(Succeed())
			fetched, err := registry.GetByName("re-approved")
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusUnhealthy))

			// Re-register with autoApprove=false — should restore to healthy
			// because the node was previously approved (status != pending)
			node2 := makeNode("re-approved", "10.0.0.4:50052", 8_000_000_000)
			Expect(registry.Register(node2, false)).To(Succeed())
			Expect(node2.Status).To(Equal(StatusHealthy))
		})
	})

	Describe("ApproveNode", func() {
		It("transitions a pending node to healthy", func() {
			node := makeNode("approve-me", "10.0.0.5:50051", 4_000_000_000)
			Expect(registry.Register(node, false)).To(Succeed())
			Expect(node.Status).To(Equal(StatusPending))

			Expect(registry.ApproveNode(node.ID)).To(Succeed())

			fetched, err := registry.Get(node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusHealthy))
		})

		It("returns error for non-existent node ID", func() {
			err := registry.ApproveNode("non-existent-id")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found or not in pending status"))
		})

		It("returns error for an already-healthy node", func() {
			node := makeNode("already-healthy", "10.0.0.6:50051", 4_000_000_000)
			Expect(registry.Register(node, true)).To(Succeed())

			err := registry.ApproveNode(node.ID)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found or not in pending status"))
		})
	})

	Describe("MarkOffline", func() {
		It("sets status to offline and clears model records", func() {
			node := makeNode("offline-test", "10.0.0.7:50051", 8_000_000_000)
			Expect(registry.Register(node, true)).To(Succeed())

			// Load a model on the node
			Expect(registry.SetNodeModel(node.ID, "llama-7b", "loaded", "10.0.0.7:50052")).To(Succeed())
			models, err := registry.GetNodeModels(node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(HaveLen(1))

			// Mark offline
			Expect(registry.MarkOffline(node.ID)).To(Succeed())

			fetched, err := registry.Get(node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusOffline))

			// Model records should be cleared
			models, err = registry.GetNodeModels(node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(BeEmpty())
		})

		It("returns error for non-existent node", func() {
			err := registry.MarkOffline("does-not-exist")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
		})
	})

	Describe("FindNodeWithVRAM", func() {
		It("selects the node with sufficient VRAM", func() {
			small := makeNode("small-gpu", "10.0.0.10:50051", 4_000_000_000)
			big := makeNode("big-gpu", "10.0.0.11:50051", 16_000_000_000)
			Expect(registry.Register(small, true)).To(Succeed())
			Expect(registry.Register(big, true)).To(Succeed())

			// Request 8 GB — only big-gpu qualifies
			found, err := registry.FindNodeWithVRAM(8_000_000_000)
			Expect(err).ToNot(HaveOccurred())
			Expect(found.Name).To(Equal("big-gpu"))
		})

		It("returns error when no node has enough VRAM", func() {
			small := makeNode("tiny-gpu", "10.0.0.12:50051", 2_000_000_000)
			Expect(registry.Register(small, true)).To(Succeed())

			_, err := registry.FindNodeWithVRAM(32_000_000_000)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("FindIdleNode", func() {
		It("returns the node with no loaded models", func() {
			busy := makeNode("busy-node", "10.0.0.20:50051", 8_000_000_000)
			idle := makeNode("idle-node", "10.0.0.21:50051", 8_000_000_000)
			Expect(registry.Register(busy, true)).To(Succeed())
			Expect(registry.Register(idle, true)).To(Succeed())

			// Load a model on the busy node
			Expect(registry.SetNodeModel(busy.ID, "model-a", "loaded")).To(Succeed())

			found, err := registry.FindIdleNode()
			Expect(err).ToNot(HaveOccurred())
			Expect(found.Name).To(Equal("idle-node"))
		})

		It("returns error when all nodes have models loaded", func() {
			n := makeNode("all-busy", "10.0.0.22:50051", 8_000_000_000)
			Expect(registry.Register(n, true)).To(Succeed())
			Expect(registry.SetNodeModel(n.ID, "model-x", "loaded")).To(Succeed())

			_, err := registry.FindIdleNode()
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("FindLeastLoadedNode", func() {
		It("returns the node with fewer in-flight requests", func() {
			heavy := makeNode("heavy-node", "10.0.0.30:50051", 8_000_000_000)
			light := makeNode("light-node", "10.0.0.31:50051", 8_000_000_000)
			Expect(registry.Register(heavy, true)).To(Succeed())
			Expect(registry.Register(light, true)).To(Succeed())

			// Set up models with different in-flight counts
			Expect(registry.SetNodeModel(heavy.ID, "model-a", "loaded")).To(Succeed())
			Expect(registry.IncrementInFlight(heavy.ID, "model-a")).To(Succeed())
			Expect(registry.IncrementInFlight(heavy.ID, "model-a")).To(Succeed())
			Expect(registry.IncrementInFlight(heavy.ID, "model-a")).To(Succeed())

			Expect(registry.SetNodeModel(light.ID, "model-b", "loaded")).To(Succeed())
			Expect(registry.IncrementInFlight(light.ID, "model-b")).To(Succeed())

			found, err := registry.FindLeastLoadedNode()
			Expect(err).ToNot(HaveOccurred())
			Expect(found.Name).To(Equal("light-node"))
		})
	})

	Describe("FindAndLockNodeWithModel", func() {
		It("returns the correct node and increments in-flight", func() {
			node := makeNode("lock-node", "10.0.0.40:50051", 8_000_000_000)
			Expect(registry.Register(node, true)).To(Succeed())
			Expect(registry.SetNodeModel(node.ID, "my-model", "loaded", "10.0.0.40:50052")).To(Succeed())

			foundNode, foundNM, err := registry.FindAndLockNodeWithModel("my-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(foundNode.ID).To(Equal(node.ID))
			Expect(foundNM.ModelName).To(Equal("my-model"))

			// Verify in-flight was incremented
			nm, err := registry.GetNodeModel(node.ID, "my-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(nm.InFlight).To(Equal(1))
		})

		It("returns error when model is not loaded anywhere", func() {
			_, _, err := registry.FindAndLockNodeWithModel("nonexistent-model")
			Expect(err).To(HaveOccurred())
		})

		It("selects the node with fewer in-flight when multiple exist", func() {
			n1 := makeNode("lock-heavy", "10.0.0.41:50051", 8_000_000_000)
			n2 := makeNode("lock-light", "10.0.0.42:50051", 8_000_000_000)
			Expect(registry.Register(n1, true)).To(Succeed())
			Expect(registry.Register(n2, true)).To(Succeed())

			Expect(registry.SetNodeModel(n1.ID, "shared-model", "loaded")).To(Succeed())
			Expect(registry.SetNodeModel(n2.ID, "shared-model", "loaded")).To(Succeed())

			// Add in-flight to n1
			Expect(registry.IncrementInFlight(n1.ID, "shared-model")).To(Succeed())
			Expect(registry.IncrementInFlight(n1.ID, "shared-model")).To(Succeed())

			foundNode, _, err := registry.FindAndLockNodeWithModel("shared-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(foundNode.Name).To(Equal("lock-light"))
		})
	})

	Describe("DecrementInFlight", func() {
		It("does not go below zero", func() {
			node := makeNode("dec-node", "10.0.0.50:50051", 4_000_000_000)
			Expect(registry.Register(node, true)).To(Succeed())
			Expect(registry.SetNodeModel(node.ID, "dec-model", "loaded")).To(Succeed())

			// in_flight starts at 0 — decrement should be a no-op
			Expect(registry.DecrementInFlight(node.ID, "dec-model")).To(Succeed())

			nm, err := registry.GetNodeModel(node.ID, "dec-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(nm.InFlight).To(Equal(0))
		})

		It("decrements correctly from a positive value", func() {
			node := makeNode("dec-node-2", "10.0.0.51:50051", 4_000_000_000)
			Expect(registry.Register(node, true)).To(Succeed())
			Expect(registry.SetNodeModel(node.ID, "dec-model-2", "loaded")).To(Succeed())

			Expect(registry.IncrementInFlight(node.ID, "dec-model-2")).To(Succeed())
			Expect(registry.IncrementInFlight(node.ID, "dec-model-2")).To(Succeed())

			nm, err := registry.GetNodeModel(node.ID, "dec-model-2")
			Expect(err).ToNot(HaveOccurred())
			Expect(nm.InFlight).To(Equal(2))

			Expect(registry.DecrementInFlight(node.ID, "dec-model-2")).To(Succeed())

			nm, err = registry.GetNodeModel(node.ID, "dec-model-2")
			Expect(err).ToNot(HaveOccurred())
			Expect(nm.InFlight).To(Equal(1))
		})
	})
})
