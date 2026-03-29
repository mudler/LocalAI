package distributed_test

import (
	"context"

	"github.com/mudler/LocalAI/core/services/nodes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("NodeRegistry extra methods", Label("Distributed"), func() {
	var (
		infra    *TestInfra
		db       *gorm.DB
		registry *nodes.NodeRegistry
	)

	BeforeEach(func() {
		infra = SetupInfra("localai_registry_extra_test")

		var err error
		db, err = gorm.Open(pgdriver.Open(infra.PGURL), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).ToNot(HaveOccurred())

		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
	})

	Context("ListAllLoadedModels", func() {
		It("returns empty when no models loaded", func() {
			models, err := registry.ListAllLoadedModels(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(BeEmpty())
		})

		It("returns models from healthy nodes with state loaded", func() {
			node := &nodes.BackendNode{
				Name: "healthy-node", Address: "h:5000",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "model-a", "loaded")).To(Succeed())

			models, err := registry.ListAllLoadedModels(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(HaveLen(1))
			Expect(models[0].ModelName).To(Equal("model-a"))
		})

		It("excludes models on unhealthy nodes", func() {
			node := &nodes.BackendNode{
				Name: "sick-node", Address: "s:5000",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "model-on-sick", "loaded")).To(Succeed())
			Expect(registry.MarkUnhealthy(context.Background(), node.ID)).To(Succeed())

			models, err := registry.ListAllLoadedModels(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(BeEmpty())
		})

		It("excludes models with state != loaded", func() {
			node := &nodes.BackendNode{
				Name: "state-node", Address: "st:5000",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "loading-model", "loading")).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "idle-model", "idle")).To(Succeed())

			models, err := registry.ListAllLoadedModels(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(BeEmpty())
		})

		It("returns models across multiple nodes", func() {
			node1 := &nodes.BackendNode{
				Name: "multi-1", Address: "m1:5000",
			}
			node2 := &nodes.BackendNode{
				Name: "multi-2", Address: "m2:5000",
			}
			Expect(registry.Register(context.Background(), node1, true)).To(Succeed())
			Expect(registry.Register(context.Background(), node2, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node1.ID, "model-x", "loaded")).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node2.ID, "model-y", "loaded")).To(Succeed())

			models, err := registry.ListAllLoadedModels(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(HaveLen(2))

			names := map[string]bool{}
			for _, m := range models {
				names[m.ModelName] = true
			}
			Expect(names).To(HaveKey("model-x"))
			Expect(names).To(HaveKey("model-y"))
		})
	})

	Context("FindNodeForModel", func() {
		It("returns (node, true) when model is loaded on healthy node", func() {
			node := &nodes.BackendNode{
				Name: "find-node", Address: "f:5000",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "findable-model", "loaded")).To(Succeed())

			found, ok := registry.FindNodeForModel(context.Background(), "findable-model")
			Expect(ok).To(BeTrue())
			Expect(found).ToNot(BeNil())
			Expect(found.ID).To(Equal(node.ID))
		})

		It("returns (nil, false) when model not found", func() {
			found, ok := registry.FindNodeForModel(context.Background(), "no-such-model")
			Expect(ok).To(BeFalse())
			Expect(found).To(BeNil())
		})

		It("returns (nil, false) when model only on unhealthy node", func() {
			node := &nodes.BackendNode{
				Name: "unhealthy-find", Address: "uf:5000",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "unhealthy-model", "loaded")).To(Succeed())
			Expect(registry.MarkUnhealthy(context.Background(), node.ID)).To(Succeed())

			found, ok := registry.FindNodeForModel(context.Background(), "unhealthy-model")
			Expect(ok).To(BeFalse())
			Expect(found).To(BeNil())
		})
	})

	Context("Register clears stale models", func() {
		It("clears node_models when a node re-registers", func() {
			node := &nodes.BackendNode{
				Name: "stale-clear-node", Address: "sc:5000",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "stale-model-1", "loaded")).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "stale-model-2", "loaded")).To(Succeed())

			// Verify models exist
			models, err := registry.GetNodeModels(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(HaveLen(2))

			// Re-register the same node (simulates restart)
			reNode := &nodes.BackendNode{
				Name: "stale-clear-node", Address: "sc:5001",
			}
			Expect(registry.Register(context.Background(), reNode, true)).To(Succeed())

			// Stale models should be cleared
			models, err = registry.GetNodeModels(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(BeEmpty())
		})
	})

	Context("FindIdleNode", func() {
		It("returns a healthy node with no loaded models", func() {
			node := &nodes.BackendNode{
				Name: "idle-node", Address: "idle:5000",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			found, err := registry.FindIdleNode(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(found).ToNot(BeNil())
			Expect(found.ID).To(Equal(node.ID))
		})

		It("skips nodes that have loaded models", func() {
			busy := &nodes.BackendNode{
				Name: "busy-node", Address: "busy:5000",
			}
			idle := &nodes.BackendNode{
				Name: "idle-node-2", Address: "idle2:5000",
			}
			Expect(registry.Register(context.Background(), busy, true)).To(Succeed())
			Expect(registry.Register(context.Background(), idle, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), busy.ID, "some-model", "loaded")).To(Succeed())

			found, err := registry.FindIdleNode(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(found).ToNot(BeNil())
			Expect(found.ID).To(Equal(idle.ID))
		})

		It("returns error when no idle nodes exist", func() {
			node := &nodes.BackendNode{
				Name: "loaded-node", Address: "loaded:5000",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "model-x", "loaded")).To(Succeed())

			_, err := registry.FindIdleNode(context.Background())
			Expect(err).To(HaveOccurred())
		})
	})
})
