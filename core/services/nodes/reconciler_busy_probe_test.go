package nodes

import (
	"context"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/testutil"
)

// The liveness probe is a short gRPC HealthCheck, and a backend that blocks
// inside a long synchronous generation (video/avatar models spend 10+ minutes
// in a denoising loop) cannot answer it. Treating that silence as death
// deleted registry rows for backends that were alive and working.
//
// The discriminator is HOW the probe failed, not whether the replica looked
// busy in the database: a dead process refuses the connection, while a busy one
// accepts it and never services the RPC. Crucially this does NOT consult
// in_flight — that counter can leak high and would then shield a genuinely dead
// replica from ever being reaped.
var _ = Describe("ReplicaReconciler — probe reaper vs busy backends", func() {
	var (
		db       *gorm.DB
		registry *NodeRegistry
		node     *BackendNode
	)

	const addr = "10.0.0.1:12345"

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

	// seed inserts a stale loaded row so the probe pass picks it up.
	seed := func(id string, inFlight int) {
		Expect(db.Create(&NodeModel{
			ID:                 id,
			NodeID:             node.ID,
			ModelName:          id,
			WorkerLocalAddress: addr,
			State:              "loaded",
			InFlight:           inFlight,
			UpdatedAt:          time.Now().Add(-5 * time.Minute),
		}).Error).To(Succeed())
	}

	// makeStale rewinds updated_at so the row qualifies for the next pass.
	makeStale := func(id string) {
		Expect(db.Model(&NodeModel{}).Where("id = ?", id).
			Update("updated_at", time.Now().Add(-5*time.Minute)).Error).To(Succeed())
	}

	newReconciler := func(prober ModelProber) *ReplicaReconciler {
		return NewReplicaReconciler(ReplicaReconcilerOptions{
			Registry:        registry,
			DB:              db,
			Prober:          prober,
			ProbeStaleAfter: 2 * time.Minute,
		})
	}

	It("never reaps a replica that is reachable but too busy to answer", func() {
		seed("busy-1", 0)
		prober := &fakeProber{outcomes: map[string]ProbeOutcome{addr: ProbeBusy}}
		rc := newReconciler(prober)

		// However long the generation runs, a busy backend is not a dead one.
		for range 10 {
			rc.probeLoadedModels(context.Background())
			makeStale("busy-1")
		}

		var after NodeModel
		Expect(db.First(&after, "id = ?", "busy-1").Error).To(Succeed(),
			"a backend that accepted the connection but was mid-request must never be reaped")
	})

	It("never reaps a replica it could not probe at all", func() {
		// ProbeUnknown is this FRONTEND saying it has no way to reach the
		// worker, which is nothing at all about the backend. Folding it into
		// ProbeUnreachable would empty the whole node_models table the moment
		// the tunnel wiring was wrong, and the models would still be running.
		seed("unknown-1", 0)
		prober := &fakeProber{outcomes: map[string]ProbeOutcome{addr: ProbeUnknown}}
		rc := newReconciler(prober)

		for range probeFailuresBeforeReap * 3 {
			rc.probeLoadedModels(context.Background())
			makeStale("unknown-1")
		}

		var after NodeModel
		Expect(db.First(&after, "id = ?", "unknown-1").Error).To(Succeed(),
			"a replica this frontend could not reach must never be reaped")
	})

	It("does not let an unprobeable pass forgive a real failure streak", func() {
		// The other half of the same rule. Clearing the streak on an outcome
		// that observed nothing would let a flapping tunnel keep a genuinely
		// dead backend in the table forever.
		seed("mixed-1", 0)
		prober := &fakeProber{outcomes: map[string]ProbeOutcome{addr: ProbeUnreachable}}
		rc := newReconciler(prober)

		for i := 1; i < probeFailuresBeforeReap; i++ {
			rc.probeLoadedModels(context.Background())
			makeStale("mixed-1")
		}
		prober.outcomes[addr] = ProbeUnknown
		rc.probeLoadedModels(context.Background())
		makeStale("mixed-1")

		prober.outcomes[addr] = ProbeUnreachable
		rc.probeLoadedModels(context.Background())
		var after NodeModel
		Expect(db.First(&after, "id = ?", "mixed-1").Error).To(MatchError(gorm.ErrRecordNotFound),
			"an unprobeable pass must leave the streak untouched, not reset it")
	})

	It("reaps an unreachable replica even when in_flight leaked high", func() {
		// in_flight has no decrement guarantee: a frontend that dies mid-request
		// leaves the increment behind forever. Gating the reaper on it would
		// make such a row permanently unreapable, so the reaper must ignore it.
		seed("leaked-1", 7)
		prober := &fakeProber{outcomes: map[string]ProbeOutcome{addr: ProbeUnreachable}}
		rc := newReconciler(prober)

		for range probeFailuresBeforeReap {
			rc.probeLoadedModels(context.Background())
			makeStale("leaked-1")
		}

		var after NodeModel
		Expect(db.First(&after, "id = ?", "leaked-1").Error).To(MatchError(gorm.ErrRecordNotFound),
			"a stale in_flight counter must not shield a dead replica from reaping")
	})

	It("requires consecutive unreachable probes before reaping", func() {
		seed("idle-1", 0)
		prober := &fakeProber{outcomes: map[string]ProbeOutcome{addr: ProbeUnreachable}}
		rc := newReconciler(prober)

		for i := 1; i < probeFailuresBeforeReap; i++ {
			rc.probeLoadedModels(context.Background())
			makeStale("idle-1")
			var after NodeModel
			Expect(db.First(&after, "id = ?", "idle-1").Error).To(Succeed(),
				"a replica must survive until the failure threshold is reached")
		}

		rc.probeLoadedModels(context.Background())
		var after NodeModel
		Expect(db.First(&after, "id = ?", "idle-1").Error).To(MatchError(gorm.ErrRecordNotFound))
	})

	It("resets the failure streak when a replica answers again", func() {
		seed("flaky-1", 0)
		prober := &fakeProber{outcomes: map[string]ProbeOutcome{addr: ProbeUnreachable}}
		rc := newReconciler(prober)

		rc.probeLoadedModels(context.Background())
		makeStale("flaky-1")
		rc.probeLoadedModels(context.Background())
		makeStale("flaky-1")

		prober.outcomes[addr] = ProbeAlive
		rc.probeLoadedModels(context.Background())
		makeStale("flaky-1")

		// Streak restarts from zero, so the threshold is not reached again yet.
		prober.outcomes[addr] = ProbeUnreachable
		for i := 1; i < probeFailuresBeforeReap; i++ {
			rc.probeLoadedModels(context.Background())
			makeStale("flaky-1")
		}

		var after NodeModel
		Expect(db.First(&after, "id = ?", "flaky-1").Error).To(Succeed(),
			"a successful probe must reset the consecutive-failure streak")
	})

	It("does not let a busy probe count toward the failure streak", func() {
		seed("mixed-1", 0)
		prober := &fakeProber{outcomes: map[string]ProbeOutcome{addr: ProbeUnreachable}}
		rc := newReconciler(prober)

		// Two genuine misses...
		rc.probeLoadedModels(context.Background())
		makeStale("mixed-1")
		rc.probeLoadedModels(context.Background())
		makeStale("mixed-1")

		// ...then the backend starts a long generation. That is evidence of
		// life, so it must not be the miss that tips the row over the edge.
		prober.outcomes[addr] = ProbeBusy
		rc.probeLoadedModels(context.Background())
		makeStale("mixed-1")

		var after NodeModel
		Expect(db.First(&after, "id = ?", "mixed-1").Error).To(Succeed(),
			"a busy probe is proof of life and must clear the failure streak")
	})
})

// errConnRefused builds the error gRPC surfaces when nothing is listening on
// the address, which is what a dead backend process looks like.
func errConnRefused() error {
	return status.Error(codes.Unavailable, "connection error: desc = \"transport: Error while dialing: dial tcp 10.0.0.1:12345: connect: connection refused\"")
}

var _ = Describe("classifyProbeOutcome", func() {
	It("treats a deadline as busy and a refused connection as unreachable", func() {
		// These are the two cases that matter: they are what the reaper keys
		// its delete decision off.
		Expect(classifyProbeOutcome(true, nil)).To(Equal(ProbeAlive))
		Expect(classifyProbeOutcome(false, context.DeadlineExceeded)).To(Equal(ProbeBusy))
		Expect(classifyProbeOutcome(false, errConnRefused())).To(Equal(ProbeUnreachable))
		// Answered, but reported unhealthy: an affirmative signal, unlike silence.
		Expect(classifyProbeOutcome(false, nil)).To(Equal(ProbeUnreachable))
	})
})
