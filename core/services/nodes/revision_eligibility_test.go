package nodes

import (
	"context"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

var _ = Describe("revision eligibility consumers", func() {
	var (
		ctx      context.Context
		db       *gorm.DB
		registry *NodeRegistry
		nodes    map[string]*BackendNode
	)

	const modelName = "revision-matrix"

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		ctx = context.Background()
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		Expect(registry.AdvanceModelConfigRevision(ctx, modelName, "current")).To(BeEmpty())

		nodes = map[string]*BackendNode{}
		for i, kind := range []string{"current", "empty", "mismatch", "unloading"} {
			node := &BackendNode{Name: "revision-" + kind, NodeType: NodeTypeBackend, Address: "10.0.0." + string(rune('1'+i)) + ":50051", AvailableVRAM: uint64(100 + i)}
			Expect(registry.Register(ctx, node, true)).To(Succeed())
			nodes[kind] = node
			state := "loaded"
			revision := kind
			if kind == "current" {
				revision = "current"
			}
			if kind == "empty" {
				revision = ""
			}
			if kind == "unloading" {
				state = "unloading"
				revision = "current"
			}
			Expect(db.Create(&NodeModel{
				ID: kind, NodeID: node.ID, ModelName: modelName, ReplicaIndex: i,
				Address: kind, State: state, ConfigRevision: revision, LastUsed: time.Now().Add(time.Duration(i) * time.Minute),
				UpdatedAt: time.Now().Add(-time.Hour),
			}).Error).To(Succeed())
		}
	})

	It("establishes the first request revision without allowing a later request to roll it back", func() {
		const freshModel = "first-request-revision"
		Expect(registry.EstablishModelConfigRevision(ctx, freshModel, "new")).To(Succeed())
		Expect(registry.EstablishModelConfigRevision(ctx, freshModel, "old")).To(MatchError(ErrStaleModelConfigRevision))
		revision, err := registry.GetModelConfigRevision(ctx, freshModel)
		Expect(err).NotTo(HaveOccurred())
		Expect(revision).To(Equal("new"))
	})

	It("rejects mixed old-context and old-parallel requests before placement", func() {
		router := NewSmartRouter(registry, SmartRouterOptions{})
		for _, opts := range []*pb.ModelOptions{
			{ContextSize: 8192},
			{ContextSize: 100000, Options: []string{"parallel:4"}},
		} {
			_, err := router.Route(ctx, modelName, "models/revision.gguf", "llama-cpp", "old", opts, false)
			Expect(err).To(MatchError(ContainSubstring("stale model config revision")))
		}
		revision, getErr := registry.GetModelConfigRevision(ctx, modelName)
		Expect(getErr).NotTo(HaveOccurred())
		Expect(revision).To(Equal("current"))
	})

	DescribeTable("excludes empty, mismatched, and unloading rows after current state exists",
		func(query func() []string) {
			Expect(query()).To(ConsistOf("current"))
		},
		Entry("FindNodesWithModel", func() []string {
			got, err := registry.FindNodesWithModel(ctx, modelName)
			Expect(err).NotTo(HaveOccurred())
			out := make([]string, 0, len(got))
			for _, node := range got {
				out = append(out, node.Name[len("revision-"):])
			}
			return out
		}),
		Entry("ListAllLoadedModels", func() []string {
			got, err := registry.ListAllLoadedModels(ctx)
			Expect(err).NotTo(HaveOccurred())
			out := make([]string, 0, len(got))
			for _, row := range got {
				out = append(out, row.ID)
			}
			return out
		}),
		Entry("FindLRUModel", func() []string {
			Expect(db.Model(&NodeModel{}).Where("id IN ?", []string{"empty", "mismatch", "unloading"}).Update("node_id", nodes["current"].ID).Error).To(Succeed())
			row, err := registry.FindLRUModel(ctx, nodes["current"].ID)
			Expect(err).NotTo(HaveOccurred())
			return []string{row.ID}
		}),
		Entry("FindGlobalLRUModelWithZeroInFlight", func() []string {
			row, err := registry.FindGlobalLRUModelWithZeroInFlight(ctx)
			Expect(err).NotTo(HaveOccurred())
			return []string{row.ID}
		}),
	)

	It("applies eligibility to replica counts and slot allocation", func() {
		// Put stale rows into slots 1 and 2 on the same node. They must not
		// consume capacity once a current state exists.
		Expect(db.Model(&NodeModel{}).Where("id IN ?", []string{"empty", "mismatch"}).Update("node_id", nodes["current"].ID).Error).To(Succeed())
		count, err := registry.CountReplicasOnNode(ctx, nodes["current"].ID, modelName)
		Expect(err).NotTo(HaveOccurred())
		Expect(count).To(Equal(1))

		idx, err := registry.NextFreeReplicaIndex(ctx, nodes["current"].ID, modelName, 4)
		Expect(err).NotTo(HaveOccurred())
		Expect(idx).To(Equal(1))
	})

	It("GetWithExtras counts only current loaded rows in both per-node queries", func() {
		Expect(db.Model(&NodeModel{}).Where("model_name = ?", modelName).
			Updates(map[string]any{"node_id": nodes["current"].ID, "in_flight": 7}).Error).To(Succeed())

		got, err := registry.GetWithExtras(ctx, nodes["current"].ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ModelCount).To(Equal(1))
		Expect(got.InFlightCount).To(Equal(7))
	})

	It("scaleDownIdle selects a current idle row but not stale, empty, or unloading rows", func() {
		Expect(db.Model(&NodeModel{}).Where("model_name = ?", modelName).
			Updates(map[string]any{"node_id": nodes["current"].ID, "last_used": time.Now().Add(-2 * time.Hour)}).Error).To(Succeed())
		Expect(db.Model(&NodeModel{}).Where("id = ?", "empty").Update("replica_index", 8).Error).To(Succeed())
		Expect(db.Model(&NodeModel{}).Where("id = ?", "mismatch").Update("replica_index", 9).Error).To(Succeed())
		Expect(db.Create(&NodeModel{
			ID: "current-extra", NodeID: nodes["current"].ID, ModelName: modelName,
			ReplicaIndex: 4, Address: "current-extra", State: "loaded",
			ConfigRevision: "current", LastUsed: time.Now().Add(-time.Hour),
		}).Error).To(Succeed())

		unloader := &fakeUnloader{}
		rc := NewReplicaReconciler(ReplicaReconcilerOptions{
			Registry: registry, DB: db, Unloader: unloader, ScaleDownDelay: time.Minute,
		})
		rc.scaleDownIdle(ctx, ModelSchedulingConfig{ModelName: modelName}, 2, 1)

		Expect(unloader.unloadCalls).To(ConsistOf(nodes["current"].ID + ":" + modelName))
		var remaining []string
		Expect(db.Model(&NodeModel{}).Where("model_name = ?", modelName).Order("id").Pluck("id", &remaining).Error).To(Succeed())
		Expect(remaining).To(ConsistOf("current", "empty", "mismatch", "unloading"))
	})

	It("reconciler busy checks ignore stale idle replicas", func() {
		Expect(db.Model(&NodeModel{}).Where("id = ?", "current").Update("in_flight", 1).Error).To(Succeed())
		Expect(db.Model(&NodeModel{}).Where("id IN ?", []string{"empty", "mismatch"}).Update("node_id", nodes["current"].ID).Error).To(Succeed())
		rc := NewReplicaReconciler(ReplicaReconcilerOptions{Registry: registry, DB: db})
		Expect(rc.allReplicasBusy(ctx, modelName)).To(BeTrue())
	})

	DescribeTable("limits reconciler state queries to eligible rows",
		func(run func(*recordingEligibilityProber, *recordingEligibilityLister)) {
			prober := &recordingEligibilityProber{}
			lister := &recordingEligibilityLister{}
			run(prober, lister)
			Expect(prober.addresses).NotTo(ContainElements("empty", "mismatch", "unloading"))
			Expect(lister.nodeIDs).NotTo(ContainElements(nodes["empty"].ID, nodes["mismatch"].ID, nodes["unloading"].ID))
		},
		Entry("probeLoadedModels", func(prober *recordingEligibilityProber, _ *recordingEligibilityLister) {
			rc := NewReplicaReconciler(ReplicaReconcilerOptions{Registry: registry, DB: db, Prober: prober, ProbeStaleAfter: time.Minute})
			rc.probeLoadedModels(ctx)
			Expect(prober.addresses).To(ConsistOf("current"))
		}),
		Entry("sweepLeakedInFlight", func(prober *recordingEligibilityProber, _ *recordingEligibilityLister) {
			Expect(db.Model(&NodeModel{}).Where("model_name = ?", modelName).Updates(map[string]any{"in_flight": 1, "last_used": time.Now().Add(-2 * inFlightLeakIdleAfter)}).Error).To(Succeed())
			rc := NewReplicaReconciler(ReplicaReconcilerOptions{Registry: registry, DB: db, Prober: prober})
			rc.sweepLeakedInFlight(ctx)
			Expect(prober.addresses).To(ConsistOf("current"))
		}),
		Entry("reconcileNodeProcesses", func(_ *recordingEligibilityProber, lister *recordingEligibilityLister) {
			lister.running = map[string][]messaging.RunningModelInfo{nodes["current"].ID: {{ModelID: modelName, ReplicaIndex: 0}}}
			rc := NewReplicaReconciler(ReplicaReconcilerOptions{Registry: registry, DB: db, ProcessLister: lister, ProbeStaleAfter: time.Minute})
			rc.reconcileNodeProcesses(ctx)
			Expect(lister.nodeIDs).To(ConsistOf(nodes["current"].ID))
		}),
	)

	It("router eviction minimum-replica count ignores stale rows", func() {
		// A scheduling row makes the first OR branch false. With one current
		// replica at a minimum of one, the stale rows must not inflate the
		// revision-filtered count and make any row evictable.
		Expect(db.Create(&ModelSchedulingConfig{ModelName: modelName, MinReplicas: 1, MaxReplicas: 2}).Error).To(Succeed())
		shortCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
		router := NewSmartRouter(registry, SmartRouterOptions{DB: db, Unloader: &fakeUnloader{}})
		_, err := router.evictLRUAndFreeNode(shortCtx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("context cancelled"))

		// Once a second current replica exists, the count is genuinely above
		// the minimum and the oldest eligible current row may be selected.
		Expect(db.Create(&NodeModel{
			ID: "current-extra", NodeID: nodes["current"].ID, ModelName: modelName,
			ReplicaIndex: 4, Address: "current-extra", State: "loaded",
			ConfigRevision: "current", LastUsed: time.Now().Add(time.Minute),
		}).Error).To(Succeed())
		unloader := &fakeUnloader{}
		router = NewSmartRouter(registry, SmartRouterOptions{DB: db, Unloader: unloader})
		node, err := router.evictLRUAndFreeNode(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(node.ID).To(Equal(nodes["current"].ID))
		Expect(unloader.unloadCalls).To(ConsistOf(nodes["current"].ID + ":" + modelName))
		for _, id := range []string{"empty", "mismatch", "unloading"} {
			var count int64
			Expect(db.Model(&NodeModel{}).Where("id = ?", id).Count(&count).Error).To(Succeed())
			Expect(count).To(Equal(int64(1)), id+" must not be evicted")
		}
	})

	It("ordinary worker reaping preserves current state and matching replay info", func() {
		Expect(registry.UpsertModelLoadInfoRevision(ctx, modelName, "llama-cpp", "current", []byte("opts"))).To(Succeed())
		lister := &recordingEligibilityLister{}
		rc := NewReplicaReconciler(ReplicaReconcilerOptions{Registry: registry, DB: db, ProcessLister: lister, ProbeStaleAfter: time.Minute})
		for range workerMissesBeforeReap {
			rc.reconcileNodeProcesses(ctx)
			Expect(db.Model(&NodeModel{}).Where("id = ?", "current").Update("updated_at", time.Now().Add(-time.Hour)).Error).To(Succeed())
		}
		revision, err := registry.GetModelConfigRevision(ctx, modelName)
		Expect(err).NotTo(HaveOccurred())
		Expect(revision).To(Equal("current"))
		backend, revision, opts, err := registry.GetModelLoadInfoRevision(ctx, modelName)
		Expect(err).NotTo(HaveOccurred())
		Expect(backend).To(Equal("llama-cpp"))
		Expect(revision).To(Equal("current"))
		Expect(opts).To(Equal([]byte("opts")))
	})

	It("health/offline reaping preserves current state and matching replay info", func() {
		Expect(registry.UpsertModelLoadInfoRevision(ctx, modelName, "llama-cpp", "current", []byte("opts"))).To(Succeed())
		Expect(registry.MarkOffline(ctx, nodes["current"].ID)).To(Succeed())
		revision, err := registry.GetModelConfigRevision(ctx, modelName)
		Expect(err).NotTo(HaveOccurred())
		Expect(revision).To(Equal("current"))
		backend, revision, opts, err := registry.GetModelLoadInfoRevision(ctx, modelName)
		Expect(err).NotTo(HaveOccurred())
		Expect(backend).To(Equal("llama-cpp"))
		Expect(revision).To(Equal("current"))
		Expect(opts).To(Equal([]byte("opts")))
	})
})

type recordingEligibilityProber struct{ addresses []string }

func (p *recordingEligibilityProber) Probe(_ context.Context, address string) ProbeOutcome {
	p.addresses = append(p.addresses, address)
	return ProbeAlive
}

type recordingEligibilityLister struct {
	nodeIDs []string
	running map[string][]messaging.RunningModelInfo
}

func (l *recordingEligibilityLister) ListRunningModels(nodeID string) (*messaging.ModelsRunningReply, error) {
	l.nodeIDs = append(l.nodeIDs, nodeID)
	return &messaging.ModelsRunningReply{Models: l.running[nodeID]}, nil
}
