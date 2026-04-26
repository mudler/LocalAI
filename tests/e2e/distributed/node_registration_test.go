package distributed_test

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/services/nodes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("Phase 1: Node Registration", Label("Distributed"), func() {
	var (
		infra    *TestInfra
		db       *gorm.DB
		registry *nodes.NodeRegistry
	)

	BeforeEach(func() {
		infra = SetupInfra("localai_nodes_test")

		var err error
		db, err = gorm.Open(pgdriver.Open(infra.PGURL), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).ToNot(HaveOccurred())

		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
	})

	Context("Node Registration", func() {
		It("should register a node", func() {
			node := &nodes.BackendNode{
				Name:    "test-node",
				Address: "localhost:50051",
			}
			err := registry.Register(context.Background(), node, true)
			Expect(err).ToNot(HaveOccurred())
			Expect(node.ID).ToNot(BeEmpty())
			Expect(node.Status).To(Equal("healthy"))
		})

		It("should list registered nodes", func() {
			err := registry.Register(context.Background(), &nodes.BackendNode{
				Name: "node-1", Address: "host1:50051",
			}, true)
			Expect(err).ToNot(HaveOccurred())

			err = registry.Register(context.Background(), &nodes.BackendNode{
				Name: "node-2", Address: "host2:50051",
			}, true)
			Expect(err).ToNot(HaveOccurred())

			list, err := registry.List(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(list).To(HaveLen(2))
		})

		It("should deregister a node", func() {
			node := &nodes.BackendNode{
				Name: "ephemeral", Address: "host3:50051",
			}
			err := registry.Register(context.Background(), node, true)
			Expect(err).ToNot(HaveOccurred())

			err = registry.Deregister(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())

			list, err := registry.List(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(list).To(BeEmpty())
		})

		It("should receive heartbeats and update last_heartbeat", func() {
			node := &nodes.BackendNode{
				Name: "heartbeat-node", Address: "host4:50051",
			}
			err := registry.Register(context.Background(), node, true)
			Expect(err).ToNot(HaveOccurred())

			// Wait a bit then heartbeat
			time.Sleep(100 * time.Millisecond)
			err = registry.Heartbeat(context.Background(), node.ID, nil)
			Expect(err).ToNot(HaveOccurred())

			updated, err := registry.Get(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(updated.LastHeartbeat).To(BeTemporally(">", node.LastHeartbeat))
		})

		It("should mark node unhealthy after missed heartbeats", func() {
			node := &nodes.BackendNode{
				Name: "stale-node", Address: "host5:50051",
			}
			err := registry.Register(context.Background(), node, true)
			Expect(err).ToNot(HaveOccurred())

			err = registry.MarkUnhealthy(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())

			updated, err := registry.Get(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(updated.Status).To(Equal("unhealthy"))
		})

		It("should find stale nodes", func() {
			node := &nodes.BackendNode{
				Name: "old-node", Address: "host6:50051",
			}
			err := registry.Register(context.Background(), node, true)
			Expect(err).ToNot(HaveOccurred())

			// Set heartbeat to the past
			db.Model(&nodes.BackendNode{}).Where("id = ?", node.ID).
				Update("last_heartbeat", time.Now().Add(-5*time.Minute))

			stale, err := registry.FindStaleNodes(context.Background(), 1*time.Minute)
			Expect(err).ToNot(HaveOccurred())
			Expect(stale).To(HaveLen(1))
			Expect(stale[0].Name).To(Equal("old-node"))
		})

		It("should update existing node on re-registration", func() {
			node := &nodes.BackendNode{
				Name: "reregister-node", Address: "h1:50051",
			}
			err := registry.Register(context.Background(), node, true)
			Expect(err).ToNot(HaveOccurred())
			firstID := node.ID

			// Re-register with updated address
			node2 := &nodes.BackendNode{
				Name: "reregister-node", Address: "h1:50052",
			}
			err = registry.Register(context.Background(), node2, true)
			Expect(err).ToNot(HaveOccurred())

			// Should be same node (upsert by name)
			list, err := registry.List(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(list).To(HaveLen(1))
			Expect(list[0].ID).To(Equal(firstID))
			Expect(list[0].Address).To(Equal("h1:50052"))
		})
	})

	Context("Node Models", func() {
		var nodeID string

		BeforeEach(func() {
			node := &nodes.BackendNode{
				Name: "model-node", Address: "mh:50051",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			nodeID = node.ID
		})

		It("should track models loaded on a node", func() {
			err := registry.SetNodeModel(context.Background(), nodeID, "llama3", "loaded", "", 0)
			Expect(err).ToNot(HaveOccurred())

			models, err := registry.GetNodeModels(context.Background(), nodeID)
			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(HaveLen(1))
			Expect(models[0].ModelName).To(Equal("llama3"))
			Expect(models[0].State).To(Equal("loaded"))
		})

		It("should find nodes with a specific model", func() {
			registry.SetNodeModel(context.Background(), nodeID, "llama3", "loaded", "", 0)

			nodesWithModel, err := registry.FindNodesWithModel(context.Background(), "llama3")
			Expect(err).ToNot(HaveOccurred())
			Expect(nodesWithModel).To(HaveLen(1))
			Expect(nodesWithModel[0].ID).To(Equal(nodeID))
		})

		It("should increment and decrement in-flight counters", func() {
			registry.SetNodeModel(context.Background(), nodeID, "llama3", "loaded", "", 0)

			err := registry.IncrementInFlight(context.Background(), nodeID, "llama3")
			Expect(err).ToNot(HaveOccurred())
			err = registry.IncrementInFlight(context.Background(), nodeID, "llama3")
			Expect(err).ToNot(HaveOccurred())

			models, _ := registry.GetNodeModels(context.Background(), nodeID)
			Expect(models[0].InFlight).To(Equal(2))

			registry.DecrementInFlight(context.Background(), nodeID, "llama3")
			models, _ = registry.GetNodeModels(context.Background(), nodeID)
			Expect(models[0].InFlight).To(Equal(1))
		})

		It("should remove model association from node", func() {
			registry.SetNodeModel(context.Background(), nodeID, "llama3", "loaded", "", 0)
			err := registry.RemoveNodeModel(context.Background(), nodeID, "llama3")
			Expect(err).ToNot(HaveOccurred())

			models, _ := registry.GetNodeModels(context.Background(), nodeID)
			Expect(models).To(BeEmpty())
		})

		It("should find LRU model on a node", func() {
			// Load two models
			registry.SetNodeModel(context.Background(), nodeID, "old-model", "loaded", "", 0)
			time.Sleep(10 * time.Millisecond)
			registry.SetNodeModel(context.Background(), nodeID, "new-model", "loaded", "", 0)

			// Update last_used to make old-model older
			db.Model(&nodes.NodeModel{}).Where("node_id = ? AND model_name = ?", nodeID, "old-model").
				Update("last_used", time.Now().Add(-10*time.Minute))

			lru, err := registry.FindLRUModel(context.Background(), nodeID)
			Expect(err).ToNot(HaveOccurred())
			Expect(lru.ModelName).To(Equal("old-model"))
		})

		It("should clean up models when deregistering node", func() {
			registry.SetNodeModel(context.Background(), nodeID, "llama3", "loaded", "", 0)
			registry.SetNodeModel(context.Background(), nodeID, "whisper", "loaded", "", 0)

			err := registry.Deregister(context.Background(), nodeID)
			Expect(err).ToNot(HaveOccurred())

			// Models should be gone too
			models, _ := registry.GetNodeModels(context.Background(), nodeID)
			Expect(models).To(BeEmpty())
		})
	})
})
