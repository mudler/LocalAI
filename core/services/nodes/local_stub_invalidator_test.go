package nodes

import (
	"context"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/pkg/model"
)

// In distributed mode the frontend keeps an in-process stub for every model it
// has routed, and DistributedModelStore.Range reports local stubs UNION the
// registry rows. Every registry removal path deletes the DB row only, so
// without an invalidator the stub outlives the replica and the model is
// reported as loaded forever (visible on the home page, absent from the nodes
// page). These tests pin the invalidator that closes that gap.
var _ = Describe("LocalStubInvalidator", func() {
	var (
		db       *gorm.DB
		registry *NodeRegistry
		local    *model.InMemoryModelStore
		nodeA    *BackendNode
		nodeB    *BackendNode
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		local = model.NewInMemoryModelStore()
		nodeA = &BackendNode{Name: "node-a", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051"}
		nodeB = &BackendNode{Name: "node-b", NodeType: NodeTypeBackend, Address: "10.0.0.2:50051"}
		Expect(registry.Register(context.Background(), nodeA, true)).To(Succeed())
		Expect(registry.Register(context.Background(), nodeB, true)).To(Succeed())
	})

	It("drops the local stub once the last replica of the model is gone", func() {
		store := NewDistributedModelStore(local, registry)
		Expect(registry.SetNodeModel(context.Background(), nodeA.ID, "ghost-model", 0, "loaded", "10.0.0.1:12345", 0)).To(Succeed())
		local.Set("ghost-model", model.NewModel("ghost-model", "10.0.0.1:12345", nil))

		registry.AddReplicaRemovedHook(NewLocalStubInvalidator(registry, store))
		Expect(registry.RemoveNodeModel(context.Background(), nodeA.ID, "ghost-model", 0)).To(Succeed())

		_, ok := local.Get("ghost-model")
		Expect(ok).To(BeFalse(), "the stub must not outlive the last replica of the model")

		// And the model must disappear from the loaded listing that feeds /system.
		listed := []string{}
		store.Range(func(id string, _ *model.Model) bool {
			listed = append(listed, id)
			return true
		})
		Expect(listed).ToNot(ContainElement("ghost-model"))
	})

	It("keeps the local stub while another replica still serves the model", func() {
		store := NewDistributedModelStore(local, registry)
		Expect(registry.SetNodeModel(context.Background(), nodeA.ID, "shared-model", 0, "loaded", "10.0.0.1:12345", 0)).To(Succeed())
		Expect(registry.SetNodeModel(context.Background(), nodeB.ID, "shared-model", 0, "loaded", "10.0.0.2:12345", 0)).To(Succeed())
		local.Set("shared-model", model.NewModel("shared-model", "10.0.0.1:12345", nil))

		registry.AddReplicaRemovedHook(NewLocalStubInvalidator(registry, store))
		Expect(registry.RemoveNodeModel(context.Background(), nodeA.ID, "shared-model", 0)).To(Succeed())

		_, ok := local.Get("shared-model")
		Expect(ok).To(BeTrue(), "a model still loaded on another node must stay in the local store")
	})

	It("fires every registered replica-removed hook", func() {
		Expect(registry.SetNodeModel(context.Background(), nodeA.ID, "m", 0, "loaded", "10.0.0.1:12345", 0)).To(Succeed())

		var first, second int
		registry.AddReplicaRemovedHook(func(_, _ string, _ int) { first++ })
		registry.AddReplicaRemovedHook(func(_, _ string, _ int) { second++ })

		Expect(registry.RemoveNodeModel(context.Background(), nodeA.ID, "m", 0)).To(Succeed())

		// Prefix-cache invalidation and local-stub invalidation are independent
		// subsystems; registering one must never silently displace the other.
		Expect(first).To(Equal(1))
		Expect(second).To(Equal(1))
	})

	It("drops the local stub when a whole node's replicas are removed", func() {
		store := NewDistributedModelStore(local, registry)
		Expect(registry.SetNodeModel(context.Background(), nodeA.ID, "node-model", 0, "loaded", "10.0.0.1:12345", 0)).To(Succeed())
		local.Set("node-model", model.NewModel("node-model", "10.0.0.1:12345", nil))

		registry.AddReplicaRemovedHook(NewLocalStubInvalidator(registry, store))
		// Negative replica index signals "all replicas of this model on the node",
		// the shape used by node deregistration and health reaping.
		Expect(registry.RemoveAllNodeModelReplicas(context.Background(), nodeA.ID, "node-model")).To(Succeed())

		Eventually(func() bool {
			_, ok := local.Get("node-model")
			return ok
		}, time.Second, 10*time.Millisecond).Should(BeFalse())
	})
})
