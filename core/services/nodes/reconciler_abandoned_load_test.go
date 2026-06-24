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
// runs for tens of minutes. The load job's LastProgress heartbeat is the
// discriminator, the same signal job takeover already trusts.
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

	seedJob := func(model, state string, sinceProgress time.Duration) {
		Expect(db.Create(&ModelLoadJob{
			TrackingKey:  model,
			State:        state,
			OwnerReplica: "someone",
			LastProgress: time.Now().Add(-sinceProgress),
			CreatedAt:    time.Now().Add(-sinceProgress),
			UpdatedAt:    time.Now().Add(-sinceProgress),
		}).Error).To(Succeed())
	}

	rowExists := func(model string) bool {
		var count int64
		Expect(db.Model(&NodeModel{}).Where("model_name = ?", model).Count(&count).Error).To(Succeed())
		return count > 0
	}

	It("reclaims a staging row whose load job has stopped heartbeating", func() {
		seedReplica("abandoned", "staging", time.Hour)
		seedJob("abandoned", LoadJobStateStaging, 30*time.Minute)

		rc.reclaimAbandonedLoads(context.Background())

		Expect(rowExists("abandoned")).To(BeFalse())
	})

	It("reclaims a jobless row once its node is gone", func() {
		seedReplica("orphan", "loading", time.Hour)
		Expect(registry.MarkUnhealthy(context.Background(), node.ID)).To(Succeed())

		rc.reclaimAbandonedLoads(context.Background())

		Expect(rowExists("orphan")).To(BeFalse())
	})

	// Only the request path creates load jobs. The reconciler's own scale-up
	// loads a replica without one, so treating a missing job as abandonment
	// deleted healthy transfers the moment they outran the grace period, which
	// for a multi-gigabyte checkpoint is every time. That is what made a replica
	// appear to hop between nodes instead of finishing anywhere.
	It("keeps a jobless row while its node is still healthy", func() {
		seedReplica("scaling-up", "staging", time.Hour)

		rc.reclaimAbandonedLoads(context.Background())

		Expect(rowExists("scaling-up")).To(BeTrue(),
			"a reconciler-driven load has no job row and must not be reclaimed for it")
	})

	It("keeps a long transfer whose job is still heartbeating", func() {
		// The row itself is old, because staging does not touch it. Only the
		// job proves the transfer is alive.
		seedReplica("big-model", "staging", time.Hour)
		seedJob("big-model", LoadJobStateStaging, time.Second)

		rc.reclaimAbandonedLoads(context.Background())

		Expect(rowExists("big-model")).To(BeTrue(), "a live transfer must never be reclaimed")
	})

	It("leaves a freshly created row alone while its job row is still being written", func() {
		seedReplica("just-started", "loading", time.Second)

		rc.reclaimAbandonedLoads(context.Background())

		Expect(rowExists("just-started")).To(BeTrue())
	})

	It("does not touch loaded replicas, which the other sweeps own", func() {
		seedReplica("serving", "loaded", time.Hour)

		rc.reclaimAbandonedLoads(context.Background())

		Expect(rowExists("serving")).To(BeTrue())
	})

	It("frees the slot so the model can be scheduled on that node again", func() {
		seedReplica("wedged", "staging", time.Hour)
		seedJob("wedged", LoadJobStateFailed, time.Minute)

		_, err := registry.NextFreeReplicaIndex(context.Background(), node.ID, "wedged", 1)
		Expect(err).To(MatchError(ErrNoFreeSlot), "precondition: the stuck row holds the only slot")

		rc.reclaimAbandonedLoads(context.Background())

		idx, err := registry.NextFreeReplicaIndex(context.Background(), node.ID, "wedged", 1)
		Expect(err).ToNot(HaveOccurred())
		Expect(idx).To(Equal(0))
	})
})
