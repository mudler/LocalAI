package nodes

import (
	"context"
	"fmt"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// When every node a model may use hosts a model it shares a concurrency group
// with, the scheduler must evict the conflict, as single-node mode does,
// instead of loading beside it (#13366).
var _ = Describe("Concurrency-group eviction in distributed mode", func() {
	var (
		db       *gorm.DB
		registry *NodeRegistry
		unloader *fakeUnloader
		resolver *fakeConflictResolver
		pinned   *fakePinnedResolver
		router   *SmartRouter
		ctx      context.Context
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		unloader = &fakeUnloader{installReply: &workerctl.BackendInstallReply{Success: true, Address: "10.0.0.1:9001"}}
		resolver = &fakeConflictResolver{conflicts: map[string][]string{"big": {"rival"}}}
		pinned = &fakePinnedResolver{}
		router = NewSmartRouter(registry, SmartRouterOptions{DB: db, Unloader: unloader, ConflictResolver: resolver, PinnedResolver: pinned})
		ctx = context.Background()
	})

	register := func(name string) *BackendNode {
		node := &BackendNode{Name: name, NodeType: NodeTypeBackend, Address: name + ":50051"}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		fetched, err := registry.GetByName(ctx, name)
		Expect(err).ToNot(HaveOccurred())
		Expect(registry.SetNodeLabel(ctx, fetched.ID, "gpu", name)).To(Succeed())
		Expect(registry.SetNodeLabel(ctx, fetched.ID, "pool", "shared")).To(Succeed())
		return fetched
	}

	rowID := 0
	seed := func(node *BackendNode, model string, idleFor time.Duration, inFlight int) {
		rowID++
		Expect(db.Create(&NodeModel{
			ID: fmt.Sprintf("group-row-%d", rowID), NodeID: node.ID, ModelName: model,
			Address: node.Address, State: "loaded", InFlight: inFlight,
			LastUsed: time.Now().Add(-idleFor), UpdatedAt: time.Now(),
		}).Error).To(Succeed())
	}

	// allow pins the model to one node, or to the shared pool of all nodes.
	allow := func(model string, nodes ...*BackendNode) {
		selector := `{"gpu":"` + nodes[0].Name + `"}`
		if len(nodes) > 1 {
			selector = `{"pool":"shared"}`
		}
		Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: model, NodeSelector: selector})).To(Succeed())
	}

	schedule := func() *BackendNode {
		node, _, _, err := router.scheduleNewModel(ctx, "llama-cpp", "big", &pb.ModelOptions{})
		Expect(err).ToNot(HaveOccurred())
		return node
	}

	It("evicts only the conflicting model when every allowed node hosts one", func() {
		gpu := register("gpu-a")
		seed(gpu, "other", 2*time.Hour, 0) // older, but not in the group
		seed(gpu, "rival", time.Hour, 0)
		allow("big", gpu)

		Expect(schedule().ID).To(Equal(gpu.ID))
		Expect(unloader.unloadCalls).To(ConsistOf(gpu.ID + ":rival"))
	})

	It("prefers a node without a conflict and evicts nothing", func() {
		busy := register("gpu-a")
		free := register("gpu-b")
		seed(busy, "rival", time.Hour, 0)
		allow("big", busy, free)

		Expect(schedule().ID).To(Equal(free.ID))
		Expect(unloader.unloadCalls).To(BeEmpty())
	})

	It("never evicts a conflict that is serving a request, and still loads", func() {
		gpu := register("gpu-a")
		seed(gpu, "rival", time.Hour, 1)
		allow("big", gpu)

		Expect(schedule().ID).To(Equal(gpu.ID))
		Expect(unloader.unloadCalls).To(BeEmpty())
	})

	It("never evicts a pinned conflict", func() {
		gpu := register("gpu-a")
		seed(gpu, "rival", time.Hour, 0)
		pinned.names = []string{"rival"}
		allow("big", gpu)

		Expect(schedule().ID).To(Equal(gpu.ID))
		Expect(unloader.unloadCalls).To(BeEmpty())
	})
})
