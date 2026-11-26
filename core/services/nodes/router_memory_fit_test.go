package nodes

import (
	"context"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// A model with a size estimate used to be placed on any node when no node had
// that much free GPU memory ("falling back to standard scheduling"). It must
// now go only where it fits, after evicting idle models if that makes room,
// and otherwise fail with a retryable error.
var _ = Describe("Memory fit of a cold load", func() {
	const (
		gib   = uint64(1) << 30
		model = "model-x"
	)
	var (
		ctx      context.Context
		db       *gorm.DB
		registry *NodeRegistry
		unloader *fakeUnloader
		pinned   *fakePinnedResolver
		estimate uint64
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		ctx = context.Background()
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		blob, err := proto.Marshal(&pb.ModelOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(registry.UpsertModelLoadInfo(ctx, model, "llama-cpp", blob)).To(Succeed())
		unloader = &fakeUnloader{installReply: &workerctl.BackendInstallReply{Success: true, Address: "127.0.0.1:9001"}}
		pinned = &fakePinnedResolver{}
		estimate = 37 * gib
	})

	newRouter := func() *SmartRouter {
		backend := &stubBackend{healthResult: true, loadResult: &pb.Result{Success: true}}
		r := NewSmartRouter(registry, SmartRouterOptions{
			Unloader: unloader, ClientFactory: &stubClientFactory{client: backend}, DB: db, PinnedResolver: pinned,
		})
		r.vramEstimator = func(context.Context, *pb.ModelOptions) uint64 { return estimate }
		return r
	}

	register := func(name string, total, available uint64) *BackendNode {
		node := &BackendNode{Name: name, NodeType: NodeTypeBackend, Address: name + ":50051",
			MaxReplicasPerModel: 4, TotalVRAM: total, AvailableVRAM: available}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		fetched, err := registry.GetByName(ctx, name)
		Expect(err).NotTo(HaveOccurred())
		return fetched
	}

	installedOn := func() []string {
		unloader.mu.Lock()
		defer unloader.mu.Unlock()
		ids := []string{}
		for _, c := range unloader.installCalls {
			ids = append(ids, c.nodeID)
		}
		return ids
	}

	replicasOf := func(name string) int64 {
		var n int64
		Expect(db.Model(&NodeModel{}).Where("model_name = ?", name).Count(&n).Error).To(Succeed())
		return n
	}

	// node-a holds two idle models in 40 GiB of its 48 GiB; node-b is empty but
	// too small for the model at any time.
	var nodeA, nodeB *BackendNode
	setupFull := func() {
		nodeA = register("node-a", 48*gib, 8*gib)
		nodeB = register("node-b", 24*gib, 24*gib)
		Expect(registry.SetNodeModel(ctx, nodeA.ID, "model-small-1", 0, "loaded", "", 0)).To(Succeed())
		Expect(registry.SetNodeModel(ctx, nodeA.ID, "model-small-2", 0, "loaded", "", 0)).To(Succeed())
	}

	It("places the model where it fits now, without evicting", func() {
		setupFull()
		roomy := register("node-c", 80*gib, 60*gib)

		node, err := newRouter().ScheduleAndLoadModel(ctx, model, nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(node.ID).To(Equal(roomy.ID))
		Expect(unloader.unloadCalls).To(BeEmpty())
	})

	It("evicts idle models on the node where that makes room", func() {
		setupFull()

		node, err := newRouter().ScheduleAndLoadModel(ctx, model, nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(node.ID).To(Equal(nodeA.ID))
		Expect(installedOn()).To(ConsistOf(nodeA.ID))
		Expect(installedOn()).NotTo(ContainElement(nodeB.ID), "node-b can never hold the model")
		Expect(replicasOf("model-small-1") + replicasOf("model-small-2")).To(BeZero())
	})

	It("prefers a node that fits now over evicting", func() {
		setupFull()
		estimate = 20 * gib

		node, err := newRouter().ScheduleAndLoadModel(ctx, model, nil)

		Expect(err).NotTo(HaveOccurred())
		// node-b fits 20 GiB without eviction, so it wins over evicting on node-a.
		Expect(node.ID).To(Equal(nodeB.ID))
		Expect(unloader.unloadCalls).To(BeEmpty())
	})

	It("does not evict a model that is serving a request", func() {
		setupFull()
		Expect(registry.IncrementInFlight(ctx, nodeA.ID, "model-small-1", 0)).To(Succeed())

		_, err := newRouter().ScheduleAndLoadModel(ctx, model, nil)

		Expect(err).To(MatchError(ErrInsufficientMemory))
		Expect(err).To(MatchError(ErrNoAvailableNodes), "retryable: the HTTP layer answers 503")
		Expect(installedOn()).To(BeEmpty(), "the model is not placed anyway")
		Expect(replicasOf("model-small-1")).To(BeNumerically("==", 1))
		Expect(replicasOf("model-small-2")).To(BeNumerically("==", 1), "a partial eviction that cannot make room evicts nothing")
	})

	It("does not evict a pinned model", func() {
		setupFull()
		pinned.names = []string{"model-small-2"}

		_, err := newRouter().ScheduleAndLoadModel(ctx, model, nil)

		Expect(err).To(MatchError(ErrInsufficientMemory))
		Expect(replicasOf("model-small-2")).To(BeNumerically("==", 1))
	})

	It("does not evict below a rule's replica floor", func() {
		setupFull()
		Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "model-small-1", MinReplicas: 1})).To(Succeed())

		_, err := newRouter().ScheduleAndLoadModel(ctx, model, nil)

		Expect(err).To(MatchError(ErrInsufficientMemory))
		Expect(replicasOf("model-small-1")).To(BeNumerically("==", 1))
	})

	It("still uses a node that reports no GPU memory", func() {
		setupFull()
		Expect(registry.IncrementInFlight(ctx, nodeA.ID, "model-small-1", 0)).To(Succeed())
		cpuOnly := register("node-cpu", 0, 0)

		node, err := newRouter().ScheduleAndLoadModel(ctx, model, nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(node.ID).To(Equal(cpuOnly.ID))
	})

	// The eviction a placement plans runs only once this replica owns the
	// load of the model on that node. A claim another frontend won, or a model
	// that became ready in the meantime, leaves the idle models alone.
	It("evicts nothing when the claim on the chosen node is lost", func() {
		setupFull()
		stolen := &claimHookRegistry{NodeRegistry: registry}
		stolen.beforeClaim = func(ctx context.Context, key, nodeID string) {
			_, won, err := registry.ClaimLoadJobOn(ctx, key, nodeID, "another-frontend")
			Expect(err).NotTo(HaveOccurred())
			Expect(won).To(BeTrue())
		}
		r := newRouter()
		r.registry = stolen

		_, err := r.ScheduleAndLoadModel(ctx, model, nil)

		Expect(err).To(MatchError(ErrLoadInProgress))
		Expect(unloader.unloadCalls).To(BeEmpty())
		Expect(replicasOf("model-small-1")).To(BeNumerically("==", 1))
		Expect(replicasOf("model-small-2")).To(BeNumerically("==", 1))
		Expect(installedOn()).To(BeEmpty())
	})

	It("evicts nothing when the model is ready after the claim", func() {
		setupFull()
		hooked := &claimHookRegistry{NodeRegistry: registry}
		hooked.afterClaim = func(ctx context.Context, key, _ string) {
			// Another node finished a load of the model just as this claim won.
			Expect(registry.SetNodeModel(ctx, nodeB.ID, key, 0, "loaded", "127.0.0.1:9002", 0)).To(Succeed())
		}
		r := newRouter()
		r.registry = hooked

		result, err := r.Route(ctx, model, "models/x.gguf", "llama-cpp", "", &pb.ModelOptions{Model: "models/x.gguf"}, false)

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Node.ID).To(Equal(nodeB.ID))
		Expect(unloader.unloadCalls).To(BeEmpty())
		Expect(replicasOf("model-small-1")).To(BeNumerically("==", 1))
		Expect(replicasOf("model-small-2")).To(BeNumerically("==", 1))
		Expect(installedOn()).To(BeEmpty())
	})

	It("keeps the old placement when the model has no size estimate", func() {
		setupFull()
		estimate = 0

		node, err := newRouter().ScheduleAndLoadModel(ctx, model, nil)

		Expect(err).NotTo(HaveOccurred())
		// The idle-first scheduler picks the empty node, whatever its memory.
		Expect(node.ID).To(Equal(nodeB.ID))
	})
})

// claimHookRegistry runs a hook around the load-job claim, so a spec can lose
// the claim to another frontend or change the cluster right after it.
type claimHookRegistry struct {
	*NodeRegistry
	beforeClaim func(ctx context.Context, key, nodeID string)
	afterClaim  func(ctx context.Context, key, nodeID string)
}

func (c *claimHookRegistry) ClaimLoadJobOn(ctx context.Context, key, nodeID, owner string) (*ModelLoadJob, bool, error) {
	if c.beforeClaim != nil {
		c.beforeClaim(ctx, key, nodeID)
	}
	job, won, err := c.NodeRegistry.ClaimLoadJobOn(ctx, key, nodeID, owner)
	if err == nil && won && c.afterClaim != nil {
		c.afterClaim(ctx, key, nodeID)
	}
	return job, won, err
}
