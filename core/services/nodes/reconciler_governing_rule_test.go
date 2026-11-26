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
