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

// in_flight has no decrement guarantee: a frontend that dies mid-request leaves
// the increment behind, and the load-time reservation is only released when the
// first inference completes. A leaked counter is not cosmetic — it pins the
// replica against LRU eviction (FindLRUModel, FindGlobalLRUModelWithZeroInFlight
// and the router's eviction query all require in_flight = 0), so the VRAM is
// never reclaimed.
//
// Resetting on elapsed time alone is unsafe: IncrementInFlight stamps last_used
// at request START and nothing moves it while the request runs, so a long
// generation looks identical to a leak. The discriminator is the health probe —
// a backend that answers immediately is not inside a request.
var _ = Describe("ReplicaReconciler — leaked in_flight sweeper", func() {
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
		node = &BackendNode{Name: "n1", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051"}
		Expect(registry.Register(context.Background(), node, true)).To(Succeed())
	})

	seed := func(id string, inFlight int, idleFor time.Duration) {
		Expect(db.Create(&NodeModel{
			ID:        id,
			NodeID:    node.ID,
			ModelName: id,
			Address:   addr,
			State:     "loaded",
			InFlight:  inFlight,
			LastUsed:  time.Now().Add(-idleFor),
			UpdatedAt: time.Now(),
		}).Error).To(Succeed())
	}

	inFlightOf := func(id string) int {
		var m NodeModel
		Expect(db.First(&m, "id = ?", id).Error).To(Succeed())
		return m.InFlight
	}

	newReconciler := func(prober ModelProber) *ReplicaReconciler {
		return NewReplicaReconciler(ReplicaReconcilerOptions{
			Registry: registry,
			DB:       db,
			Prober:   prober,
		})
	}

	It("resets a counter whose backend has been answering promptly while idle", func() {
		seed("leaked", 3, 2*inFlightLeakIdleAfter)
		rc := newReconciler(&fakeProber{outcomes: map[string]ProbeOutcome{addr: ProbeAlive}})

		for range inFlightLeakConfirmations {
			rc.sweepLeakedInFlight(context.Background())
		}

		Expect(inFlightOf("leaked")).To(Equal(0),
			"an idle backend answering health checks cannot be serving 3 requests")
	})

	It("never resets a counter while the backend is mid-request", func() {
		// The exact case a time-based sweeper gets wrong: a video generation
		// that has been running for far longer than the idle threshold.
		seed("generating", 1, 2*inFlightLeakIdleAfter)
		rc := newReconciler(&fakeProber{outcomes: map[string]ProbeOutcome{addr: ProbeBusy}})

		for range inFlightLeakConfirmations + 3 {
			rc.sweepLeakedInFlight(context.Background())
		}

		Expect(inFlightOf("generating")).To(Equal(1),
			"resetting here would expose a serving model to LRU eviction")
	})

	It("never resets a counter that was used recently", func() {
		// A busy parallel backend keeps last_used fresh through each new
		// request's increment, so recency alone protects it.
		seed("recent", 2, time.Minute)
		rc := newReconciler(&fakeProber{outcomes: map[string]ProbeOutcome{addr: ProbeAlive}})

		for range inFlightLeakConfirmations + 3 {
			rc.sweepLeakedInFlight(context.Background())
		}

		Expect(inFlightOf("recent")).To(Equal(2))
	})

	It("requires consecutive idle confirmations before resetting", func() {
		seed("flappy", 1, 2*inFlightLeakIdleAfter)
		prober := &fakeProber{outcomes: map[string]ProbeOutcome{addr: ProbeAlive}}
		rc := newReconciler(prober)

		for i := 1; i < inFlightLeakConfirmations; i++ {
			rc.sweepLeakedInFlight(context.Background())
			Expect(inFlightOf("flappy")).To(Equal(1),
				"one idle observation is not enough to overrule the counter")
		}

		// Backend turns out to be working after all: the streak resets.
		prober.outcomes[addr] = ProbeBusy
		rc.sweepLeakedInFlight(context.Background())
		prober.outcomes[addr] = ProbeAlive
		for i := 1; i < inFlightLeakConfirmations; i++ {
			rc.sweepLeakedInFlight(context.Background())
		}

		Expect(inFlightOf("flappy")).To(Equal(1))
	})

	It("leaves rows with a zero counter alone", func() {
		seed("idle", 0, 2*inFlightLeakIdleAfter)
		prober := &fakeProber{outcomes: map[string]ProbeOutcome{addr: ProbeAlive}}
		rc := newReconciler(prober)

		rc.sweepLeakedInFlight(context.Background())

		Expect(inFlightOf("idle")).To(Equal(0))
		Expect(prober.calls).To(Equal(0), "nothing to correct means nothing to probe")
	})

	It("frees the replica for LRU eviction once the leak is cleared", func() {
		// The point of the sweep: FindLRUModel only considers in_flight = 0,
		// so until the counter is corrected this replica's VRAM is unreclaimable.
		seed("pinned", 1, 2*inFlightLeakIdleAfter)
		rc := newReconciler(&fakeProber{outcomes: map[string]ProbeOutcome{addr: ProbeAlive}})

		_, err := registry.FindLRUModel(context.Background(), node.ID, nil)
		Expect(err).To(HaveOccurred(), "precondition: the leak hides the row from LRU")

		for range inFlightLeakConfirmations {
			rc.sweepLeakedInFlight(context.Background())
		}

		lru, err := registry.FindLRUModel(context.Background(), node.ID, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(lru.ModelName).To(Equal("pinned"))
	})
})
