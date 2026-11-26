package nodes

import (
	"context"
	"errors"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// The reconciler picks the nodes a replica may go to from its rule's selector
// and passes them to ScheduleAndLoadModel. If the router ignored them, a
// replica could land on a node the reconciler never meant to use.
var _ = Describe("ScheduleAndLoadModel candidate nodes", func() {
	const modelName = "candidate-model"
	var (
		ctx      context.Context
		db       *gorm.DB
		registry *NodeRegistry
		unloader *fakeUnloader
		router   *SmartRouter
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
		Expect(registry.EstablishModelConfigRevision(ctx, modelName, "rev-1")).To(Succeed())
		Expect(registry.UpsertModelLoadInfoRevision(ctx, modelName, "llama-cpp", "rev-1", blob)).To(Succeed())
		unloader = &fakeUnloader{installReply: &workerctl.BackendInstallReply{Success: true, Address: "10.0.0.1:9001"}}
		backend := &stubBackend{healthResult: true, loadResult: &pb.Result{Success: true}}
		router = NewSmartRouter(registry, SmartRouterOptions{Unloader: unloader, ClientFactory: &stubClientFactory{client: backend}})
	})

	register := func(name string, labels map[string]string) *BackendNode {
		node := &BackendNode{Name: name, NodeType: NodeTypeBackend, Address: name + ":50051", MaxReplicasPerModel: 4}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		fetched, err := registry.GetByName(ctx, name)
		Expect(err).NotTo(HaveOccurred())
		for k, v := range labels {
			Expect(registry.SetNodeLabel(ctx, fetched.ID, k, v)).To(Succeed())
		}
		return fetched
	}

	installedOn := func() []string {
		unloader.mu.Lock()
		defer unloader.mu.Unlock()
		ids := make([]string, 0, len(unloader.installCalls))
		for _, c := range unloader.installCalls {
			ids = append(ids, c.nodeID)
		}
		return ids
	}

	It("places the replica on a candidate node", func() {
		// The idle node is the scheduler's first choice when nothing narrows
		// the pool, so only honouring the candidates places it on the busy one.
		register("idle-node", nil)
		busy := register("busy-node", nil)
		Expect(registry.SetNodeModel(ctx, busy.ID, "other-model", 0, "loaded", "", 0)).To(Succeed())

		node, err := router.ScheduleAndLoadModel(ctx, modelName, []string{busy.ID})

		Expect(err).NotTo(HaveOccurred())
		Expect(node.ID).To(Equal(busy.ID))
		Expect(installedOn()).To(ConsistOf(busy.ID))
	})

	It("reports a load that another owner holds as in progress, not as a failure", func() {
		only := register("node-a", nil)
		_, claimed, err := registry.ClaimLoadJobOn(ctx, modelName, only.ID, "another-replica")
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(BeTrue())

		_, err = router.ScheduleAndLoadModel(ctx, modelName, nil)

		Expect(err).To(MatchError(ErrLoadInProgress))
		Expect(installedOn()).To(BeEmpty())
	})

	It("fails rather than leave the candidates when none matches the model's rule", func() {
		gpu := register("gpu-node", map[string]string{"tier": "gpu"})
		cpu := register("cpu-node", map[string]string{"tier": "cpu"})
		Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: modelName, NodeSelector: `{"tier":"gpu"}`})).To(Succeed())

		_, err := router.ScheduleAndLoadModel(ctx, modelName, []string{cpu.ID})

		Expect(err).To(MatchError(ErrNoAvailableNodes))
		Expect(installedOn()).NotTo(ContainElement(gpu.ID))
		Expect(installedOn()).To(BeEmpty())
	})
})

// The request path reads the model's rule before it routes. A rule it cannot
// read is not "no rule": treating it as one placed the model on any node.
var _ = Describe("Route when the governing rule cannot be read", func() {
	It("answers with a retryable error and places nothing", func() {
		reg := &fakeModelRouter{findAndLockErr: errors.New("not found")}
		reg.getModelSchedErr = errors.New("canceling statement due to statement timeout")
		reg.findIdleNode = &BackendNode{ID: "node-b", Name: "node-b", Address: "node-b:50051"}
		unloader := &fakeUnloader{installReply: &workerctl.BackendInstallReply{Success: true, Address: "127.0.0.1:9001"}}
		backend := &stubBackend{healthResult: true, loadResult: &pb.Result{Success: true}}
		router := NewSmartRouter(reg, SmartRouterOptions{Unloader: unloader, ClientFactory: &stubClientFactory{client: backend}})

		_, err := router.Route(context.Background(), "model-x", "model-x.gguf", "llama-cpp", "", &pb.ModelOptions{}, false)

		Expect(err).To(MatchError(ErrNoAvailableNodes))
		Expect(err).To(MatchError(ErrSchedulingRuleUnavailable))
		Expect(unloader.installCalls).To(BeEmpty())
		Expect(reg.findAndLockCandidates).To(BeEmpty(), "no replica is chosen without the rule either")
	})
})
