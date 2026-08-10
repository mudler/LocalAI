package nodes

// Regression tests for #11101: `pinned: true` must hold cluster-wide, not
// just against the per-node watchdog. Before the fix, every distributed
// eviction path (EvictLRU, evictLRUAndFreeNode, scaleDownIdle) was
// pinned-blind, so a pinned model became eviction-eligible the moment its
// in-flight count dropped to zero — observable as the backend being freed
// immediately after every request under capacity pressure.

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/testutil"
	"gorm.io/gorm"
	"runtime"
)

type fakePinnedResolver struct {
	names []string
}

func (f *fakePinnedResolver) GetPinnedModelNames() []string { return f.names }

var _ = Describe("Pinned models vs distributed eviction (#11101)", func() {
	Describe("EvictLRU (mock-based)", func() {
		It("excludes pinned models at the query, evicting nothing when only a pinned model is idle", func() {
			reg := &fakeModelRouter{
				findLRUModel: &NodeModel{NodeID: "n1", ModelName: "pinned-model"},
			}
			unloader := &fakeUnloader{}

			router := NewSmartRouter(reg, SmartRouterOptions{
				Unloader:       unloader,
				PinnedResolver: &fakePinnedResolver{names: []string{"pinned-model"}},
			})

			_, err := router.EvictLRU(context.Background(), "n1")
			Expect(err).To(HaveOccurred())
			// The exclusion must reach the registry query: filtering after
			// selection would just burn the attempt instead of picking the
			// next-oldest unpinned model.
			Expect(reg.findLRUExclude).To(ContainElement("pinned-model"))
			Expect(unloader.stopCalls).To(BeEmpty())
		})

		It("still evicts unpinned models when a resolver is wired", func() {
			reg := &fakeModelRouter{
				findLRUModel: &NodeModel{NodeID: "n1", ModelName: "plain-model"},
			}
			unloader := &fakeUnloader{}

			router := NewSmartRouter(reg, SmartRouterOptions{
				Unloader:       unloader,
				PinnedResolver: &fakePinnedResolver{names: []string{"some-other-model"}},
			})

			evicted, err := router.EvictLRU(context.Background(), "n1")
			Expect(err).ToNot(HaveOccurred())
			Expect(evicted).To(Equal("plain-model"))
			Expect(unloader.stopCalls).To(ContainElement("n1:plain-model"))
		})
	})

	Describe("evictLRUAndFreeNode (integration)", func() {
		var (
			db       *gorm.DB
			registry *NodeRegistry
			node     *BackendNode
		)

		BeforeEach(func() {
			if runtime.GOOS == "darwin" {
				Skip("testcontainers requires Docker, not available on macOS CI")
			}
			db = testutil.SetupTestDB()
			var err error
			registry, err = NewNodeRegistry(db)
			Expect(err).ToNot(HaveOccurred())

			node = &BackendNode{
				Name:     "pinned-evict-node",
				NodeType: NodeTypeBackend,
				Address:  "10.0.0.200:50051",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.MarkHealthy(context.Background(), node.ID)).To(Succeed())
		})

		// setLoadedModel creates an idle loaded replica row with a controlled
		// last_used so the specs can dictate LRU order.
		setLoadedModel := func(name string, lastUsed time.Time) {
			Expect(registry.SetNodeModel(context.Background(), node.ID, name, 0, "loaded", "", 0)).To(Succeed())
			Expect(db.Model(&NodeModel{}).
				Where("node_id = ? AND model_name = ?", node.ID, name).
				Update("last_used", lastUsed).Error).ToNot(HaveOccurred())
		}

		It("skips the pinned LRU model and evicts the next-oldest unpinned one", func() {
			// The pinned model is the older (= LRU) candidate. Without the
			// exclusion it would be selected first.
			setLoadedModel("pinned-old", time.Now().Add(-2*time.Hour))
			setLoadedModel("plain-new", time.Now().Add(-1*time.Hour))

			router := NewSmartRouter(registry, SmartRouterOptions{
				DB:             db,
				PinnedResolver: &fakePinnedResolver{names: []string{"pinned-old"}},
			})

			freed, err := router.evictLRUAndFreeNode(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(freed.ID).To(Equal(node.ID))

			var remaining []NodeModel
			Expect(db.Where("node_id = ?", node.ID).Find(&remaining).Error).ToNot(HaveOccurred())
			Expect(remaining).To(HaveLen(1))
			Expect(remaining[0].ModelName).To(Equal("pinned-old"))
		})

		It("returns ErrEvictionBusy instead of evicting when every idle model is pinned", func() {
			setLoadedModel("pinned-only", time.Now().Add(-2*time.Hour))

			router := NewSmartRouter(registry, SmartRouterOptions{
				DB:             db,
				PinnedResolver: &fakePinnedResolver{names: []string{"pinned-only"}},
			})

			_, err := router.evictLRUAndFreeNode(context.Background())
			Expect(err).To(MatchError(ErrEvictionBusy))

			var count int64
			Expect(db.Model(&NodeModel{}).Where("model_name = ?", "pinned-only").Count(&count).Error).ToNot(HaveOccurred())
			Expect(count).To(Equal(int64(1)), "the pinned model's replica row must survive")
		})

		It("still evicts the LRU model when no resolver is wired (embedder/back-compat path)", func() {
			setLoadedModel("plain-old", time.Now().Add(-2*time.Hour))

			router := NewSmartRouter(registry, SmartRouterOptions{DB: db})

			freed, err := router.evictLRUAndFreeNode(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(freed.ID).To(Equal(node.ID))
		})
	})

	Describe("scaleDownIdle (integration)", func() {
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

		registerNode := func(name, address string) *BackendNode {
			node := &BackendNode{
				Name:                name,
				NodeType:            NodeTypeBackend,
				Address:             address,
				MaxReplicasPerModel: 4,
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			return node
		}

		It("does not scale down idle replicas of a pinned model", func() {
			node1 := registerNode("pin-idle-1", "10.0.1.1:50051")
			node2 := registerNode("pin-idle-2", "10.0.1.2:50051")
			node3 := registerNode("pin-idle-3", "10.0.1.3:50051")
			Expect(registry.SetModelScheduling(context.Background(), &ModelSchedulingConfig{
				ModelName: "pinned-replicated", MinReplicas: 1, MaxReplicas: 4,
			})).To(Succeed())

			// Three idle replicas above the floor of one — prime scale-down bait.
			pastTime := time.Now().Add(-10 * time.Minute)
			for _, n := range []*BackendNode{node1, node2, node3} {
				Expect(registry.SetNodeModel(context.Background(), n.ID, "pinned-replicated", 0, "loaded", "", 0)).To(Succeed())
				db.Model(&NodeModel{}).Where("node_id = ? AND model_name = ?", n.ID, "pinned-replicated").
					Update("last_used", pastTime)
			}

			unloader := &fakeUnloader{}
			reconciler := NewReplicaReconciler(ReplicaReconcilerOptions{
				Registry:       registry,
				Unloader:       unloader,
				DB:             db,
				ScaleDownDelay: 1 * time.Minute,
				PinnedResolver: &fakePinnedResolver{names: []string{"pinned-replicated"}},
			})

			reconciler.reconcile(context.Background())

			Expect(unloader.unloadCalls).To(BeEmpty())
		})

		It("scales down unpinned models normally with a resolver wired", func() {
			node1 := registerNode("unpin-idle-1", "10.0.1.4:50051")
			node2 := registerNode("unpin-idle-2", "10.0.1.5:50051")
			Expect(registry.SetModelScheduling(context.Background(), &ModelSchedulingConfig{
				ModelName: "plain-replicated", MinReplicas: 1, MaxReplicas: 4,
			})).To(Succeed())

			pastTime := time.Now().Add(-10 * time.Minute)
			for _, n := range []*BackendNode{node1, node2} {
				Expect(registry.SetNodeModel(context.Background(), n.ID, "plain-replicated", 0, "loaded", "", 0)).To(Succeed())
				db.Model(&NodeModel{}).Where("node_id = ? AND model_name = ?", n.ID, "plain-replicated").
					Update("last_used", pastTime)
			}

			unloader := &fakeUnloader{}
			reconciler := NewReplicaReconciler(ReplicaReconcilerOptions{
				Registry:       registry,
				Unloader:       unloader,
				DB:             db,
				ScaleDownDelay: 1 * time.Minute,
				PinnedResolver: &fakePinnedResolver{names: []string{"some-other-model"}},
			})

			reconciler.reconcile(context.Background())

			Expect(unloader.unloadCalls).To(HaveLen(1))
		})
	})
})
