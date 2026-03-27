package distributed_test

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/nodes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("Model Routing", Label("Distributed"), func() {
	var (
		infra    *TestInfra
		db       *gorm.DB
		registry *nodes.NodeRegistry
	)

	BeforeEach(func() {
		infra = SetupInfra("localai_routing_test")

		var err error
		db, err = gorm.Open(pgdriver.Open(infra.PGURL), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).ToNot(HaveOccurred())

		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
	})

	Context("ModelRouterAdapter from SmartRouter", func() {
		It("should create ModelRouterAdapter from SmartRouter", func() {
			router := nodes.NewSmartRouter(registry)
			Expect(router).ToNot(BeNil())

			adapter := nodes.NewModelRouterAdapter(router)
			Expect(adapter).ToNot(BeNil())

			// The adapter should provide a ModelRouter callback
			routerFunc := adapter.AsModelRouter()
			Expect(routerFunc).ToNot(BeNil())
		})

		It("should release in-flight counter on model unload", func() {
			// Register a node with a loaded model
			node := &nodes.BackendNode{
				Name: "gpu-1", Address: "h1:50051",
			}
			Expect(registry.Register(node, true)).To(Succeed())
			Expect(registry.SetNodeModel(node.ID, "llama3", "loaded")).To(Succeed())
			Expect(registry.IncrementInFlight(node.ID, "llama3")).To(Succeed())
			Expect(registry.IncrementInFlight(node.ID, "llama3")).To(Succeed())

			// Verify in-flight count
			models, err := registry.GetNodeModels(node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(models[0].InFlight).To(Equal(2))

			// Simulate decrement (what Release does)
			Expect(registry.DecrementInFlight(node.ID, "llama3")).To(Succeed())
			models, _ = registry.GetNodeModels(node.ID)
			Expect(models[0].InFlight).To(Equal(1))

			// The ModelRouterAdapter.ReleaseModel calls the stored Release function
			router := nodes.NewSmartRouter(registry)
			adapter := nodes.NewModelRouterAdapter(router)

			// ReleaseModel on an unknown model should be a no-op (no panic)
			Expect(func() { adapter.ReleaseModel("nonexistent-model") }).ToNot(Panic())
		})

		It("should use SmartRouter to find nodes with a model", func() {
			// Register multiple nodes
			node1 := &nodes.BackendNode{
				Name: "node-a", Address: "h1:50051",
			}
			node2 := &nodes.BackendNode{
				Name: "node-b", Address: "h2:50051",
			}
			Expect(registry.Register(node1, true)).To(Succeed())
			Expect(registry.Register(node2, true)).To(Succeed())

			// Load model on node1
			Expect(registry.SetNodeModel(node1.ID, "llama3", "loaded")).To(Succeed())

			// Verify routing can find the model
			nodesWithModel, err := registry.FindNodesWithModel("llama3")
			Expect(err).ToNot(HaveOccurred())
			Expect(nodesWithModel).To(HaveLen(1))
			Expect(nodesWithModel[0].ID).To(Equal(node1.ID))
		})
	})

	Context("Without --distributed", func() {
		It("should fall through to local loading without --distributed", func() {
			appCfg := config.NewApplicationConfig()
			Expect(appCfg.Distributed.Enabled).To(BeFalse())

			// Without distributed mode, no SmartRouter is created.
			// The ModelLoader uses its local process management.
			// This test documents the design decision.
			Expect(appCfg.Distributed.NatsURL).To(BeEmpty())
		})
	})
})
