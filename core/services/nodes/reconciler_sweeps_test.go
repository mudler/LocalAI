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

// The reconciler applies the idle unload and the memory reclaimer of the
// cluster memory settings to the replicas on the workers.
var _ = Describe("Reconciler memory sweeps", func() {
	const gib = uint64(1) << 30
	var (
		ctx      context.Context
		db       *gorm.DB
		registry *NodeRegistry
		unloader *fakeUnloader
		pinned   *fakePinnedResolver
		policy   MemoryPolicy
		rc       *ReplicaReconciler
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
		policy = DefaultMemoryPolicy()
		registry.SetMemoryPolicy(func() MemoryPolicy { return policy })
		unloader = &fakeUnloader{}
		pinned = &fakePinnedResolver{}
		rc = NewReplicaReconciler(ReplicaReconcilerOptions{Registry: registry, Unloader: unloader, DB: db, PinnedResolver: pinned})
	})

	register := func(name string, total, available uint64) *BackendNode {
		n := &BackendNode{Name: name, NodeType: NodeTypeBackend, Address: name + ":50051", MaxReplicasPerModel: 4, TotalVRAM: total, AvailableVRAM: available}
		Expect(registry.Register(ctx, n, true)).To(Succeed())
		got, err := registry.GetByName(ctx, name)
		Expect(err).ToNot(HaveOccurred())
		return got
	}

	loaded := func(node *BackendNode, name string, replica int, idle time.Duration) {
		Expect(registry.SetNodeModel(ctx, node.ID, name, replica, "loaded", "", 0)).To(Succeed())
		Expect(db.Model(&NodeModel{}).Where("node_id = ? AND model_name = ? AND replica_index = ?", node.ID, name, replica).
			Update("last_used", time.Now().Add(-idle)).Error).To(Succeed())
	}

	count := func(name string) int64 {
		var n int64
		Expect(db.Model(&NodeModel{}).Where("model_name = ?", name).Count(&n).Error).To(Succeed())
		return n
	}

	Describe("idle unload", func() {
		BeforeEach(func() {
			policy.IdleEnabled = true
			policy.IdleTimeout = 10 * time.Minute
		})

		It("unloads idle models with no rule, and keeps recent ones", func() {
			n := register("node-a", 0, 0)
			loaded(n, "stale", 0, time.Hour)
			loaded(n, "fresh", 0, time.Minute)

			rc.sweepIdle(ctx)

			Expect(count("stale")).To(BeZero())
			Expect(count("fresh")).To(BeNumerically("==", 1))
		})

		It("keeps the min_replicas of a rule, pinned models and does nothing when off", func() {
			n := register("node-a", 0, 0)
			m := register("node-b", 0, 0)
			Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "ruled", MinReplicas: 1, MaxReplicas: 2})).To(Succeed())
			loaded(n, "ruled", 0, time.Hour)
			loaded(m, "ruled", 0, time.Hour)
			loaded(n, "pinned", 0, time.Hour)
			pinned.names = []string{"pinned"}

			rc.sweepIdle(ctx)

			Expect(count("ruled")).To(BeNumerically("==", 1))
			Expect(count("pinned")).To(BeNumerically("==", 1))

			policy.IdleEnabled = false
			loaded(n, "later", 0, time.Hour)
			rc.sweepIdle(ctx)
			Expect(count("later")).To(BeNumerically("==", 1))
		})
	})

	Describe("idle unload of an autoscaled model", func() {
		BeforeEach(func() {
			policy.IdleEnabled = true
			policy.IdleTimeout = 10 * time.Minute
		})

		It("keeps the last idle replica, which the scale-up would load again", func() {
			Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "auto", MinReplicas: 1, MaxReplicas: 4})).To(Succeed())
			for i, name := range []string{"node-a", "node-b", "node-c"} {
				n := register(name, 0, 0)
				loaded(n, "auto", 0, time.Hour)
				if i > 0 {
					Expect(registry.IncrementInFlight(ctx, n.ID, "auto", 0)).To(Succeed())
				}
			}

			rc.sweepIdle(ctx)

			Expect(count("auto")).To(BeNumerically("==", 3))
		})

		It("unloads the idle replicas above the one it keeps", func() {
			Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "auto", MinReplicas: 1, MaxReplicas: 4})).To(Succeed())
			for i, name := range []string{"node-a", "node-b", "node-c", "node-d"} {
				n := register(name, 0, 0)
				loaded(n, "auto", 0, time.Hour)
				if i == 0 {
					Expect(registry.IncrementInFlight(ctx, n.ID, "auto", 0)).To(Succeed())
				}
			}

			rc.sweepIdle(ctx)

			Expect(count("auto")).To(BeNumerically("==", 2), "the busy replica and one idle one")
		})

		It("unloads every idle replica of a model with no rule in one pass", func() {
			for _, name := range []string{"node-a", "node-b"} {
				loaded(register(name, 0, 0), "free", 0, time.Hour)
			}

			rc.sweepIdle(ctx)

			Expect(count("free")).To(BeZero())
		})
	})

	Describe("a replica that takes a request during a sweep", func() {
		BeforeEach(func() {
			policy.IdleEnabled = true
			policy.IdleTimeout = 10 * time.Minute
		})

		// takeRequestsOnFirstUnload makes every other replica of model busy
		// (one request in flight) when the sweep unloads its first replica:
		// a request that arrived after the sweep read its candidates.
		takeRequestsOnFirstUnload := func(model string) {
			fired := false
			unloader.unloadHook = func(_, name string) {
				if fired || name != model {
					return
				}
				fired = true
				Expect(db.Model(&NodeModel{}).Where("model_name = ?", model).
					Updates(map[string]any{"in_flight": 1, "last_used": time.Now()}).Error).To(Succeed())
			}
		}

		It("is not unloaded by the idle sweep", func() {
			for _, name := range []string{"node-a", "node-b", "node-c"} {
				loaded(register(name, 0, 0), "shared", 0, time.Hour)
			}
			takeRequestsOnFirstUnload("shared")

			rc.sweepIdle(ctx)

			Expect(count("shared")).To(BeNumerically("==", 2), "the busy replicas stay")
			Expect(unloader.unloadCalls).To(HaveLen(1))
		})

		It("is not unloaded by the memory reclaimer", func() {
			policy.ReclaimerEnabled = true
			policy.ReclaimerThreshold = 0.8
			hot := register("node-hot", 48*gib, 4*gib)
			loaded(hot, "victim", 0, time.Hour)
			cands, err := rc.sweepCandidates(ctx, hot.ID, time.Time{})
			Expect(err).ToNot(HaveOccurred())
			Expect(cands).To(HaveLen(1))
			Expect(registry.IncrementInFlight(ctx, hot.ID, "victim", 0)).To(Succeed())

			Expect(rc.unloadForSweep(ctx, cands[0], "memory threshold")).To(BeFalse())

			Expect(count("victim")).To(BeNumerically("==", 1))
			Expect(unloader.unloadCalls).To(BeEmpty())
		})

		It("is not unloaded when it served a request and went idle again", func() {
			n := register("node-a", 0, 0)
			loaded(n, "recent", 0, time.Hour)
			cands, err := rc.sweepCandidates(ctx, n.ID, time.Now().Add(-policy.IdleTimeout))
			Expect(err).ToNot(HaveOccurred())
			Expect(cands).To(HaveLen(1))
			Expect(db.Model(&NodeModel{}).Where("model_name = ?", "recent").Update("last_used", time.Now()).Error).To(Succeed())

			Expect(rc.unloadForSweep(ctx, cands[0], "idle timeout")).To(BeFalse())

			Expect(count("recent")).To(BeNumerically("==", 1))
		})

		It("is not removed by the idle scale-down of a rule", func() {
			cfg := ModelSchedulingConfig{ModelName: "scaled", MinReplicas: 1, MaxReplicas: 4}
			Expect(registry.SetModelScheduling(ctx, &cfg)).To(Succeed())
			for _, name := range []string{"node-a", "node-b", "node-c"} {
				loaded(register(name, 0, 0), "scaled", 0, time.Hour)
			}
			takeRequestsOnFirstUnload("scaled")

			rc.scaleDownIdle(ctx, cfg, 3, 1)

			Expect(count("scaled")).To(BeNumerically("==", 2), "only the first replica was idle when removed")
		})
	})

	Describe("memory reclaimer", func() {
		It("unloads one idle replica on a node above its threshold", func() {
			policy.ReclaimerEnabled = true
			policy.ReclaimerThreshold = 0.8
			hot := register("node-hot", 48*gib, 4*gib)
			cool := register("node-cool", 48*gib, 40*gib)
			loaded(hot, "older", 0, time.Hour)
			loaded(hot, "newer", 0, time.Minute)
			loaded(cool, "kept", 0, time.Hour)

			rc.sweepMemoryPressure(ctx)

			Expect(count("older")).To(BeZero())
			Expect(count("newer")).To(BeNumerically("==", 1))
			Expect(count("kept")).To(BeNumerically("==", 1))
		})

		It("reclaims on a node that sets its own threshold while the cluster leaves the reclaimer off", func() {
			policy.ReclaimerEnabled = false
			policy.ReclaimerStated = true
			hot := register("node-hot", 48*gib, 4*gib)
			Expect(registry.SetNodeLimits(ctx, hot.ID, NodeLimits{MemoryReclaimerThreshold: ptr(0.8)})).To(Succeed())
			other := register("node-other", 48*gib, 4*gib)
			loaded(hot, "on-hot", 0, time.Hour)
			loaded(other, "on-other", 0, time.Hour)

			rc.sweepMemoryPressure(ctx)

			Expect(count("on-hot")).To(BeZero())
			Expect(count("on-other")).To(BeNumerically("==", 1), "the cluster reclaimer is off")
		})

		It("reclaims on a unified-memory node at 0.90 when the cluster names no reclaimer", func() {
			n := &BackendNode{Name: "spark", NodeType: NodeTypeBackend, Address: "spark:50051", MaxReplicasPerModel: 4,
				TotalVRAM: 100 * gib, AvailableVRAM: 5 * gib, UnifiedMemory: true}
			Expect(registry.Register(ctx, n, true)).To(Succeed())
			spark, err := registry.GetByName(ctx, "spark")
			Expect(err).ToNot(HaveOccurred())
			Expect(spark.UnifiedMemory).To(BeTrue())
			loaded(spark, "big", 0, time.Hour)

			rc.sweepMemoryPressure(ctx)

			Expect(count("big")).To(BeZero())
		})
	})
})

var _ = Describe("Reclaimer headroom in placement", func() {
	const gb = uint64(1000 * 1000 * 1000)
	var (
		ctx      context.Context
		registry *NodeRegistry
		policy   MemoryPolicy
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		ctx = context.Background()
		var err error
		registry, err = NewNodeRegistry(testutil.SetupTestDB())
		Expect(err).ToNot(HaveOccurred())
		policy = DefaultMemoryPolicy()
		registry.SetMemoryPolicy(func() MemoryPolicy { return policy })
	})

	It("keeps (1 - threshold) of the node free when the reclaimer is on", func() {
		n := &BackendNode{Name: "node-a", NodeType: NodeTypeBackend, Address: "node-a:50051", TotalVRAM: 20 * gb, AvailableVRAM: 20 * gb}
		Expect(registry.Register(ctx, n, true)).To(Succeed())
		avail := 10 * gb

		Expect(registry.Heartbeat(ctx, n.ID, &HeartbeatUpdate{AvailableVRAM: &avail})).To(Succeed())
		_, err := registry.FindNodeWithVRAM(ctx, 9*gb)
		Expect(err).ToNot(HaveOccurred(), "reclaimer off: 10 GB free holds 9 GB")

		policy.ReclaimerEnabled = true
		policy.ReclaimerThreshold = 0.9
		Expect(registry.Heartbeat(ctx, n.ID, &HeartbeatUpdate{AvailableVRAM: &avail})).To(Succeed())
		_, err = registry.FindNodeWithVRAM(ctx, 9*gb)
		Expect(err).To(HaveOccurred(), "2 GB of headroom leaves 8 GB")
		_, err = registry.FindNodeWithVRAM(ctx, 7*gb)
		Expect(err).ToNot(HaveOccurred())
	})
})
