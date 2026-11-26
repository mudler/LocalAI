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

// The loads of one scale-up run in parallel, but their nodes are chosen one
// after the other. Chosen from one snapshot, every load picked the same node:
// one claimed it and the others met ErrLoadInProgress, so a floor of three
// took three ticks to fill.
var _ = Describe("ReplicaReconciler scale-up placement", func() {
	var (
		ctx      context.Context
		db       *gorm.DB
		registry *NodeRegistry
		unloader *fakeUnloader
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		ctx = context.Background()
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		unloader = &fakeUnloader{installReply: &workerctl.BackendInstallReply{Success: true, Address: "127.0.0.1:9001"}}
		blob, err := proto.Marshal(&pb.ModelOptions{})
		Expect(err).ToNot(HaveOccurred())
		Expect(registry.UpsertModelLoadInfo(ctx, "model-x", "llama-cpp", blob)).To(Succeed())
	})

	It("loads a floor of three on three distinct nodes in one tick", func() {
		ids := []string{}
		for _, name := range []string{"spread-1", "spread-2", "spread-3"} {
			node := &BackendNode{Name: name, NodeType: NodeTypeBackend, Address: name + ":50051", MaxReplicasPerModel: 1}
			Expect(registry.Register(ctx, node, true)).To(Succeed())
			fetched, err := registry.GetByName(ctx, name)
			Expect(err).ToNot(HaveOccurred())
			ids = append(ids, fetched.ID)
		}
		Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "model-x", MinReplicas: 3})).To(Succeed())

		backend := &stubBackend{healthResult: true, loadResult: &pb.Result{Success: true}}
		router := NewSmartRouter(registry, SmartRouterOptions{Unloader: unloader, ClientFactory: &stubClientFactory{client: backend}, DB: db})
		rc := NewReplicaReconciler(ReplicaReconcilerOptions{Registry: registry, Scheduler: router, Unloader: unloader, DB: db})

		Expect(rc.scaleUp(ctx, mustGetSched(registry, "model-x"), 3)).To(BeTrue())

		unloader.mu.Lock()
		installed := []string{}
		for _, c := range unloader.installCalls {
			installed = append(installed, c.nodeID)
		}
		unloader.mu.Unlock()
		Expect(installed).To(ConsistOf(ids))

		var loaded []NodeModel
		Expect(db.Where("model_name = ? AND state = ?", "model-x", "loaded").Find(&loaded).Error).To(Succeed())
		nodesLoaded := []string{}
		for _, nm := range loaded {
			nodesLoaded = append(nodesLoaded, nm.NodeID)
		}
		Expect(nodesLoaded).To(ConsistOf(ids))
	})
})
