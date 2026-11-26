package nodes

import (
	"context"
	"errors"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

// The router places a model by the one rule that governs it (see
// GetGoverningScheduling). The reconciler must scale it by that same rule and
// see its replicas through that rule's selector, or the two disagree on where
// the model runs: the reconciler fills the floor of a rule the router ignores,
// or counts a replica on a node the rule excludes as if it met the floor.
var _ = Describe("ReplicaReconciler and the governing rule", func() {
	var (
		ctx      context.Context
		db       *gorm.DB
		registry *NodeRegistry
		resolver *fakeAliasResolver
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
		resolver = &fakeAliasResolver{aliases: map[string]string{"production": "qwen3"}}
		registry.SetAliasResolver(resolver)
	})

	register := func(name string, labels map[string]string) *BackendNode {
		node := &BackendNode{Name: name, NodeType: NodeTypeBackend, Address: name + ":50051", MaxReplicasPerModel: 4}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		fetched, err := registry.GetByName(ctx, name)
		Expect(err).ToNot(HaveOccurred())
		for k, v := range labels {
			Expect(registry.SetNodeLabel(ctx, fetched.ID, k, v)).To(Succeed())
		}
		return fetched
	}

	setRule := func(cfg *ModelSchedulingConfig) {
		ExpectWithOffset(1, registry.SetModelScheduling(ctx, cfg)).To(Succeed())
	}

	Describe("listing the rules to scale", func() {
		It("skips an alias rule that the model's own rule overrides", func() {
			setRule(&ModelSchedulingConfig{ModelName: "production", MinReplicas: 2, NodeSelector: `{"tier":"gpu"}`})
			setRule(&ModelSchedulingConfig{ModelName: "qwen3", MinReplicas: 1, NodeSelector: `{"tier":"cpu"}`})

			configs, err := registry.ListAutoScalingConfigs(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(configs).To(HaveLen(1))
			Expect(configs[0].ModelName).To(Equal("qwen3"))
		})

		It("skips an overridden alias rule even when the governing rule does not scale", func() {
			// The model's own rule only pins placement. It still governs, so
			// the alias rule's floor must not apply on the alias's nodes.
			setRule(&ModelSchedulingConfig{ModelName: "production", MinReplicas: 2, NodeSelector: `{"tier":"gpu"}`})
			setRule(&ModelSchedulingConfig{ModelName: "qwen3", NodeSelector: `{"tier":"cpu"}`})

			configs, err := registry.ListAutoScalingConfigs(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(configs).To(BeEmpty())
		})

		It("keeps an alias rule that governs its model", func() {
			setRule(&ModelSchedulingConfig{ModelName: "production", MinReplicas: 2})

			configs, err := registry.ListAutoScalingConfigs(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(configs).To(HaveLen(1))
			Expect(configs[0].ModelName).To(Equal("production"))
		})
	})

	Describe("counting replicas against the floor", func() {
		It("does not count a replica on a node the selector excludes", func() {
			gpu := register("gov-gpu", map[string]string{"tier": "gpu"})
			cpu := register("gov-cpu", map[string]string{"tier": "cpu"})
			Expect(registry.SetNodeModel(ctx, cpu.ID, "qwen3", 0, "loaded", "", 0)).To(Succeed())
			setRule(&ModelSchedulingConfig{ModelName: "qwen3", MinReplicas: 1, MaxReplicas: 2, NodeSelector: `{"tier":"gpu"}`})

			scheduler := &fakeScheduler{scheduleNode: gpu}
			rc := NewReplicaReconciler(ReplicaReconcilerOptions{Registry: registry, Scheduler: scheduler, DB: db})
			rc.reconcileModel(ctx, mustGetSched(registry, "qwen3"))

			Expect(scheduler.scheduleCalls).To(HaveLen(1), "the floor is not met on the nodes the rule selects")
			Expect(scheduler.scheduleCalls[0].candidateIDs).To(ConsistOf(gpu.ID))
		})

		It("does not trim a selected replica because of one on an excluded node", func() {
			gpu := register("gov-gpu", map[string]string{"tier": "gpu"})
			cpu := register("gov-cpu", map[string]string{"tier": "cpu"})
			past := time.Now().Add(-10 * time.Minute)
			// The selected replica has the higher index, so it is the first
			// one an unscoped idle scale-down would remove.
			Expect(registry.SetNodeModel(ctx, cpu.ID, "qwen3", 0, "loaded", "", 0)).To(Succeed())
			Expect(registry.SetNodeModel(ctx, gpu.ID, "qwen3", 1, "loaded", "", 0)).To(Succeed())
			Expect(db.Model(&NodeModel{}).Where("model_name = ?", "qwen3").Update("last_used", past).Error).To(Succeed())
			setRule(&ModelSchedulingConfig{ModelName: "qwen3", MinReplicas: 1, MaxReplicas: 4, NodeSelector: `{"tier":"gpu"}`})

			unloader := &fakeUnloader{}
			rc := NewReplicaReconciler(ReplicaReconcilerOptions{
				Registry: registry, Scheduler: &fakeScheduler{}, Unloader: unloader, DB: db, ScaleDownDelay: time.Minute,
			})
			rc.reconcileModel(ctx, mustGetSched(registry, "qwen3"))

			var onGPU int64
			Expect(db.Model(&NodeModel{}).Where("model_name = ? AND node_id = ?", "qwen3", gpu.ID).Count(&onGPU).Error).To(Succeed())
			Expect(onGPU).To(BeNumerically("==", 1), "the only replica on a selected node is the floor")
		})
	})
})

// ruleReadFailingRegistry is a registry whose scheduling rule cannot be read,
// as during a database statement timeout. Everything else works.
type ruleReadFailingRegistry struct {
	*NodeRegistry
}

func (r ruleReadFailingRegistry) GetGoverningScheduling(context.Context, string) (*ModelSchedulingConfig, error) {
	return nil, errors.New("canceling statement due to statement timeout")
}

// A load, from a request or from the reconciler, must only land on a node that
// matches the rule that governs the model. When the rule cannot be read, or
// this frontend resolves the rule's alias from an outdated config, the router
// used to place the model on any healthy node.
var _ = Describe("Strict placement by the governing rule", func() {
	var (
		ctx      context.Context
		db       *gorm.DB
		registry *NodeRegistry
		unloader *fakeUnloader
		gpu, cpu *BackendNode
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

		register := func(name string, labels map[string]string) *BackendNode {
			node := &BackendNode{Name: name, NodeType: NodeTypeBackend, Address: name + ":50051", MaxReplicasPerModel: 4}
			Expect(registry.Register(ctx, node, true)).To(Succeed())
			fetched, err := registry.GetByName(ctx, name)
			Expect(err).ToNot(HaveOccurred())
			for k, v := range labels {
				Expect(registry.SetNodeLabel(ctx, fetched.ID, k, v)).To(Succeed())
			}
			return fetched
		}
		// The matching node is busy and the other one is idle, so the idle-first
		// scheduler picks the wrong node unless the rule narrows the pool.
		gpu = register("node-a", map[string]string{"gpu.vendor": "nvidia"})
		cpu = register("node-b", map[string]string{"gpu.vendor": "amd"})
		Expect(registry.SetNodeModel(ctx, gpu.ID, "other-model", 0, "loaded", "", 0)).To(Succeed())

		blob, err := proto.Marshal(&pb.ModelOptions{})
		Expect(err).ToNot(HaveOccurred())
		Expect(registry.UpsertModelLoadInfo(ctx, "model-x", "llama-cpp", blob)).To(Succeed())
	})

	newRouter := func(reg ModelRouter) *SmartRouter {
		backend := &stubBackend{healthResult: true, loadResult: &pb.Result{Success: true}}
		return NewSmartRouter(reg, SmartRouterOptions{Unloader: unloader, ClientFactory: &stubClientFactory{client: backend}, DB: db})
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

	It("fails with a retryable error when the rule cannot be read", func() {
		Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "model-x", MinReplicas: 1, NodeSelector: `{"gpu.vendor":"nvidia"}`})).To(Succeed())
		router := newRouter(ruleReadFailingRegistry{registry})

		_, err := router.ScheduleAndLoadModel(ctx, "model-x", nil)

		Expect(err).To(MatchError(ErrNoAvailableNodes), "the HTTP layer answers 503 for it")
		Expect(err).To(MatchError(ErrSchedulingRuleUnavailable))
		Expect(installedOn()).To(BeEmpty())
	})

	It("places by the stored target when this frontend's alias is outdated", func() {
		// A current frontend wrote the rule for alias "production", which now
		// points at model-x. This frontend still resolves the alias to the
		// model it used to point at.
		current, err := NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		current.SetAliasResolver(&revisionAliasResolver{
			fakeAliasResolver: fakeAliasResolver{aliases: map[string]string{"production": "model-x"}},
			revisions:         map[string]string{"production": "rev-new"},
		})
		_, err = current.AdvanceModelConfigRevision(ctx, "production", "rev-new")
		Expect(err).ToNot(HaveOccurred())
		Expect(current.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "production", MinReplicas: 2, NodeSelector: `{"gpu.vendor":"nvidia"}`})).To(Succeed())

		registry.SetAliasResolver(&revisionAliasResolver{
			fakeAliasResolver: fakeAliasResolver{aliases: map[string]string{"production": "model-old"}},
			revisions:         map[string]string{"production": "rev-old"},
		})

		rule, err := registry.GetGoverningScheduling(ctx, "model-x")
		Expect(err).ToNot(HaveOccurred())
		Expect(rule).ToNot(BeNil(), "the outdated frontend finds the rule through the stored target")
		Expect(rule.ModelName).To(Equal("production"))

		node, err := newRouter(registry).ScheduleAndLoadModel(ctx, "model-x", nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(node.ID).To(Equal(gpu.ID))
		Expect(installedOn()).To(ConsistOf(gpu.ID))
		Expect(installedOn()).ToNot(ContainElement(cpu.ID))
	})

	It("fails with a retryable error when no matching node is healthy", func() {
		Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "model-x", MinReplicas: 1, NodeSelector: `{"gpu.vendor":"nvidia"}`})).To(Succeed())
		Expect(registry.MarkDraining(ctx, gpu.ID)).To(Succeed())

		_, err := newRouter(registry).ScheduleAndLoadModel(ctx, "model-x", nil)

		Expect(err).To(MatchError(ErrNoAvailableNodes))
		Expect(installedOn()).To(BeEmpty(), "never falls back to a node the rule excludes")
	})

	It("does not treat a selector it cannot parse as no selector", func() {
		Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "model-x", NodeSelector: `{not json`})).To(Succeed())

		_, err := newRouter(registry).ScheduleAndLoadModel(ctx, "model-x", nil)

		Expect(err).To(MatchError(ErrNoAvailableNodes))
		Expect(installedOn()).To(BeEmpty())
	})
})
