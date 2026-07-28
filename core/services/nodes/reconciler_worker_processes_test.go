package nodes

import (
	"context"
	"errors"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// fakeProcessLister stands in for the NATS round-trip to a worker.
type fakeProcessLister struct {
	running map[string][]messaging.RunningModelInfo
	err     error
	calls   int
}

func (f *fakeProcessLister) ListRunningModels(nodeID string) (*messaging.ModelsRunningReply, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &messaging.ModelsRunningReply{Models: f.running[nodeID]}, nil
}

// The worker owns the backend processes, so its answer is authoritative and,
// unlike a health probe against the backend's own port, is unaffected by how
// busy those backends are. These tests pin the reconciliation of registry rows
// against that answer.
var _ = Describe("ReplicaReconciler — reconcile against worker processes", func() {
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
		node = &BackendNode{Name: "w1", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051"}
		Expect(registry.Register(context.Background(), node, true)).To(Succeed())
	})

	seed := func(id, modelName string, replica int, age time.Duration) {
		Expect(db.Create(&NodeModel{
			ID:           id,
			NodeID:       node.ID,
			ModelName:    modelName,
			ReplicaIndex: replica,
			Address:      "10.0.0.1:12345",
			State:        "loaded",
			UpdatedAt:    time.Now().Add(-age),
		}).Error).To(Succeed())
	}

	makeStale := func(id string) {
		Expect(db.Model(&NodeModel{}).Where("id = ?", id).
			Update("updated_at", time.Now().Add(-5*time.Minute)).Error).To(Succeed())
	}

	newReconciler := func(lister NodeProcessLister) *ReplicaReconciler {
		return NewReplicaReconciler(ReplicaReconcilerOptions{
			Registry:        registry,
			DB:              db,
			ProcessLister:   lister,
			ProbeStaleAfter: 2 * time.Minute,
		})
	}

	It("reaps a row for a model the worker is not running", func() {
		seed("ghost-1", "ghost-model", 0, 5*time.Minute)
		lister := &fakeProcessLister{running: map[string][]messaging.RunningModelInfo{}}
		rc := newReconciler(lister)

		for range workerMissesBeforeReap {
			rc.reconcileNodeProcesses(context.Background())
			makeStale("ghost-1")
		}

		var after NodeModel
		Expect(db.First(&after, "id = ?", "ghost-1").Error).To(MatchError(gorm.ErrRecordNotFound),
			"the worker is the authority on which processes exist")
	})

	It("keeps and refreshes a row the worker confirms is running", func() {
		seed("live-1", "live-model", 0, 5*time.Minute)
		lister := &fakeProcessLister{running: map[string][]messaging.RunningModelInfo{
			node.ID: {{ModelID: "live-model", ReplicaIndex: 0, Address: "10.0.0.1:12345"}},
		}}
		rc := newReconciler(lister)

		for range workerMissesBeforeReap + 2 {
			rc.reconcileNodeProcesses(context.Background())
			makeStale("live-1")
		}

		var after NodeModel
		Expect(db.First(&after, "id = ?", "live-1").Error).To(Succeed())
	})

	It("refreshes updated_at so the port probe skips a worker-confirmed model", func() {
		// This is what keeps a busy backend off the port prober entirely: the
		// worker vouches for it, so it never looks stale enough to probe.
		seed("busy-1", "busy-model", 0, 5*time.Minute)
		lister := &fakeProcessLister{running: map[string][]messaging.RunningModelInfo{
			node.ID: {{ModelID: "busy-model", ReplicaIndex: 0, Address: "10.0.0.1:12345"}},
		}}
		rc := newReconciler(lister)

		rc.reconcileNodeProcesses(context.Background())

		var after NodeModel
		Expect(db.First(&after, "id = ?", "busy-1").Error).To(Succeed())
		Expect(after.UpdatedAt).To(BeTemporally("~", time.Now(), 5*time.Second))
	})

	It("distinguishes replicas of the same model", func() {
		seed("rep-0", "multi-model", 0, 5*time.Minute)
		seed("rep-1", "multi-model", 1, 5*time.Minute)
		lister := &fakeProcessLister{running: map[string][]messaging.RunningModelInfo{
			node.ID: {{ModelID: "multi-model", ReplicaIndex: 0, Address: "10.0.0.1:12345"}},
		}}
		rc := newReconciler(lister)

		for range workerMissesBeforeReap {
			rc.reconcileNodeProcesses(context.Background())
			makeStale("rep-0")
			makeStale("rep-1")
		}

		var kept NodeModel
		Expect(db.First(&kept, "id = ?", "rep-0").Error).To(Succeed(),
			"replica 0 is running and must survive")
		var reaped NodeModel
		Expect(db.First(&reaped, "id = ?", "rep-1").Error).To(MatchError(gorm.ErrRecordNotFound),
			"replica 1 is not running and must be reaped")
	})

	It("reaps nothing when the worker cannot be reached", func() {
		// A NATS failure says nothing about the processes. Reaping here would
		// delete the whole node's rows on a transient messaging blip.
		seed("unknown-1", "unknown-model", 0, 5*time.Minute)
		lister := &fakeProcessLister{err: errors.New("nats: timeout")}
		rc := newReconciler(lister)

		for range workerMissesBeforeReap + 3 {
			rc.reconcileNodeProcesses(context.Background())
			makeStale("unknown-1")
		}

		var after NodeModel
		Expect(db.First(&after, "id = ?", "unknown-1").Error).To(Succeed(),
			"an unreachable worker must never cause its replicas to be reaped")
	})

	It("ignores rows younger than the stale cutoff", func() {
		// A row created moments ago may legitimately not be in the worker's
		// table yet; judging it immediately would race every fresh load.
		seed("fresh-1", "fresh-model", 0, 0)
		lister := &fakeProcessLister{running: map[string][]messaging.RunningModelInfo{}}
		rc := newReconciler(lister)

		for range workerMissesBeforeReap + 2 {
			rc.reconcileNodeProcesses(context.Background())
		}

		var after NodeModel
		Expect(db.First(&after, "id = ?", "fresh-1").Error).To(Succeed())
		Expect(lister.calls).To(Equal(0), "no stale rows means no reason to ask the worker")
	})

	It("requires consecutive misses before reaping", func() {
		seed("flap-1", "flap-model", 0, 5*time.Minute)
		lister := &fakeProcessLister{running: map[string][]messaging.RunningModelInfo{}}
		rc := newReconciler(lister)

		for i := 1; i < workerMissesBeforeReap; i++ {
			rc.reconcileNodeProcesses(context.Background())
			makeStale("flap-1")
			var after NodeModel
			Expect(db.First(&after, "id = ?", "flap-1").Error).To(Succeed())
		}

		// It shows up again: the streak resets.
		lister.running[node.ID] = []messaging.RunningModelInfo{
			{ModelID: "flap-model", ReplicaIndex: 0, Address: "10.0.0.1:12345"},
		}
		rc.reconcileNodeProcesses(context.Background())
		makeStale("flap-1")

		lister.running[node.ID] = nil
		for i := 1; i < workerMissesBeforeReap; i++ {
			rc.reconcileNodeProcesses(context.Background())
			makeStale("flap-1")
		}

		var after NodeModel
		Expect(db.First(&after, "id = ?", "flap-1").Error).To(Succeed(),
			"a reappearance must reset the consecutive-miss streak")
	})
})
