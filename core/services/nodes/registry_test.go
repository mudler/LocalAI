package nodes

import (
	"context"
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
			Expect(registry.Register(context.Background(), node, false)).To(Succeed())
			Expect(node.Status).To(Equal(StatusPending))

			fetched, err := registry.GetByName(context.Background(), "worker-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusPending))
		})

		It("sets StatusHealthy when autoApprove is true", func() {
			node := makeNode("worker-2", "10.0.0.2:50051", 4_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(node.Status).To(Equal(StatusHealthy))
		})
	})

	Describe("Re-registration", func() {
		It("keeps a pending node pending on re-register with autoApprove=false", func() {
			node := makeNode("re-pending", "10.0.0.3:50051", 4_000_000_000)
			Expect(registry.Register(context.Background(), node, false)).To(Succeed())
			Expect(node.Status).To(Equal(StatusPending))

			// Re-register same name, still no auto-approve
			node2 := makeNode("re-pending", "10.0.0.3:50052", 4_000_000_000)
			Expect(registry.Register(context.Background(), node2, false)).To(Succeed())
			Expect(node2.Status).To(Equal(StatusPending))

			// ID is preserved from original registration
			Expect(node2.ID).To(Equal(node.ID))
		})

		It("restores a previously approved node to healthy on re-register with autoApprove=false", func() {
			node := makeNode("re-approved", "10.0.0.4:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(node.Status).To(Equal(StatusHealthy))

			// Simulate the node becoming unhealthy
			Expect(registry.MarkUnhealthy(context.Background(), node.ID)).To(Succeed())
			fetched, err := registry.GetByName(context.Background(), "re-approved")
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusUnhealthy))

			// Re-register with autoApprove=false — should restore to healthy
			// because the node was previously approved (status != pending)
			node2 := makeNode("re-approved", "10.0.0.4:50052", 8_000_000_000)
			Expect(registry.Register(context.Background(), node2, false)).To(Succeed())
			Expect(node2.Status).To(Equal(StatusHealthy))
		})
	})

	Describe("ApproveNode", func() {
		It("transitions a pending node to healthy", func() {
			node := makeNode("approve-me", "10.0.0.5:50051", 4_000_000_000)
			Expect(registry.Register(context.Background(), node, false)).To(Succeed())
			Expect(node.Status).To(Equal(StatusPending))

			Expect(registry.ApproveNode(context.Background(), node.ID)).To(Succeed())

			fetched, err := registry.Get(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusHealthy))
		})

		It("returns error for non-existent node ID", func() {
			err := registry.ApproveNode(context.Background(), "non-existent-id")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found or not in pending status"))
		})

		It("returns error for an already-healthy node", func() {
			node := makeNode("already-healthy", "10.0.0.6:50051", 4_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			err := registry.ApproveNode(context.Background(), node.ID)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found or not in pending status"))
		})
	})

	Describe("MarkOffline", func() {
		It("sets status to offline and clears model records", func() {
			node := makeNode("offline-test", "10.0.0.7:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			// Load a model on the node
			Expect(registry.SetNodeModel(context.Background(), node.ID, "llama-7b", "loaded", "10.0.0.7:50052", 0)).To(Succeed())
			models, err := registry.GetNodeModels(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(HaveLen(1))

			// Mark offline
			Expect(registry.MarkOffline(context.Background(), node.ID)).To(Succeed())

			fetched, err := registry.Get(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusOffline))

			// Model records should be cleared
			models, err = registry.GetNodeModels(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(BeEmpty())
		})

		It("returns error for non-existent node", func() {
			err := registry.MarkOffline(context.Background(), "does-not-exist")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
		})
	})

	Describe("SetNodeModel ID stability", func() {
		It("preserves the ID when called twice for the same node+model", func() {
			node := makeNode("stable-id-node", "10.0.0.99:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			Expect(registry.SetNodeModel(context.Background(), node.ID, "my-model", "loaded", "10.0.0.99:50052", 0)).To(Succeed())
			nm1, err := registry.GetNodeModel(context.Background(), node.ID, "my-model")
			Expect(err).ToNot(HaveOccurred())

			// Call again with different state/address
			Expect(registry.SetNodeModel(context.Background(), node.ID, "my-model", "loaded", "10.0.0.99:50053", 0)).To(Succeed())
			nm2, err := registry.GetNodeModel(context.Background(), node.ID, "my-model")
			Expect(err).ToNot(HaveOccurred())

			Expect(nm2.ID).To(Equal(nm1.ID), "ID should remain stable across SetNodeModel calls")
			Expect(nm2.Address).To(Equal("10.0.0.99:50053"), "Address should be updated")
		})
	})

	Describe("FindNodeWithVRAM", func() {
		It("selects the node with sufficient VRAM", func() {
			small := makeNode("small-gpu", "10.0.0.10:50051", 4_000_000_000)
			big := makeNode("big-gpu", "10.0.0.11:50051", 16_000_000_000)
			Expect(registry.Register(context.Background(), small, true)).To(Succeed())
			Expect(registry.Register(context.Background(), big, true)).To(Succeed())

			// Request 8 GB — only big-gpu qualifies
			found, err := registry.FindNodeWithVRAM(context.Background(), 8_000_000_000)
			Expect(err).ToNot(HaveOccurred())
			Expect(found.Name).To(Equal("big-gpu"))
		})

		It("returns error when no node has enough VRAM", func() {
			small := makeNode("tiny-gpu", "10.0.0.12:50051", 2_000_000_000)
			Expect(registry.Register(context.Background(), small, true)).To(Succeed())

			_, err := registry.FindNodeWithVRAM(context.Background(), 32_000_000_000)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("FindIdleNode", func() {
		It("returns the node with no loaded models", func() {
			busy := makeNode("busy-node", "10.0.0.20:50051", 8_000_000_000)
			idle := makeNode("idle-node", "10.0.0.21:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), busy, true)).To(Succeed())
			Expect(registry.Register(context.Background(), idle, true)).To(Succeed())

			// Load a model on the busy node
			Expect(registry.SetNodeModel(context.Background(), busy.ID, "model-a", "loaded", "", 0)).To(Succeed())

			found, err := registry.FindIdleNode(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(found.Name).To(Equal("idle-node"))
		})

		It("returns error when all nodes have models loaded", func() {
			n := makeNode("all-busy", "10.0.0.22:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), n, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), n.ID, "model-x", "loaded", "", 0)).To(Succeed())

			_, err := registry.FindIdleNode(context.Background())
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("FindLeastLoadedNode", func() {
		It("returns the node with fewer in-flight requests", func() {
			heavy := makeNode("heavy-node", "10.0.0.30:50051", 8_000_000_000)
			light := makeNode("light-node", "10.0.0.31:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), heavy, true)).To(Succeed())
			Expect(registry.Register(context.Background(), light, true)).To(Succeed())

			// Set up models with different in-flight counts
			Expect(registry.SetNodeModel(context.Background(), heavy.ID, "model-a", "loaded", "", 0)).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), heavy.ID, "model-a")).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), heavy.ID, "model-a")).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), heavy.ID, "model-a")).To(Succeed())

			Expect(registry.SetNodeModel(context.Background(), light.ID, "model-b", "loaded", "", 0)).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), light.ID, "model-b")).To(Succeed())

			found, err := registry.FindLeastLoadedNode(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(found.Name).To(Equal("light-node"))
		})
	})

	Describe("FindAndLockNodeWithModel", func() {
		It("returns the correct node and increments in-flight", func() {
			node := makeNode("lock-node", "10.0.0.40:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "my-model", "loaded", "10.0.0.40:50052", 0)).To(Succeed())

			foundNode, foundNM, err := registry.FindAndLockNodeWithModel(context.Background(), "my-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(foundNode.ID).To(Equal(node.ID))
			Expect(foundNM.ModelName).To(Equal("my-model"))

			// Verify in-flight was incremented
			nm, err := registry.GetNodeModel(context.Background(), node.ID, "my-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(nm.InFlight).To(Equal(1))
		})

		It("returns error when model is not loaded anywhere", func() {
			_, _, err := registry.FindAndLockNodeWithModel(context.Background(), "nonexistent-model")
			Expect(err).To(HaveOccurred())
		})

		It("selects the node with fewer in-flight when multiple exist", func() {
			n1 := makeNode("lock-heavy", "10.0.0.41:50051", 8_000_000_000)
			n2 := makeNode("lock-light", "10.0.0.42:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), n1, true)).To(Succeed())
			Expect(registry.Register(context.Background(), n2, true)).To(Succeed())

			Expect(registry.SetNodeModel(context.Background(), n1.ID, "shared-model", "loaded", "", 0)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), n2.ID, "shared-model", "loaded", "", 0)).To(Succeed())

			// Add in-flight to n1
			Expect(registry.IncrementInFlight(context.Background(), n1.ID, "shared-model")).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), n1.ID, "shared-model")).To(Succeed())

			foundNode, _, err := registry.FindAndLockNodeWithModel(context.Background(), "shared-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(foundNode.Name).To(Equal("lock-light"))
		})
	})

	Describe("MarkHealthy and MarkUnhealthy round-trip", func() {
		It("transitions healthy -> unhealthy -> healthy", func() {
			node := makeNode("roundtrip-node", "10.0.0.60:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(node.Status).To(Equal(StatusHealthy))

			// Mark unhealthy
			Expect(registry.MarkUnhealthy(context.Background(), node.ID)).To(Succeed())
			fetched, err := registry.Get(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusUnhealthy))

			// Mark healthy again
			Expect(registry.MarkHealthy(context.Background(), node.ID)).To(Succeed())
			fetched, err = registry.Get(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.Status).To(Equal(StatusHealthy))
		})

		It("returns error for non-existent node", func() {
			err := registry.MarkHealthy(context.Background(), "does-not-exist")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
		})
	})

	Describe("NodeLabel CRUD", func() {
		It("sets and retrieves labels for a node", func() {
			node := makeNode("label-node", "10.0.0.70:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			Expect(registry.SetNodeLabel(context.Background(), node.ID, "env", "prod")).To(Succeed())
			Expect(registry.SetNodeLabel(context.Background(), node.ID, "region", "us-east")).To(Succeed())

			labels, err := registry.GetNodeLabels(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(labels).To(HaveLen(2))

			labelMap := make(map[string]string)
			for _, l := range labels {
				labelMap[l.Key] = l.Value
			}
			Expect(labelMap["env"]).To(Equal("prod"))
			Expect(labelMap["region"]).To(Equal("us-east"))
		})

		It("overwrites existing label with same key", func() {
			node := makeNode("label-overwrite", "10.0.0.71:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			Expect(registry.SetNodeLabel(context.Background(), node.ID, "env", "dev")).To(Succeed())
			Expect(registry.SetNodeLabel(context.Background(), node.ID, "env", "prod")).To(Succeed())

			labels, err := registry.GetNodeLabels(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(labels).To(HaveLen(1))
			Expect(labels[0].Value).To(Equal("prod"))
		})

		It("removes a single label by key", func() {
			node := makeNode("label-remove", "10.0.0.72:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			Expect(registry.SetNodeLabel(context.Background(), node.ID, "env", "prod")).To(Succeed())
			Expect(registry.SetNodeLabel(context.Background(), node.ID, "region", "us-east")).To(Succeed())

			Expect(registry.RemoveNodeLabel(context.Background(), node.ID, "env")).To(Succeed())

			labels, err := registry.GetNodeLabels(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(labels).To(HaveLen(1))
			Expect(labels[0].Key).To(Equal("region"))
		})

		It("SetNodeLabels replaces all labels", func() {
			node := makeNode("label-replace", "10.0.0.73:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			Expect(registry.SetNodeLabel(context.Background(), node.ID, "old-key", "old-val")).To(Succeed())

			newLabels := map[string]string{"new-a": "val-a", "new-b": "val-b"}
			Expect(registry.SetNodeLabels(context.Background(), node.ID, newLabels)).To(Succeed())

			labels, err := registry.GetNodeLabels(context.Background(), node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(labels).To(HaveLen(2))

			labelMap := make(map[string]string)
			for _, l := range labels {
				labelMap[l.Key] = l.Value
			}
			Expect(labelMap).To(Equal(newLabels))
		})
	})

	Describe("FindNodesBySelector", func() {
		It("returns nodes matching all labels in selector", func() {
			n1 := makeNode("sel-match", "10.0.0.80:50051", 8_000_000_000)
			n2 := makeNode("sel-nomatch", "10.0.0.81:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), n1, true)).To(Succeed())
			Expect(registry.Register(context.Background(), n2, true)).To(Succeed())

			Expect(registry.SetNodeLabel(context.Background(), n1.ID, "env", "prod")).To(Succeed())
			Expect(registry.SetNodeLabel(context.Background(), n1.ID, "region", "us-east")).To(Succeed())
			Expect(registry.SetNodeLabel(context.Background(), n2.ID, "env", "dev")).To(Succeed())

			nodes, err := registry.FindNodesBySelector(context.Background(), map[string]string{"env": "prod", "region": "us-east"})
			Expect(err).ToNot(HaveOccurred())
			Expect(nodes).To(HaveLen(1))
			Expect(nodes[0].Name).To(Equal("sel-match"))
		})

		It("returns empty when no nodes match", func() {
			n := makeNode("sel-empty", "10.0.0.82:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), n, true)).To(Succeed())
			Expect(registry.SetNodeLabel(context.Background(), n.ID, "env", "dev")).To(Succeed())

			nodes, err := registry.FindNodesBySelector(context.Background(), map[string]string{"env": "prod"})
			Expect(err).ToNot(HaveOccurred())
			Expect(nodes).To(BeEmpty())
		})

		It("ignores unhealthy nodes", func() {
			n := makeNode("sel-unhealthy", "10.0.0.83:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), n, true)).To(Succeed())
			Expect(registry.SetNodeLabel(context.Background(), n.ID, "env", "prod")).To(Succeed())
			Expect(registry.MarkUnhealthy(context.Background(), n.ID)).To(Succeed())

			nodes, err := registry.FindNodesBySelector(context.Background(), map[string]string{"env": "prod"})
			Expect(err).ToNot(HaveOccurred())
			Expect(nodes).To(BeEmpty())
		})

		It("matches nodes with more labels than selector requires", func() {
			n := makeNode("sel-superset", "10.0.0.84:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), n, true)).To(Succeed())
			Expect(registry.SetNodeLabel(context.Background(), n.ID, "env", "prod")).To(Succeed())
			Expect(registry.SetNodeLabel(context.Background(), n.ID, "region", "us-east")).To(Succeed())
			Expect(registry.SetNodeLabel(context.Background(), n.ID, "tier", "gpu")).To(Succeed())

			nodes, err := registry.FindNodesBySelector(context.Background(), map[string]string{"env": "prod"})
			Expect(err).ToNot(HaveOccurred())
			Expect(nodes).To(HaveLen(1))
			Expect(nodes[0].Name).To(Equal("sel-superset"))
		})

		It("returns all healthy nodes for empty selector", func() {
			n1 := makeNode("sel-all-1", "10.0.0.85:50051", 8_000_000_000)
			n2 := makeNode("sel-all-2", "10.0.0.86:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), n1, true)).To(Succeed())
			Expect(registry.Register(context.Background(), n2, true)).To(Succeed())

			nodes, err := registry.FindNodesBySelector(context.Background(), map[string]string{})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(nodes)).To(BeNumerically(">=", 2))
		})
	})

	Describe("ModelSchedulingConfig CRUD", func() {
		It("creates and retrieves a scheduling config", func() {
			config := &ModelSchedulingConfig{
				ModelName:    "llama-7b",
				NodeSelector: `{"gpu.vendor":"nvidia"}`,
				MinReplicas:  1,
				MaxReplicas:  3,
			}
			Expect(registry.SetModelScheduling(context.Background(), config)).To(Succeed())
			Expect(config.ID).ToNot(BeEmpty())

			fetched, err := registry.GetModelScheduling(context.Background(), "llama-7b")
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched).ToNot(BeNil())
			Expect(fetched.ModelName).To(Equal("llama-7b"))
			Expect(fetched.NodeSelector).To(Equal(`{"gpu.vendor":"nvidia"}`))
			Expect(fetched.MinReplicas).To(Equal(1))
			Expect(fetched.MaxReplicas).To(Equal(3))
		})

		It("updates existing config via SetModelScheduling", func() {
			config := &ModelSchedulingConfig{
				ModelName:   "update-model",
				MinReplicas: 1,
				MaxReplicas: 2,
			}
			Expect(registry.SetModelScheduling(context.Background(), config)).To(Succeed())

			config2 := &ModelSchedulingConfig{
				ModelName:   "update-model",
				MinReplicas: 2,
				MaxReplicas: 5,
			}
			Expect(registry.SetModelScheduling(context.Background(), config2)).To(Succeed())

			fetched, err := registry.GetModelScheduling(context.Background(), "update-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched.MinReplicas).To(Equal(2))
			Expect(fetched.MaxReplicas).To(Equal(5))
		})

		It("lists all configs", func() {
			Expect(registry.SetModelScheduling(context.Background(), &ModelSchedulingConfig{ModelName: "list-a", MinReplicas: 1})).To(Succeed())
			Expect(registry.SetModelScheduling(context.Background(), &ModelSchedulingConfig{ModelName: "list-b", MaxReplicas: 2})).To(Succeed())

			configs, err := registry.ListModelSchedulings(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(len(configs)).To(BeNumerically(">=", 2))
		})

		It("lists only auto-scaling configs", func() {
			Expect(registry.SetModelScheduling(context.Background(), &ModelSchedulingConfig{ModelName: "auto-a", MinReplicas: 2})).To(Succeed())
			Expect(registry.SetModelScheduling(context.Background(), &ModelSchedulingConfig{ModelName: "auto-b", MaxReplicas: 3})).To(Succeed())
			Expect(registry.SetModelScheduling(context.Background(), &ModelSchedulingConfig{ModelName: "no-auto", NodeSelector: `{"env":"prod"}`})).To(Succeed())

			configs, err := registry.ListAutoScalingConfigs(context.Background())
			Expect(err).ToNot(HaveOccurred())

			names := make([]string, len(configs))
			for i, c := range configs {
				names[i] = c.ModelName
			}
			Expect(names).To(ContainElement("auto-a"))
			Expect(names).To(ContainElement("auto-b"))
			Expect(names).ToNot(ContainElement("no-auto"))
		})

		It("deletes a config", func() {
			Expect(registry.SetModelScheduling(context.Background(), &ModelSchedulingConfig{ModelName: "delete-me", MinReplicas: 1})).To(Succeed())

			Expect(registry.DeleteModelScheduling(context.Background(), "delete-me")).To(Succeed())

			fetched, err := registry.GetModelScheduling(context.Background(), "delete-me")
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched).To(BeNil())
		})

		It("returns nil for non-existent model", func() {
			fetched, err := registry.GetModelScheduling(context.Background(), "does-not-exist")
			Expect(err).ToNot(HaveOccurred())
			Expect(fetched).To(BeNil())
		})
	})

	Describe("CountLoadedReplicas", func() {
		It("returns correct count of loaded replicas", func() {
			n1 := makeNode("replica-node-1", "10.0.0.90:50051", 8_000_000_000)
			n2 := makeNode("replica-node-2", "10.0.0.91:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), n1, true)).To(Succeed())
			Expect(registry.Register(context.Background(), n2, true)).To(Succeed())

			Expect(registry.SetNodeModel(context.Background(), n1.ID, "counted-model", "loaded", "", 0)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), n2.ID, "counted-model", "loaded", "", 0)).To(Succeed())

			count, err := registry.CountLoadedReplicas(context.Background(), "counted-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(count).To(Equal(int64(2)))
		})

		It("excludes non-loaded states", func() {
			n1 := makeNode("replica-loaded", "10.0.0.92:50051", 8_000_000_000)
			n2 := makeNode("replica-loading", "10.0.0.93:50051", 8_000_000_000)
			Expect(registry.Register(context.Background(), n1, true)).To(Succeed())
			Expect(registry.Register(context.Background(), n2, true)).To(Succeed())

			Expect(registry.SetNodeModel(context.Background(), n1.ID, "state-model", "loaded", "", 0)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), n2.ID, "state-model", "loading", "", 0)).To(Succeed())

			count, err := registry.CountLoadedReplicas(context.Background(), "state-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(count).To(Equal(int64(1)))
		})
	})

	Describe("DecrementInFlight", func() {
		It("does not go below zero", func() {
			node := makeNode("dec-node", "10.0.0.50:50051", 4_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "dec-model", "loaded", "", 0)).To(Succeed())

			// in_flight starts at 0 — decrement should be a no-op
			Expect(registry.DecrementInFlight(context.Background(), node.ID, "dec-model")).To(Succeed())

			nm, err := registry.GetNodeModel(context.Background(), node.ID, "dec-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(nm.InFlight).To(Equal(0))
		})

		It("decrements correctly from a positive value", func() {
			node := makeNode("dec-node-2", "10.0.0.51:50051", 4_000_000_000)
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "dec-model-2", "loaded", "", 0)).To(Succeed())

			Expect(registry.IncrementInFlight(context.Background(), node.ID, "dec-model-2")).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), node.ID, "dec-model-2")).To(Succeed())

			nm, err := registry.GetNodeModel(context.Background(), node.ID, "dec-model-2")
			Expect(err).ToNot(HaveOccurred())
			Expect(nm.InFlight).To(Equal(2))

			Expect(registry.DecrementInFlight(context.Background(), node.ID, "dec-model-2")).To(Succeed())

			nm, err = registry.GetNodeModel(context.Background(), node.ID, "dec-model-2")
			Expect(err).ToNot(HaveOccurred())
			Expect(nm.InFlight).To(Equal(1))
		})
	})
})
