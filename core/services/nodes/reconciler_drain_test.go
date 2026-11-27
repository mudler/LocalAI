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

var _ = Describe("ReplicaReconciler drain", func() {
	var (
		ctx       context.Context
		db        *gorm.DB
		registry  *NodeRegistry
		scheduler *fakeScheduler
		unloader  *fakeUnloader
		rc        *ReplicaReconciler
		n1, n2    *BackendNode
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

		register := func(name string) *BackendNode {
			node := &BackendNode{Name: name, NodeType: NodeTypeBackend, Address: name + ":50051", MaxReplicasPerModel: 4}
			Expect(registry.Register(ctx, node, true)).To(Succeed())
			return node
		}
		n1, n2 = register("drain-n1"), register("drain-n2")
		scheduler = &fakeScheduler{scheduleNode: n2}
		unloader = &fakeUnloader{}
		rc = NewReplicaReconciler(ReplicaReconcilerOptions{
			Registry:  registry,
			Scheduler: scheduler,
			Unloader:  unloader,
		})
	})

	loaded := func(node *BackendNode, model string) {
		Expect(registry.SetNodeModel(ctx, node.ID, model, 0, "loaded", node.Name+":6000", 0)).To(Succeed())
	}
	replicas := func(model string) map[string]bool {
		var rows []NodeModel
		Expect(db.Where("model_name = ? AND state = ?", model, "loaded").Find(&rows).Error).To(Succeed())
		out := map[string]bool{}
		for _, r := range rows {
			out[r.NodeID] = true
		}
		return out
	}
	drain := func(node *BackendNode, timeout time.Duration) {
		_, err := registry.DrainNode(ctx, node.ID, DrainOptions{Timeout: timeout})
		Expect(err).ToNot(HaveOccurred())
	}
	scheduledFor := func(model string) int {
		scheduler.mu.Lock()
		defer scheduler.mu.Unlock()
		n := 0
		for _, c := range scheduler.scheduleCalls {
			if c.modelName == model {
				n++
			}
		}
		return n
	}

	It("places the replacement of a min_replicas model before it removes the draining replica", func() {
		Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "floor", MinReplicas: 1})).To(Succeed())
		loaded(n1, "floor")
		drain(n1, time.Hour)

		rc.reconcile(ctx)
		Expect(scheduledFor("floor")).To(Equal(1), "the draining replica does not hold the floor")
		Expect(replicas("floor")).To(HaveKey(n1.ID), "the draining replica stays until the replacement loads")

		// Another pass while the replacement is not loaded yet changes nothing.
		rc.reconcile(ctx)
		Expect(replicas("floor")).To(HaveKey(n1.ID))

		// The replacement loads on n2.
		loaded(n2, "floor")
		rc.reconcile(ctx)
		Expect(replicas("floor")).To(Equal(map[string]bool{n2.ID: true}))
		Expect(unloader.unloadCalls).To(ContainElement(n1.ID + ":floor"))
	})

	It("holds min_replicas through the drain: the model never has zero replicas", func() {
		Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "floor2", MinReplicas: 2})).To(Succeed())
		n3 := &BackendNode{Name: "drain-n3", NodeType: NodeTypeBackend, Address: "n3:50051", MaxReplicasPerModel: 4}
		Expect(registry.Register(ctx, n3, true)).To(Succeed())
		loaded(n1, "floor2")
		loaded(n2, "floor2")
		drain(n1, time.Hour)

		for range 3 {
			rc.reconcile(ctx)
			Expect(len(replicas("floor2"))).To(Equal(2), "n1 serves until its replacement is up")
		}
		loaded(n3, "floor2")
		rc.reconcile(ctx)
		Expect(replicas("floor2")).To(Equal(map[string]bool{n2.ID: true, n3.ID: true}))
	})

	It("keeps the draining replica while the other replica is on an unhealthy node", func() {
		Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "sick", MinReplicas: 1})).To(Succeed())
		scheduler.scheduleErr = ErrNoAvailableNodes
		loaded(n1, "sick")
		loaded(n2, "sick")
		Expect(registry.MarkUnhealthy(ctx, n2.ID)).To(Succeed())
		drain(n1, time.Hour)

		rc.reconcile(ctx)
		Expect(replicas("sick")).To(HaveKey(n1.ID), "the replica on n2 is not routable, so n1 is the only one that serves")

		// n2 comes back: the replica on n1 may leave.
		Expect(db.Model(&BackendNode{}).Where("id = ?", n2.ID).Update("status", StatusHealthy).Error).To(Succeed())
		rc.reconcile(ctx)
		Expect(replicas("sick")).To(Equal(map[string]bool{n2.ID: true}))
	})

	It("does not remove a replica of a node that resumed during the pass", func() {
		loaded(n1, "resumed-mid-pass")
		drain(n1, time.Hour)
		var nm NodeModel
		Expect(db.Where("node_id = ? AND model_name = ?", n1.ID, "resumed-mid-pass").First(&nm).Error).To(Succeed())
		Expect(registry.ResumeNode(ctx, n1.ID)).To(Succeed())

		removed, err := registry.RemoveDrainedReplica(ctx, nm)
		Expect(err).ToNot(HaveOccurred())
		Expect(removed).To(BeFalse())
		Expect(replicas("resumed-mid-pass")).To(HaveKey(n1.ID))
	})

	It("waits for the requests that run on the draining replica", func() {
		Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "busy", MinReplicas: 1})).To(Succeed())
		loaded(n1, "busy")
		Expect(registry.IncrementInFlight(ctx, n1.ID, "busy", 0)).To(Succeed())
		drain(n1, time.Hour)
		loaded(n2, "busy")

		rc.reconcile(ctx)
		Expect(replicas("busy")).To(HaveKey(n1.ID), "a request still runs on it")

		Expect(registry.DecrementInFlight(ctx, n1.ID, "busy", 0)).To(Succeed())
		rc.reconcile(ctx)
		Expect(replicas("busy")).ToNot(HaveKey(n1.ID))
	})

	It("removes what is left when the deadline passes", func() {
		Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "stuck", MinReplicas: 1})).To(Succeed())
		scheduler.scheduleErr = ErrNoAvailableNodes
		loaded(n1, "stuck")
		Expect(registry.IncrementInFlight(ctx, n1.ID, "stuck", 0)).To(Succeed())
		drain(n1, time.Minute)

		rc.reconcile(ctx)
		Expect(replicas("stuck")).To(HaveKey(n1.ID))

		// The deadline is judged by the database clock, not by the clock of
		// the frontend that runs the pass: a frontend clock that is behind
		// does not hold the replicas past the deadline.
		rc.now = func() time.Time { return time.Now().Add(-time.Hour) }
		Expect(db.Model(&BackendNode{}).Where("id = ?", n1.ID).
			Update("drain_deadline", time.Now().Add(-time.Second)).Error).To(Succeed())
		rc.reconcile(ctx)
		Expect(replicas("stuck")).To(BeEmpty())
		Expect(unloader.unloadCalls).To(ContainElement(n1.ID + ":stuck"))
	})

	It("does not remove the replicas early when the frontend clock is ahead", func() {
		Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "early", MinReplicas: 1})).To(Succeed())
		scheduler.scheduleErr = ErrNoAvailableNodes
		loaded(n1, "early")
		drain(n1, time.Hour)

		rc.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
		rc.reconcile(ctx)
		Expect(replicas("early")).To(HaveKey(n1.ID))
	})

	It("replaces the last replica of a model without a rule that is in use", func() {
		loaded(n1, "used") // SetNodeModel stamps last_used now
		drain(n1, time.Hour)

		rc.reconcile(ctx)
		Expect(scheduledFor("used")).To(Equal(1))
		Expect(replicas("used")).To(HaveKey(n1.ID))

		loaded(n2, "used")
		rc.reconcile(ctx)
		Expect(replicas("used")).To(Equal(map[string]bool{n2.ID: true}))
	})

	It("places the replacement of a model with a selector and no floor on a matching node only", func() {
		Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "sel", NodeSelector: `{"tier":"fast"}`})).To(Succeed())
		Expect(registry.SetNodeLabel(ctx, n1.ID, "tier", "fast")).To(Succeed())
		n3 := &BackendNode{Name: "drain-n3", NodeType: NodeTypeBackend, Address: "n3:50051", MaxReplicasPerModel: 4}
		Expect(registry.Register(ctx, n3, true)).To(Succeed())
		Expect(registry.SetNodeLabel(ctx, n3.ID, "tier", "fast")).To(Succeed())
		loaded(n1, "sel") // in use: SetNodeModel stamps last_used now
		drain(n1, time.Hour)

		rc.reconcile(ctx)
		scheduler.mu.Lock()
		calls := append([]scheduleCall(nil), scheduler.scheduleCalls...)
		scheduler.mu.Unlock()
		Expect(calls).To(HaveLen(1))
		Expect(calls[0].modelName).To(Equal("sel"))
		Expect(calls[0].candidateIDs).To(Equal([]string{n3.ID}), "only n3 matches the selector and is healthy")
	})

	It("places nothing for a model with a selector when no healthy node matches it", func() {
		Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "strict", NodeSelector: `{"tier":"fast"}`})).To(Succeed())
		Expect(registry.SetNodeLabel(ctx, n1.ID, "tier", "fast")).To(Succeed())
		loaded(n1, "strict")
		drain(n1, time.Hour)

		// A replacement on n2 (no label) would never count as staying, so
		// every pass would place one more until the deadline.
		for range 3 {
			rc.reconcile(ctx)
		}
		Expect(scheduledFor("strict")).To(BeZero())
		Expect(replicas("strict")).To(HaveKey(n1.ID), "the draining replica serves until the deadline")
	})

	It("replaces a pinned model even when it is idle", func() {
		rc.pinnedResolver = &fakePinnedResolver{names: []string{"pinned"}}
		loaded(n1, "pinned")
		Expect(db.Model(&NodeModel{}).Where("model_name = ?", "pinned").
			Update("last_used", time.Now().Add(-time.Hour)).Error).To(Succeed())
		drain(n1, time.Hour)

		rc.reconcile(ctx)
		Expect(scheduledFor("pinned")).To(Equal(1))
		Expect(replicas("pinned")).To(HaveKey(n1.ID))
	})

	It("removes an idle replica nobody needs at once", func() {
		loaded(n1, "idle")
		Expect(db.Model(&NodeModel{}).Where("model_name = ?", "idle").
			Update("last_used", time.Now().Add(-time.Hour)).Error).To(Succeed())
		drain(n1, time.Hour)

		rc.reconcile(ctx)
		Expect(scheduledFor("idle")).To(BeZero())
		Expect(replicas("idle")).To(BeEmpty())
	})

	It("does nothing after a resume", func() {
		Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "resumed", MinReplicas: 1})).To(Succeed())
		loaded(n1, "resumed")
		drain(n1, time.Hour)
		Expect(registry.ResumeNode(ctx, n1.ID)).To(Succeed())

		rc.reconcile(ctx)
		Expect(scheduledFor("resumed")).To(BeZero())
		Expect(replicas("resumed")).To(HaveKey(n1.ID))
	})

	It("a forced drain leaves the floor to the reconciler", func() {
		Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "forced", MinReplicas: 1})).To(Succeed())
		loaded(n1, "forced")
		Expect(registry.MarkDraining(ctx, n1.ID)).To(Succeed())
		Expect(replicas("forced")).To(BeEmpty())

		rc.reconcile(ctx)
		Expect(scheduledFor("forced")).To(Equal(1))
	})
})
