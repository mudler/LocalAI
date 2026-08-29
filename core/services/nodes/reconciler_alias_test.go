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

var _ = Describe("ReplicaReconciler with alias-keyed rules", func() {
	var (
		db       *gorm.DB
		registry *NodeRegistry
		resolver *fakeAliasResolver
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		resolver = &fakeAliasResolver{aliases: map[string]string{"production": "qwen3"}}
		registry.SetAliasResolver(resolver)
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

	setRule := func(cfg *ModelSchedulingConfig) ModelSchedulingConfig {
		ExpectWithOffset(1, registry.SetModelScheduling(context.Background(), cfg)).To(Succeed())
		return mustGetSched(registry, cfg.ModelName)
	}

	It("loads the model the alias points at, not the alias itself", func() {
		node := registerNode("alias-n1", "10.9.0.1:50051")
		rule := setRule(&ModelSchedulingConfig{ModelName: "production", MinReplicas: 1, MaxReplicas: 2})

		scheduler := &fakeScheduler{scheduleNode: node}
		reconciler := NewReplicaReconciler(ReplicaReconcilerOptions{Registry: registry, Scheduler: scheduler, DB: db})

		reconciler.reconcileModel(context.Background(), rule)

		Expect(scheduler.scheduleCalls).To(HaveLen(1))
		Expect(scheduler.scheduleCalls[0].modelName).To(Equal("qwen3"))
	})

	It("counts the target's replicas when deciding whether the floor is met", func() {
		node := registerNode("alias-n2", "10.9.0.2:50051")
		Expect(registry.SetNodeModel(context.Background(), node.ID, "qwen3", 0, "loaded", "", 0)).To(Succeed())
		rule := setRule(&ModelSchedulingConfig{ModelName: "production", MinReplicas: 1, MaxReplicas: 2})

		scheduler := &fakeScheduler{scheduleNode: node}
		reconciler := NewReplicaReconciler(ReplicaReconcilerOptions{Registry: registry, Scheduler: scheduler, DB: db})

		reconciler.reconcileModel(context.Background(), rule)

		// The floor is already met by the target's replica. Counting against
		// the alias name instead would see zero and load a redundant replica.
		Expect(scheduler.scheduleCalls).To(BeEmpty())
	})

	It("scales down idle replicas of the target", func() {
		n1 := registerNode("alias-n3", "10.9.0.3:50051")
		n2 := registerNode("alias-n4", "10.9.0.4:50051")
		past := time.Now().Add(-10 * time.Minute)
		for _, n := range []*BackendNode{n1, n2} {
			Expect(registry.SetNodeModel(context.Background(), n.ID, "qwen3", 0, "loaded", "", 0)).To(Succeed())
			db.Model(&NodeModel{}).Where("node_id = ? AND model_name = ?", n.ID, "qwen3").Update("last_used", past)
		}
		rule := setRule(&ModelSchedulingConfig{ModelName: "production", MinReplicas: 1, MaxReplicas: 4})

		unloader := &fakeUnloader{}
		reconciler := NewReplicaReconciler(ReplicaReconcilerOptions{
			Registry: registry, Unloader: unloader, DB: db, ScaleDownDelay: time.Minute,
		})

		reconciler.reconcileModel(context.Background(), rule)

		remaining, err := registry.CountLoadedReplicas(context.Background(), "qwen3")
		Expect(err).ToNot(HaveOccurred())
		Expect(remaining).To(BeNumerically("==", 1))
	})

	It("skips a rule whose alias no longer resolves", func() {
		registerNode("alias-n5", "10.9.0.5:50051")
		resolver.aliases["orphan"] = "orphan" // target removed: resolves to itself
		rule := setRule(&ModelSchedulingConfig{ModelName: "orphan", MinReplicas: 1})

		scheduler := &fakeScheduler{}
		reconciler := NewReplicaReconciler(ReplicaReconcilerOptions{Registry: registry, Scheduler: scheduler, DB: db})

		reconciler.reconcileModel(context.Background(), rule)

		// Loading the alias name would ask a worker to start a pure redirect
		// that has no backend and no model file behind it.
		Expect(scheduler.scheduleCalls).To(BeEmpty())
	})

	It("records unsatisfiable capacity against the rule, not the target", func() {
		registerNode("alias-n6", "10.9.0.6:50051")
		rule := setRule(&ModelSchedulingConfig{ModelName: "production", MinReplicas: 1, NodeSelector: `{"tier":"absent"}`})

		reconciler := NewReplicaReconciler(ReplicaReconcilerOptions{Registry: registry, Scheduler: &fakeScheduler{}, DB: db})
		for i := 0; i < unsatisfiableTickThreshold; i++ {
			reconciler.reconcileModel(context.Background(), rule)
		}

		stored, err := registry.GetModelScheduling(context.Background(), "production")
		Expect(err).ToNot(HaveOccurred())
		Expect(stored.UnsatisfiableUntil).ToNot(BeNil())

		// The bookkeeping belongs to the rule row; the target has no rule.
		targetRule, err := registry.GetModelScheduling(context.Background(), "qwen3")
		Expect(err).ToNot(HaveOccurred())
		Expect(targetRule).To(BeNil())
	})
})
