package nodes

import (
	"context"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/testutil"
)

// A replica row in loading or staging holds its slot: NextFreeReplicaIndex
// counts every state except unloading. Nothing reclaimed such a row. Every
// reconciler sweep and the router's eviction query filter state = "loaded", and
// the per-model health probe skips rows with no address, which is exactly what a
// row that never finished loading has. So a worker that dropped out mid-transfer
// left a row that pinned the only replica slot on that node for that model, and
// the next request failed with "no replica slot ... all models busy".
//
// Elapsed time alone cannot decide this: staging a large checkpoint legitimately
// runs for tens of minutes. The load job's lease is the
// discriminator: a row names its attempt, and the attempt is alive while its job is.
var _ = Describe("ReplicaReconciler — abandoned load sweeper", func() {
	var (
		db       *gorm.DB
		registry *NodeRegistry
		node     *BackendNode
		rc       *ReplicaReconciler
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		node = &BackendNode{Name: "n1", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051"}
		Expect(registry.Register(context.Background(), node, true)).To(Succeed())
		rc = NewReplicaReconciler(ReplicaReconcilerOptions{Registry: registry, DB: db})
	})

	// seedReplica creates a replica row in the given state, aged so it is past
	// the sweeper's grace period unless stated otherwise.
	seedReplica := func(model, state string, age time.Duration) {
		Expect(db.Create(&NodeModel{
			ID:        model + "-row",
			NodeID:    node.ID,
			ModelName: model,
			State:     state,
			UpdatedAt: time.Now().Add(-age),
		}).Error).To(Succeed())
	}

	// seedJob writes a job row directly. leaseSecs and stopSecs are offsets from
	// the database clock; a nil stop deadline means the job is still running.
	seedJob := func(model, state, generation string, leaseSecs int, stopSecs *int) {
		Expect(db.Create(&ModelLoadJob{
			TrackingKey:  model,
			Generation:   generation,
			State:        state,
			OwnerReplica: "someone",
			LastProgress: time.Now(),
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}).Error).To(Succeed())
		Expect(db.Exec("UPDATE model_load_jobs SET lease_until = now() + make_interval(secs => ?) WHERE tracking_key = ?", leaseSecs, model).Error).To(Succeed())
		if stopSecs != nil {
			Expect(db.Exec("UPDATE model_load_jobs SET stop_deadline = now() + make_interval(secs => ?) WHERE tracking_key = ?", *stopSecs, model).Error).To(Succeed())
		}
	}
	tag := func(model, generation string) {
		Expect(db.Model(&NodeModel{}).Where("model_name = ?", model).Update("load_generation", generation).Error).To(Succeed())
	}
	secs := func(n int) *int { return &n }

	rowExists := func(model string) bool {
		var count int64
		Expect(db.Model(&NodeModel{}).Where("model_name = ?", model).Count(&count).Error).To(Succeed())
		return count > 0
	}

	It("reclaims a staging row once the job of its attempt has been released", func() {
		seedReplica("abandoned", "staging", time.Hour)
		tag("abandoned", "gen-1")

		rc.reclaimAbandonedLoads(context.Background())

		Expect(rowExists("abandoned")).To(BeFalse())
	})

	It("reclaims a row whose attempt was replaced by another generation", func() {
		seedReplica("replaced", "staging", time.Hour)
		tag("replaced", "gen-1")
		seedJob("replaced", LoadJobStateStaging, "gen-2", 30, nil)

		rc.reclaimAbandonedLoads(context.Background())

		Expect(rowExists("replaced")).To(BeFalse())
	})

	It("holds the slot while the failed job of its attempt waits out the stop window", func() {
		seedReplica("holding", "staging", time.Hour)
		tag("holding", "gen-1")
		seedJob("holding", LoadJobStateFailed, "gen-1", -10, secs(100))

		rc.reclaimAbandonedLoads(context.Background())

		Expect(rowExists("holding")).To(BeTrue(), "remote work may still run until the stop deadline")
	})

	It("reclaims a jobless row once its node is gone", func() {
		seedReplica("orphan", "loading", time.Hour)
		Expect(registry.MarkUnhealthy(context.Background(), node.ID)).To(Succeed())

		rc.reclaimAbandonedLoads(context.Background())

		Expect(rowExists("orphan")).To(BeFalse())
	})

	// A row with no generation and no job may be a healthy load an older binary
	// drives. Treating a missing job as abandonment deleted healthy transfers the
	// moment they outran the grace period. That is what made a replica appear to
	// hop between nodes instead of finishing anywhere.
	It("keeps an untagged jobless row while its node is still healthy", func() {
		seedReplica("scaling-up", "staging", time.Hour)

		rc.reclaimAbandonedLoads(context.Background())

		Expect(rowExists("scaling-up")).To(BeTrue(),
			"nothing proves the load stopped")
	})

	It("reclaims an untagged row when the job of its model is released", func() {
		seedReplica("legacy", "staging", time.Hour)
		seedJob("legacy", LoadJobStateFailed, "gen-1", -10, secs(-1))

		rc.reclaimAbandonedLoads(context.Background())

		Expect(rowExists("legacy")).To(BeFalse())
	})

	It("keeps a long transfer whose job still holds a live lease", func() {
		// The row itself is old, because staging does not touch it. Only the
		// job proves the transfer is alive.
		seedReplica("big-model", "staging", time.Hour)
		tag("big-model", "gen-1")
		seedJob("big-model", LoadJobStateStaging, "gen-1", 30, nil)

		rc.reclaimAbandonedLoads(context.Background())

		Expect(rowExists("big-model")).To(BeTrue(), "a live transfer must never be reclaimed")
	})

	It("leaves a freshly created untagged row alone while its job row is still being written", func() {
		seedReplica("just-started", "loading", time.Second)

		rc.reclaimAbandonedLoads(context.Background())

		Expect(rowExists("just-started")).To(BeTrue())
	})

	It("does not touch loaded replicas, which the other sweeps own", func() {
		seedReplica("serving", "loaded", time.Hour)
		tag("serving", "gen-1")

		rc.reclaimAbandonedLoads(context.Background())

		Expect(rowExists("serving")).To(BeTrue())
	})

	It("frees the slot so the model can be scheduled on that node again", func() {
		seedReplica("wedged", "staging", time.Hour)
		tag("wedged", "gen-1")
		seedJob("wedged", LoadJobStateFailed, "gen-1", -60, secs(-1))

		_, err := registry.NextFreeReplicaIndex(context.Background(), node.ID, "wedged", 1)
		Expect(err).To(MatchError(ErrNoFreeSlot), "precondition: the stuck row holds the only slot")

		rc.reclaimAbandonedLoads(context.Background())

		idx, err := registry.NextFreeReplicaIndex(context.Background(), node.ID, "wedged", 1)
		Expect(err).ToNot(HaveOccurred())
		Expect(idx).To(Equal(0))
	})
})
