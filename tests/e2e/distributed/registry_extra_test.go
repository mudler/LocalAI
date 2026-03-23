package distributed_test

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/services/nodes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("NodeRegistry extra methods", Label("Distributed"), func() {
	var (
		ctx         context.Context
		pgContainer *tcpostgres.PostgresContainer
		db          *gorm.DB
		registry    *nodes.NodeRegistry
	)

	BeforeEach(func() {
		ctx = context.Background()

		var err error
		pgContainer, err = tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("localai_registry_extra_test"),
			tcpostgres.WithUsername("test"),
			tcpostgres.WithPassword("test"),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(30*time.Second),
			),
		)
		Expect(err).ToNot(HaveOccurred())

		pgURL, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
		Expect(err).ToNot(HaveOccurred())

		db, err = gorm.Open(pgdriver.Open(pgURL), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).ToNot(HaveOccurred())

		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		if pgContainer != nil {
			pgContainer.Terminate(ctx)
		}
	})

	Context("ListAllLoadedModels", func() {
		It("returns empty when no models loaded", func() {
			models, err := registry.ListAllLoadedModels()
			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(BeEmpty())
		})

		It("returns models from healthy nodes with state loaded", func() {
			node := &nodes.BackendNode{
				Name: "healthy-node", Address: "h:5000",
			}
			Expect(registry.Register(node, true)).To(Succeed())
			Expect(registry.SetNodeModel(node.ID, "model-a", "loaded")).To(Succeed())

			models, err := registry.ListAllLoadedModels()
			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(HaveLen(1))
			Expect(models[0].ModelName).To(Equal("model-a"))
		})

		It("excludes models on unhealthy nodes", func() {
			node := &nodes.BackendNode{
				Name: "sick-node", Address: "s:5000",
			}
			Expect(registry.Register(node, true)).To(Succeed())
			Expect(registry.SetNodeModel(node.ID, "model-on-sick", "loaded")).To(Succeed())
			Expect(registry.MarkUnhealthy(node.ID)).To(Succeed())

			models, err := registry.ListAllLoadedModels()
			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(BeEmpty())
		})

		It("excludes models with state != loaded", func() {
			node := &nodes.BackendNode{
				Name: "state-node", Address: "st:5000",
			}
			Expect(registry.Register(node, true)).To(Succeed())
			Expect(registry.SetNodeModel(node.ID, "loading-model", "loading")).To(Succeed())
			Expect(registry.SetNodeModel(node.ID, "idle-model", "idle")).To(Succeed())

			models, err := registry.ListAllLoadedModels()
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
			Expect(registry.Register(node1, true)).To(Succeed())
			Expect(registry.Register(node2, true)).To(Succeed())
			Expect(registry.SetNodeModel(node1.ID, "model-x", "loaded")).To(Succeed())
			Expect(registry.SetNodeModel(node2.ID, "model-y", "loaded")).To(Succeed())

			models, err := registry.ListAllLoadedModels()
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
			Expect(registry.Register(node, true)).To(Succeed())
			Expect(registry.SetNodeModel(node.ID, "findable-model", "loaded")).To(Succeed())

			found, ok := registry.FindNodeForModel("findable-model")
			Expect(ok).To(BeTrue())
			Expect(found).ToNot(BeNil())
			Expect(found.ID).To(Equal(node.ID))
		})

		It("returns (nil, false) when model not found", func() {
			found, ok := registry.FindNodeForModel("no-such-model")
			Expect(ok).To(BeFalse())
			Expect(found).To(BeNil())
		})

		It("returns (nil, false) when model only on unhealthy node", func() {
			node := &nodes.BackendNode{
				Name: "unhealthy-find", Address: "uf:5000",
			}
			Expect(registry.Register(node, true)).To(Succeed())
			Expect(registry.SetNodeModel(node.ID, "unhealthy-model", "loaded")).To(Succeed())
			Expect(registry.MarkUnhealthy(node.ID)).To(Succeed())

			found, ok := registry.FindNodeForModel("unhealthy-model")
			Expect(ok).To(BeFalse())
			Expect(found).To(BeNil())
		})
	})

	Context("Register clears stale models", func() {
		It("clears node_models when a node re-registers", func() {
			node := &nodes.BackendNode{
				Name: "stale-clear-node", Address: "sc:5000",
			}
			Expect(registry.Register(node, true)).To(Succeed())
			Expect(registry.SetNodeModel(node.ID, "stale-model-1", "loaded")).To(Succeed())
			Expect(registry.SetNodeModel(node.ID, "stale-model-2", "loaded")).To(Succeed())

			// Verify models exist
			models, err := registry.GetNodeModels(node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(HaveLen(2))

			// Re-register the same node (simulates restart)
			reNode := &nodes.BackendNode{
				Name: "stale-clear-node", Address: "sc:5001",
			}
			Expect(registry.Register(reNode, true)).To(Succeed())

			// Stale models should be cleared
			models, err = registry.GetNodeModels(node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(BeEmpty())
		})
	})

	Context("FindIdleNode", func() {
		It("returns a healthy node with no loaded models", func() {
			node := &nodes.BackendNode{
				Name: "idle-node", Address: "idle:5000",
			}
			Expect(registry.Register(node, true)).To(Succeed())

			found, err := registry.FindIdleNode()
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
			Expect(registry.Register(busy, true)).To(Succeed())
			Expect(registry.Register(idle, true)).To(Succeed())
			Expect(registry.SetNodeModel(busy.ID, "some-model", "loaded")).To(Succeed())

			found, err := registry.FindIdleNode()
			Expect(err).ToNot(HaveOccurred())
			Expect(found).ToNot(BeNil())
			Expect(found.ID).To(Equal(idle.ID))
		})

		It("returns error when no idle nodes exist", func() {
			node := &nodes.BackendNode{
				Name: "loaded-node", Address: "loaded:5000",
			}
			Expect(registry.Register(node, true)).To(Succeed())
			Expect(registry.SetNodeModel(node.ID, "model-x", "loaded")).To(Succeed())

			_, err := registry.FindIdleNode()
			Expect(err).To(HaveOccurred())
		})
	})
})
