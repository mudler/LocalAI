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

	It("counts the cold load of another model on the node against its limit", func() {
		policy.MaxActiveBackends = 2
		a := register("node-a")
		loaded(a, "resident", time.Hour, 0)
		// Another frontend claimed a load of a second model on the node. It
		// has no replica row until staging, but it takes a model slot.
		_, claimed, err := registry.ClaimLoadJobOn(ctx, "other-cold", a.ID, "other-frontend")
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(BeTrue())

		_, err = newRouter().ScheduleAndLoadModel(ctx, model, nil)

		if err != nil {
			Expect(err).To(MatchError(ErrNoAvailableNodes))
			Expect(replicasOf(model)).To(BeZero())
		} else {
			Expect(replicasOf("resident")).To(BeZero(), "the node holds the resident, the cold load and the new model")
		}
	})

	It("keeps two concurrent cold loads within the limit of a node", func() {
		policy.MaxActiveBackends = 2
		a := register("node-a")
		loaded(a, "resident", time.Hour, 0)
		for _, m := range []string{"cold-1", "cold-2"} {
			blob, err := proto.Marshal(&pb.ModelOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(registry.UpsertModelLoadInfo(ctx, m, "llama-cpp", blob)).To(Succeed())
		}
		r := newRouter()

		// Both loads are placed and claimed before either makes room, as on
		// two frontends that pick the node at the same time.
		var placed []*replicaPlacement
		for _, m := range []string{"cold-1", "cold-2"} {
			p, err := r.placeNewReplica(ctx, m, &pb.ModelOptions{}, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(p.node.ID).To(Equal(a.ID))
			_, claimed, err := registry.ClaimLoadJobOn(ctx, m, p.node.ID, "frontend-"+m)
			Expect(err).NotTo(HaveOccurred())
			Expect(claimed).To(BeTrue())
			placed = append(placed, p)
		}
		proceeded := 0
		for i, m := range []string{"cold-1", "cold-2"} {
			if err := r.makeRoom(ctx, m, placed[i]); err != nil {
				Expect(err).To(MatchError(ErrNoAvailableNodes))
				continue
			}
			proceeded++
		}

		Expect(replicasOf("resident")+int64(proceeded)).To(BeNumerically("<=", 2),
			"resident %d, loads that proceeded %d", replicasOf("resident"), proceeded)
	})

	It("checks the limit again when a planned memory eviction is skipped", func() {
		policy.MaxActiveBackends = 2
		estimate = 1 * gib
		node := &BackendNode{Name: "node-a", NodeType: NodeTypeBackend, Address: "node-a:50051", MaxReplicasPerModel: 4, TotalVRAM: 48 * gib, AvailableVRAM: 40 * gib}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		a, err := registry.GetByName(ctx, "node-a")
		Expect(err).NotTo(HaveOccurred())
		loaded(a, "old", time.Hour, 0)
		loaded(a, "new", time.Minute, 0)
		var old NodeModel
		Expect(db.Where("model_name = ?", "old").First(&old).Error).To(Succeed())
		// The plan said to evict "old" for memory, but the node has the
		// memory now, so that eviction is skipped.
		p := &replicaPlacement{node: a, estimatedVRAM: estimate, room: roomPlan{memory: []NodeModel{old}}}

		Expect(newRouter().makeRoom(ctx, model, p)).To(Succeed())

		var n int64
		Expect(db.Model(&NodeModel{}).Where("node_id = ?", a.ID).Count(&n).Error).To(Succeed())
		Expect(n).To(BeNumerically("<=", 1), "room for the new model within the limit of 2")
	})

	It("bounds the wait for a busy replica in placement when the request has no deadline", func() {
		policy.MaxActiveBackends = 1
		policy.LRUMaxRetries = 30
		policy.LRURetryInterval = 200 * time.Millisecond // 6s with no bound
		a := register("node-a")
		loaded(a, "busy", time.Hour, 0)
		Expect(registry.IncrementInFlight(ctx, a.ID, "busy", 0)).To(Succeed())

		start := time.Now()
		_, err := newRouter().ScheduleAndLoadModel(context.Background(), model, nil)

		Expect(err).To(MatchError(ErrNoAvailableNodes))
		Expect(time.Since(start)).To(BeNumerically("<", maxPlacementEvictionWait+time.Second))
	})

	It("bounds the wait for a busy replica in placement by the request deadline", func() {
		policy.MaxActiveBackends = 1
		policy.LRUMaxRetries = 30
		policy.LRURetryInterval = 200 * time.Millisecond
		a := register("node-a")
		loaded(a, "busy", time.Hour, 0)
		Expect(registry.IncrementInFlight(ctx, a.ID, "busy", 0)).To(Succeed())
		reqCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		start := time.Now()
		_, err := newRouter().ScheduleAndLoadModel(reqCtx, model, nil)

		Expect(err).To(HaveOccurred())
		Expect(time.Since(start)).To(BeNumerically("<", 2*time.Second))
	})

	It("counts no capacity for the reconciler on a node at its model limit with swap off", func() {
		policy.MaxActiveBackends = 1
		policy.ModelSwap = false
		full := register("node-a")
		loaded(full, "other", time.Hour, 0)

		capacity, err := registry.ClusterCapacityForModel(ctx, model, nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(capacity).To(BeZero(), "the node may not unload other for a new replica")
	})

	It("caps the capacity of a node at its model limit", func() {
		policy.MaxActiveBackends = 2
		register("node-a") // empty, 4 replica slots per model
		b := register("node-b")
		loaded(b, model, time.Hour, 0)

		capacity, err := registry.ClusterCapacityForModel(ctx, model, nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(capacity).To(Equal(2+1), "two models on node-a, one more replica on node-b")
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
