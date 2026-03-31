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

// ---------------------------------------------------------------------------
// Fake ModelScheduler
// ---------------------------------------------------------------------------

type fakeScheduler struct {
	scheduleNode  *BackendNode
	scheduleErr   error
	scheduleCalls []scheduleCall
}

type scheduleCall struct {
	modelName    string
	candidateIDs []string
}

func (f *fakeScheduler) ScheduleAndLoadModel(_ context.Context, modelName string, candidateNodeIDs []string) (*BackendNode, error) {
	f.scheduleCalls = append(f.scheduleCalls, scheduleCall{modelName, candidateNodeIDs})
	return f.scheduleNode, f.scheduleErr
}

var _ = Describe("ReplicaReconciler", func() {
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

	// Helper to register a healthy node.
	registerNode := func(name, address string) *BackendNode {
		node := &BackendNode{
			Name:     name,
			NodeType: NodeTypeBackend,
			Address:  address,
		}
		Expect(registry.Register(context.Background(), node, true)).To(Succeed())
		return node
	}

	// Helper to set up a scheduling config.
	setSchedulingConfig := func(modelName string, minReplicas, maxReplicas int, nodeSelector string) {
		cfg := &ModelSchedulingConfig{
			ModelName:    modelName,
			MinReplicas:  minReplicas,
			MaxReplicas:  maxReplicas,
			NodeSelector: nodeSelector,
		}
		Expect(registry.SetModelScheduling(context.Background(), cfg)).To(Succeed())
	}

	Context("model below min_replicas", func() {
		It("scales up to min_replicas", func() {
			node := registerNode("node-1", "10.0.0.1:50051")
			setSchedulingConfig("model-a", 2, 4, "")

			scheduler := &fakeScheduler{
				scheduleNode: node,
			}
			reconciler := NewReplicaReconciler(ReplicaReconcilerOptions{
				Registry:  registry,
				Scheduler: scheduler,
				DB:        db,
			})

			// No replicas loaded — should schedule 2
			reconciler.reconcile(context.Background())

			Expect(scheduler.scheduleCalls).To(HaveLen(2))
			Expect(scheduler.scheduleCalls[0].modelName).To(Equal("model-a"))
			Expect(scheduler.scheduleCalls[1].modelName).To(Equal("model-a"))
		})
	})

	Context("all replicas busy and below max_replicas", func() {
		It("scales up by 1", func() {
			node := registerNode("node-busy", "10.0.0.2:50051")
			setSchedulingConfig("model-b", 1, 4, "")

			// Load 2 replicas, both busy (in_flight > 0)
			Expect(registry.SetNodeModel(context.Background(), node.ID, "model-b", "loaded", "addr1", 0)).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), node.ID, "model-b")).To(Succeed())

			node2 := registerNode("node-busy-2", "10.0.0.3:50051")
			Expect(registry.SetNodeModel(context.Background(), node2.ID, "model-b", "loaded", "addr2", 0)).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), node2.ID, "model-b")).To(Succeed())

			scheduler := &fakeScheduler{
				scheduleNode: node,
			}
			reconciler := NewReplicaReconciler(ReplicaReconcilerOptions{
				Registry:  registry,
				Scheduler: scheduler,
				DB:        db,
			})

			reconciler.reconcile(context.Background())

			Expect(scheduler.scheduleCalls).To(HaveLen(1))
			Expect(scheduler.scheduleCalls[0].modelName).To(Equal("model-b"))
		})
	})

	Context("all replicas busy and at max_replicas", func() {
		It("does not scale up", func() {
			node := registerNode("node-max", "10.0.0.4:50051")
			setSchedulingConfig("model-c", 1, 2, "")

			// Load 2 replicas (at max), both busy
			Expect(registry.SetNodeModel(context.Background(), node.ID, "model-c", "loaded", "addr1", 0)).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), node.ID, "model-c")).To(Succeed())

			node2 := registerNode("node-max-2", "10.0.0.5:50051")
			Expect(registry.SetNodeModel(context.Background(), node2.ID, "model-c", "loaded", "addr2", 0)).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), node2.ID, "model-c")).To(Succeed())

			scheduler := &fakeScheduler{
				scheduleNode: node,
			}
			reconciler := NewReplicaReconciler(ReplicaReconcilerOptions{
				Registry:  registry,
				Scheduler: scheduler,
				DB:        db,
			})

			reconciler.reconcile(context.Background())

			Expect(scheduler.scheduleCalls).To(BeEmpty())
		})
	})

	Context("idle replicas above min_replicas", func() {
		It("scales down after idle delay", func() {
			node1 := registerNode("node-idle-1", "10.0.0.6:50051")
			node2 := registerNode("node-idle-2", "10.0.0.7:50051")
			node3 := registerNode("node-idle-3", "10.0.0.8:50051")
			setSchedulingConfig("model-d", 1, 4, "")

			// Load 3 replicas, all idle with last_used in the past
			pastTime := time.Now().Add(-10 * time.Minute)
			for _, n := range []*BackendNode{node1, node2, node3} {
				Expect(registry.SetNodeModel(context.Background(), n.ID, "model-d", "loaded", "", 0)).To(Succeed())
				// Set last_used to past time to trigger scale-down
				db.Model(&NodeModel{}).Where("node_id = ? AND model_name = ?", n.ID, "model-d").
					Update("last_used", pastTime)
			}

			unloader := &fakeUnloader{}
			reconciler := NewReplicaReconciler(ReplicaReconcilerOptions{
				Registry:       registry,
				Unloader:       unloader,
				DB:             db,
				ScaleDownDelay: 1 * time.Minute, // short delay for test
			})

			reconciler.reconcile(context.Background())

			// Should scale down 2 replicas (3 - floor of 1)
			Expect(unloader.unloadCalls).To(HaveLen(2))
		})
	})

	Context("idle replicas at min_replicas", func() {
		It("does not scale down", func() {
			node1 := registerNode("node-keep-1", "10.0.0.9:50051")
			node2 := registerNode("node-keep-2", "10.0.0.10:50051")
			setSchedulingConfig("model-e", 2, 4, "")

			// Load exactly 2 replicas (at min), both idle with past last_used
			pastTime := time.Now().Add(-10 * time.Minute)
			for _, n := range []*BackendNode{node1, node2} {
				Expect(registry.SetNodeModel(context.Background(), n.ID, "model-e", "loaded", "", 0)).To(Succeed())
				db.Model(&NodeModel{}).Where("node_id = ? AND model_name = ?", n.ID, "model-e").
					Update("last_used", pastTime)
			}

			unloader := &fakeUnloader{}
			reconciler := NewReplicaReconciler(ReplicaReconcilerOptions{
				Registry:       registry,
				Unloader:       unloader,
				DB:             db,
				ScaleDownDelay: 1 * time.Minute,
			})

			reconciler.reconcile(context.Background())

			Expect(unloader.unloadCalls).To(BeEmpty())
		})
	})

	Context("model with node_selector", func() {
		It("passes candidate node IDs to scheduler", func() {
			node1 := registerNode("gpu-node", "10.0.0.11:50051")
			node2 := registerNode("cpu-node", "10.0.0.12:50051")

			// Add labels — only node1 matches the selector
			Expect(registry.SetNodeLabel(context.Background(), node1.ID, "gpu.vendor", "nvidia")).To(Succeed())
			Expect(registry.SetNodeLabel(context.Background(), node2.ID, "gpu.vendor", "none")).To(Succeed())

			setSchedulingConfig("model-f", 1, 2, `{"gpu.vendor":"nvidia"}`)

			scheduler := &fakeScheduler{
				scheduleNode: node1,
			}
			reconciler := NewReplicaReconciler(ReplicaReconcilerOptions{
				Registry:  registry,
				Scheduler: scheduler,
				DB:        db,
			})

			// No replicas loaded — should schedule 1 with candidate node IDs
			reconciler.reconcile(context.Background())

			Expect(scheduler.scheduleCalls).To(HaveLen(1))
			Expect(scheduler.scheduleCalls[0].modelName).To(Equal("model-f"))
			Expect(scheduler.scheduleCalls[0].candidateIDs).To(ContainElement(node1.ID))
			Expect(scheduler.scheduleCalls[0].candidateIDs).ToNot(ContainElement(node2.ID))
		})
	})
})
