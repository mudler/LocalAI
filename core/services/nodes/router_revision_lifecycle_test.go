package nodes

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"time"

	corebackend "github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type recordingRevisionStopper struct {
	mu       sync.Mutex
	replicas []NodeModel
	err      error
}

func (s *recordingRevisionStopper) StopModelReplica(_ context.Context, _ string, replica NodeModel, _ bool) (messaging.ModelStopReply, error) {
	s.mu.Lock()
	s.replicas = append(s.replicas, replica)
	s.mu.Unlock()
	return messaging.ModelStopReply{}, s.err
}

var _ = Describe("revision-bound load publication", func() {
	var (
		ctx      context.Context
		db       *gorm.DB
		registry *NodeRegistry
		node     *BackendNode
		backend  *stubBackend
		unloader *fakeUnloader
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		ctx = context.Background()
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		node = &BackendNode{Name: "revision-worker", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051", TotalVRAM: 64_000_000_000, AvailableVRAM: 64_000_000_000}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		backend = &stubBackend{healthResult: true, loadResult: &pb.Result{Success: true}}
		unloader = &fakeUnloader{installReply: &messaging.BackendInstallReply{Success: true, Address: "10.0.0.1:9001"}}
	})

	It("quarantines and exactly stops a load that finishes after its revision changes", func() {
		const modelName = "edited-while-loading"
		Expect(registry.EstablishModelConfigRevision(ctx, modelName, "rev-old")).To(Succeed())

		entered := make(chan struct{})
		release := make(chan struct{})
		backend.loadHook = func(*pb.ModelOptions) {
			close(entered)
			<-release
		}
		stopper := &recordingRevisionStopper{err: errors.New("worker temporarily unreachable")}
		router := NewSmartRouter(registry, SmartRouterOptions{
			Unloader:      unloader,
			ClientFactory: &stubClientFactory{client: backend},
			ModelCleanup:  NewModelCleanupService(registry, stopper),
			DB:            db,
		})

		done := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			_, err := router.Route(ctx, modelName, "models/edited.gguf", "llama-cpp", "rev-old", &pb.ModelOptions{ContextSize: 8192}, false)
			done <- err
		}()
		Eventually(entered, 5*time.Second).Should(BeClosed())

		quarantined, err := registry.AdvanceModelConfigRevision(ctx, modelName, "rev-new")
		Expect(err).NotTo(HaveOccurred())
		Expect(quarantined).To(HaveLen(1))
		close(release)

		var routeErr error
		Eventually(done, 10*time.Second).Should(Receive(&routeErr))
		Expect(routeErr).To(MatchError(ContainSubstring("stale model config revision")))
		Eventually(func() int {
			stopper.mu.Lock()
			defer stopper.mu.Unlock()
			return len(stopper.replicas)
		}).Should(Equal(1))

		var rows []NodeModel
		Expect(db.Where("model_name = ?", modelName).Find(&rows).Error).To(Succeed())
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].State).To(Equal("unloading"))
		Expect(rows[0].ConfigRevision).To(Equal("rev-old"))
		Expect(rows[0].CleanupAttempts).To(Equal(1))
		Expect(rows[0].CleanupNextRetryAt).NotTo(BeNil())
		var replayCount int64
		Expect(db.Model(&ModelLoadInfo{}).Where("model_name = ?", modelName).Count(&replayCount).Error).To(Succeed())
		Expect(replayCount).To(BeZero())
		revision, err := registry.GetModelConfigRevision(ctx, modelName)
		Expect(err).NotTo(HaveOccurred())
		Expect(revision).To(Equal("rev-new"))
		job, err := registry.GetLoadJob(ctx, modelName)
		Expect(err).NotTo(HaveOccurred())
		Expect(job).NotTo(BeNil())
		Expect(job.State).To(Equal(LoadJobStateFailed))
		Expect(job.LastError).To(ContainSubstring("stale model config revision"))
	})

	It("rechecks the revision transactionally when claiming a loaded replica", func() {
		const modelName = "claim-race"
		Expect(registry.EstablishModelConfigRevision(ctx, modelName, "rev-old")).To(Succeed())
		Expect(registry.SetNodeModelRevision(ctx, node.ID, modelName, 0, "loaded", "10.0.0.1:9001", 0, "rev-old", "hash-old")).To(Succeed())

		edit := db.Begin()
		Expect(edit.Error).NotTo(HaveOccurred())
		var state ModelConfigState
		Expect(edit.Clauses(clause.Locking{Strength: "UPDATE"}).Where("model_name = ?", modelName).First(&state).Error).To(Succeed())
		Expect(edit.Model(&ModelConfigState{}).Where("model_name = ?", modelName).Update("config_revision", "rev-new").Error).To(Succeed())
		Expect(edit.Model(&NodeModel{}).Where("model_name = ?", modelName).Update("state", "unloading").Error).To(Succeed())

		type claimResult struct {
			nm  *NodeModel
			err error
		}
		claimed := make(chan claimResult, 1)
		go func() {
			defer GinkgoRecover()
			_, nm, err := registry.FindAndLockNodeWithModel(ctx, modelName, nil, nil)
			claimed <- claimResult{nm: nm, err: err}
		}()
		Consistently(claimed, 200*time.Millisecond).ShouldNot(Receive())
		Expect(edit.Commit().Error).To(Succeed())

		var result claimResult
		Eventually(claimed, 5*time.Second).Should(Receive(&result))
		Expect(result.err).To(MatchError(gorm.ErrRecordNotFound))
		Expect(result.nm).To(BeNil())
		var row NodeModel
		Expect(db.Where("model_name = ?", modelName).First(&row).Error).To(Succeed())
		Expect(row.InFlight).To(BeZero())
		Expect(row.State).To(Equal("unloading"))
	})

	It("loads changed context and parallel options as a new revision", func() {
		const modelName = "changed-options"
		stopper := &recordingRevisionStopper{}
		router := NewSmartRouter(registry, SmartRouterOptions{
			Unloader:      unloader,
			ClientFactory: &stubClientFactory{client: backend},
			ModelCleanup:  NewModelCleanupService(registry, stopper),
		})

		first, err := router.Route(ctx, modelName, "models/changed.gguf", "llama-cpp", "rev-8k", &pb.ModelOptions{ContextSize: 8192}, false)
		Expect(err).NotTo(HaveOccurred())
		first.Release()
		quarantined, err := registry.AdvanceModelConfigRevision(ctx, modelName, "rev-100k")
		Expect(err).NotTo(HaveOccurred())
		NewModelCleanupService(registry, stopper).Cleanup(ctx, quarantined, false)

		second, err := router.Route(ctx, modelName, "models/changed.gguf", "llama-cpp", "rev-100k", &pb.ModelOptions{ContextSize: 100000, Options: []string{"parallel:4"}}, true)
		Expect(err).NotTo(HaveOccurred())
		second.Release()
		backend.mu.Lock()
		loads := append([]*pb.ModelOptions(nil), backend.loadOpts...)
		backend.mu.Unlock()
		Expect(loads).To(HaveLen(2))
		Expect(loads[0].ContextSize).To(Equal(int32(8192)))
		Expect(loads[1].ContextSize).To(Equal(int32(100000)))
		Expect(loads[1].Options).To(ContainElement("parallel:4"))
		var loaded NodeModel
		Expect(db.Where("model_name = ? AND state = ?", modelName, "loaded").First(&loaded).Error).To(Succeed())
		Expect(loaded.ConfigRevision).To(Equal("rev-100k"))
	})

	It("recovers min replicas only from matching-revision replay information", func() {
		const modelName = "matching-replay"
		Expect(registry.EstablishModelConfigRevision(ctx, modelName, "rev-current")).To(Succeed())
		current, err := proto.Marshal(&pb.ModelOptions{ContextSize: 100000, Options: []string{"parallel:4"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(registry.UpsertModelLoadInfoRevision(ctx, modelName, "llama-cpp", "rev-current", current)).To(Succeed())
		Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: modelName, MinReplicas: 1, MaxReplicas: 1})).To(Succeed())

		router := NewSmartRouter(registry, SmartRouterOptions{Unloader: unloader, ClientFactory: &stubClientFactory{client: backend}})
		rc := NewReplicaReconciler(ReplicaReconcilerOptions{Registry: registry, Scheduler: router, DB: db})
		rc.reconcileModel(ctx, ModelSchedulingConfig{ModelName: modelName, MinReplicas: 1, MaxReplicas: 1})

		var loaded NodeModel
		Expect(db.Where("model_name = ? AND state = ?", modelName, "loaded").First(&loaded).Error).To(Succeed())
		Expect(loaded.ConfigRevision).To(Equal("rev-current"))
		backend.mu.Lock()
		defer backend.mu.Unlock()
		Expect(backend.loadOpts).To(HaveLen(1))
		Expect(backend.loadOpts[0].ContextSize).To(Equal(int32(100000)))
	})

	It("hashes each replica's post-default effective options on heterogeneous nodes", func() {
		const modelName = "heterogeneous-options"
		Expect(db.Model(&BackendNode{}).Where("id = ?", node.ID).Updates(map[string]any{
			"gpu_vendor": "NVIDIA", "gpu_compute_capability": "12.0", "max_replicas_per_model": 1,
		}).Error).To(Succeed())
		secondNode := &BackendNode{
			Name: "hopper-worker", NodeType: NodeTypeBackend, Address: "10.0.0.2:50051",
			GPUVendor: "NVIDIA", GPUComputeCapability: "9.0", TotalVRAM: 16_000_000_000,
			AvailableVRAM: 16_000_000_000, MaxReplicasPerModel: 1,
		}
		Expect(registry.Register(ctx, secondNode, true)).To(Succeed())
		router := NewSmartRouter(registry, SmartRouterOptions{Unloader: unloader, ClientFactory: &stubClientFactory{client: backend}})

		first, err := router.Route(ctx, modelName, "models/heterogeneous.gguf", "llama-cpp", "rev-one", &pb.ModelOptions{ContextSize: 8192, NBatch: 512}, false)
		Expect(err).NotTo(HaveOccurred())
		first.Release()
		_, err = router.ScheduleAndLoadModel(ctx, modelName, nil)
		Expect(err).NotTo(HaveOccurred())

		var replicas []NodeModel
		Expect(db.Where("model_name = ? AND state = ?", modelName, "loaded").Order("node_id").Find(&replicas).Error).To(Succeed())
		Expect(replicas).To(HaveLen(2))
		Expect(replicas[0].ConfigRevision).To(Equal("rev-one"))
		Expect(replicas[1].ConfigRevision).To(Equal("rev-one"))
		Expect(replicas[0].EffectiveOptionsHash).NotTo(BeEmpty())
		Expect(replicas[1].EffectiveOptionsHash).NotTo(BeEmpty())
		Expect(replicas[0].EffectiveOptionsHash).NotTo(Equal(replicas[1].EffectiveOptionsHash))

		backend.mu.Lock()
		loads := append([]*pb.ModelOptions(nil), backend.loadOpts...)
		backend.mu.Unlock()
		Expect(loads).To(HaveLen(2))
		Expect([]int32{loads[0].NBatch, loads[1].NBatch}).To(ConsistOf(int32(2048), int32(512)))
	})

	It("carries one immutable revision from backend options through the loader, adapter, and durable attempt", func() {
		contextSize := 10000
		cfg := config.ModelConfig{
			Name:      "full-revision-flow",
			Backend:   "llama-cpp",
			LLMConfig: config.LLMConfig{ContextSize: &contextSize},
		}
		cfg.Model = "models/full-flow.gguf"
		Expect(cfg.StampPersistedConfigRevision()).To(Succeed())
		expectedRevision := cfg.PersistedConfigRevision()

		router := NewSmartRouter(registry, SmartRouterOptions{
			Unloader:      unloader,
			ClientFactory: &stubClientFactory{client: backend},
			DB:            db,
		})
		adapter := NewModelRouterAdapter(router)
		state := &system.SystemState{}
		loader := model.NewModelLoader(state)
		loader.SetModelRouter(adapter.AsModelRouter())
		appCfg := &config.ApplicationConfig{Context: ctx, SystemState: state}
		options := corebackend.ModelOptions(cfg, appCfg)

		// Mutating the source config after ModelOptions resolved it must not alter
		// the revision captured by the durable load attempt.
		*cfg.ContextSize = 8192
		client, err := loader.Load(options...)
		Expect(err).NotTo(HaveOccurred())
		Expect(client).NotTo(BeNil())

		revision, err := registry.GetModelConfigRevision(ctx, "full-revision-flow")
		Expect(err).NotTo(HaveOccurred())
		Expect(revision).To(Equal(expectedRevision))
		var loaded NodeModel
		Expect(db.Where("model_name = ? AND state = ?", "full-revision-flow", "loaded").First(&loaded).Error).To(Succeed())
		Expect(loaded.ConfigRevision).To(Equal(expectedRevision))
		_, replayRevision, replay, err := registry.GetModelLoadInfoRevision(ctx, "full-revision-flow")
		Expect(err).NotTo(HaveOccurred())
		Expect(replayRevision).To(Equal(expectedRevision))
		var replayOpts pb.ModelOptions
		Expect(proto.Unmarshal(replay, &replayOpts)).To(Succeed())
		Expect(replayOpts.ContextSize).To(Equal(int32(10000)))
	})
})
