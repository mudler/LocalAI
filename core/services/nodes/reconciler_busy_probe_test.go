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

// The liveness probe is a 1s gRPC HealthCheck. Backends that block inside a
// long synchronous generation (video/avatar models spend 10+ minutes in a
// denoising loop) cannot answer it, so a single failed probe is not evidence
// that the process died. These tests pin the two guards that keep the reaper
// from deleting registry rows for backends that are alive and working:
// in-flight requests are never reaped, and a row must fail several consecutive
// probes before it is treated as a ghost.
var _ = Describe("ReplicaReconciler — probe reaper vs busy backends", func() {
	var (
		db       *gorm.DB
		registry *NodeRegistry
		node     *BackendNode
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		node = &BackendNode{Name: "busy-node", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051"}
		Expect(registry.Register(context.Background(), node, true)).To(Succeed())
	})

	It("never reaps a replica that has in-flight requests", func() {
		busy := &NodeModel{
			ID:        "busy-1",
			NodeID:    node.ID,
			ModelName: "longcat-video-avatar",
			Address:   "10.0.0.1:12345",
			State:     "loaded",
			InFlight:  1,
			UpdatedAt: time.Now().Add(-5 * time.Minute),
		}
		Expect(db.Create(busy).Error).To(Succeed())

		// The backend is mid-generation, so it cannot answer the health probe.
		prober := &fakeProber{alive: map[string]bool{"10.0.0.1:12345": false}}
		rc := NewReplicaReconciler(ReplicaReconcilerOptions{
			Registry:        registry,
			DB:              db,
			Prober:          prober,
			ProbeStaleAfter: 2 * time.Minute,
		})

		// Many passes must not remove a busy replica, however long it stays busy.
		for range 5 {
			rc.probeLoadedModels(context.Background())
		}

		var after NodeModel
		Expect(db.First(&after, "id = ?", "busy-1").Error).To(Succeed(),
			"a replica serving a request must never be reaped by the liveness probe")
		// A busy row is filtered out in SQL, so it is never even probed.
		Expect(prober.calls).To(Equal(0))
	})

	It("requires consecutive probe failures before reaping an idle replica", func() {
		idle := &NodeModel{
			ID:        "idle-1",
			NodeID:    node.ID,
			ModelName: "ghost-model",
			Address:   "10.0.0.1:12345",
			State:     "loaded",
			InFlight:  0,
			UpdatedAt: time.Now().Add(-5 * time.Minute),
		}
		Expect(db.Create(idle).Error).To(Succeed())

		prober := &fakeProber{alive: map[string]bool{"10.0.0.1:12345": false}}
		rc := NewReplicaReconciler(ReplicaReconcilerOptions{
			Registry:        registry,
			DB:              db,
			Prober:          prober,
			ProbeStaleAfter: 2 * time.Minute,
		})

		// A single miss is a transient blip, not a dead process.
		rc.probeLoadedModels(context.Background())
		var after NodeModel
		Expect(db.First(&after, "id = ?", "idle-1").Error).To(Succeed(),
			"one failed probe must not be enough to delete a registry row")

		rc.probeLoadedModels(context.Background())
		Expect(db.First(&after, "id = ?", "idle-1").Error).To(Succeed(),
			"two failed probes must not be enough to delete a registry row")

		// Third consecutive failure: the process really is gone.
		rc.probeLoadedModels(context.Background())
		Expect(db.First(&after, "id = ?", "idle-1").Error).To(MatchError(gorm.ErrRecordNotFound),
			"a replica that failed every probe must eventually be reaped")
	})

	It("resets the failure streak when a replica answers again", func() {
		flaky := &NodeModel{
			ID:        "flaky-1",
			NodeID:    node.ID,
			ModelName: "flaky-model",
			Address:   "10.0.0.1:12345",
			State:     "loaded",
			InFlight:  0,
			UpdatedAt: time.Now().Add(-5 * time.Minute),
		}
		Expect(db.Create(flaky).Error).To(Succeed())

		prober := &fakeProber{alive: map[string]bool{"10.0.0.1:12345": false}}
		rc := NewReplicaReconciler(ReplicaReconcilerOptions{
			Registry:        registry,
			DB:              db,
			Prober:          prober,
			ProbeStaleAfter: 2 * time.Minute,
		})

		rc.probeLoadedModels(context.Background())
		rc.probeLoadedModels(context.Background())

		// It answers again, which clears the streak and bumps updated_at.
		prober.alive["10.0.0.1:12345"] = true
		rc.probeLoadedModels(context.Background())

		// Back to failing: the streak restarts from zero, so two more misses
		// must not delete the row.
		prober.alive["10.0.0.1:12345"] = false
		Expect(db.Model(&NodeModel{}).Where("id = ?", "flaky-1").
			Update("updated_at", time.Now().Add(-5*time.Minute)).Error).To(Succeed())
		rc.probeLoadedModels(context.Background())
		Expect(db.Model(&NodeModel{}).Where("id = ?", "flaky-1").
			Update("updated_at", time.Now().Add(-5*time.Minute)).Error).To(Succeed())
		rc.probeLoadedModels(context.Background())

		var after NodeModel
		Expect(db.First(&after, "id = ?", "flaky-1").Error).To(Succeed(),
			"a successful probe must reset the consecutive-failure streak")
	})
})
