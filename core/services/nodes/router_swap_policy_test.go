package nodes

import (
	"context"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// The cluster memory settings in the scheduler: the model limit of each
// node, model swap, the eviction order and eviction of busy replicas.
var _ = Describe("Scheduler memory policy", func() {
	const (
		gib   = uint64(1) << 30
		model = "model-x"
	)
	var (
		ctx      context.Context
		db       *gorm.DB
		registry *NodeRegistry
		unloader *fakeUnloader
		policy   MemoryPolicy
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
		policy = DefaultMemoryPolicy()
		policy.LRUMaxRetries = 1
		policy.LRURetryInterval = 10 * time.Millisecond
		registry.SetMemoryPolicy(func() MemoryPolicy { return policy })
		estimate = 0
	})

	newRouter := func() *SmartRouter {
		backend := &stubBackend{healthResult: true, loadResult: &pb.Result{Success: true}}
		r := NewSmartRouter(registry, SmartRouterOptions{
			Unloader: unloader, ClientFactory: &stubClientFactory{client: backend}, DB: db,
		})
		r.vramEstimator = func(context.Context, *pb.ModelOptions) uint64 { return estimate }
		return r
	}

	register := func(name string) *BackendNode {
		node := &BackendNode{Name: name, NodeType: NodeTypeBackend, Address: name + ":50051", MaxReplicasPerModel: 4}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		fetched, err := registry.GetByName(ctx, name)
		Expect(err).NotTo(HaveOccurred())
		return fetched
	}

	loaded := func(node *BackendNode, name string, age time.Duration, size uint64) {
		Expect(registry.SetNodeModel(ctx, node.ID, name, 0, "loaded", "", 0)).To(Succeed())
		Expect(db.Model(&NodeModel{}).Where("node_id = ? AND model_name = ?", node.ID, name).
			Updates(map[string]any{"last_used": time.Now().Add(-age), "size_bytes": size}).Error).To(Succeed())
	}

	replicasOf := func(name string) int64 {
		var n int64
		Expect(db.Model(&NodeModel{}).Where("model_name = ?", name).Count(&n).Error).To(Succeed())
		return n
	}

	It("places on a node under its model limit without evicting", func() {
		policy.MaxActiveBackends = 1
		full := register("node-a")
		free := register("node-b")
		loaded(full, "other", time.Hour, 0)

		node, err := newRouter().ScheduleAndLoadModel(ctx, model, nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(node.ID).To(Equal(free.ID))
		Expect(replicasOf("other")).To(BeNumerically("==", 1))
	})

	It("evicts the least recently used model when every node is at its limit", func() {
		policy.MaxActiveBackends = 1
		a := register("node-a")
		b := register("node-b")
		loaded(a, "recent", time.Minute, 0)
		loaded(b, "old", time.Hour, 0)

		node, err := newRouter().ScheduleAndLoadModel(ctx, model, nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(node.ID).To(Equal(b.ID))
		Expect(replicasOf("old")).To(BeZero())
		Expect(replicasOf("recent")).To(BeNumerically("==", 1))
	})

	It("refuses with a retryable error and evicts nothing when swap is off", func() {
		policy.MaxActiveBackends = 1
		policy.ModelSwap = false
		a := register("node-a")
		loaded(a, "resident", time.Hour, 0)

		_, err := newRouter().ScheduleAndLoadModel(ctx, model, nil)

		Expect(err).To(MatchError(ErrModelSwapDisabled))
		Expect(err).To(MatchError(ErrNoAvailableNodes))
		Expect(replicasOf("resident")).To(BeNumerically("==", 1))
	})

	It("lets a node override turn swap off for that node only", func() {
		policy.MaxActiveBackends = 1
		a := register("node-a")
		b := register("node-b")
		loaded(a, "on-a", time.Hour, 0)
		loaded(b, "on-b", 2*time.Hour, 0)
		off := false
		Expect(registry.SetNodeLimits(ctx, b.ID, NodeLimits{ModelSwap: &off})).To(Succeed())

		node, err := newRouter().ScheduleAndLoadModel(ctx, model, nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(node.ID).To(Equal(a.ID), "only node-a may swap, even though node-b holds the older model")
		Expect(replicasOf("on-b")).To(BeNumerically("==", 1))
	})

	It("evicts the largest replica first with size-aware eviction", func() {
		policy.MaxActiveBackends = 2
		policy.SizeAwareEviction = true
		a := register("node-a")
		loaded(a, "small-old", time.Hour, 1*gib)
		loaded(a, "big-new", time.Minute, 20*gib)

		_, err := newRouter().ScheduleAndLoadModel(ctx, model, nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(replicasOf("big-new")).To(BeZero())
		Expect(replicasOf("small-old")).To(BeNumerically("==", 1))
	})

	It("evicts a busy replica only with force eviction", func() {
		policy.MaxActiveBackends = 1
		a := register("node-a")
		loaded(a, "busy", time.Hour, 0)
		Expect(registry.IncrementInFlight(ctx, a.ID, "busy", 0)).To(Succeed())

		_, err := newRouter().ScheduleAndLoadModel(ctx, model, nil)
		Expect(err).To(HaveOccurred())
		Expect(replicasOf("busy")).To(BeNumerically("==", 1))

		policy.ForceEvictionWhenBusy = true
		_, err = newRouter().ScheduleAndLoadModel(ctx, model, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(replicasOf("busy")).To(BeZero())
	})

	It("does not evict to fit memory on a node with swap off", func() {
		policy.ModelSwap = false
		estimate = 30 * gib
		node := &BackendNode{Name: "node-a", NodeType: NodeTypeBackend, Address: "node-a:50051", MaxReplicasPerModel: 4, TotalVRAM: 48 * gib, AvailableVRAM: 8 * gib}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		a, err := registry.GetByName(ctx, "node-a")
		Expect(err).NotTo(HaveOccurred())
		loaded(a, "resident", time.Hour, 0)

		_, err = newRouter().ScheduleAndLoadModel(ctx, model, nil)

		Expect(err).To(MatchError(ErrNoAvailableNodes))
		Expect(replicasOf("resident")).To(BeNumerically("==", 1))
	})

	It("records the size estimate on the loaded replica", func() {
		estimate = 2 * gib
		node := &BackendNode{Name: "node-a", NodeType: NodeTypeBackend, Address: "node-a:50051", MaxReplicasPerModel: 4, TotalVRAM: 48 * gib, AvailableVRAM: 48 * gib}
		Expect(registry.Register(ctx, node, true)).To(Succeed())

		_, err := newRouter().ScheduleAndLoadModel(ctx, model, nil)
		Expect(err).NotTo(HaveOccurred())

		var nm NodeModel
		Expect(db.Where("model_name = ?", model).First(&nm).Error).To(Succeed())
		Expect(nm.SizeBytes).To(Equal(2 * gib))
	})
})
