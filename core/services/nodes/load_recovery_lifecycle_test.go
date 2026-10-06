package nodes

import (
	"context"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sync"
	"time"
)

var _ = Describe("Conservative load recovery lifecycle", func() {
	It("passively fails a four-day orphan without a replica and blocks replacement", func() {
		db := testutil.SetupTestDB()
		registry, err := NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		job := ModelLoadJob{TrackingKey: "orphan-model", Generation: "generation", State: LoadJobStateStaging, LastProgress: time.Now().Add(-96 * time.Hour)}
		Expect(db.Create(&job).Error).To(Succeed())
		rc := NewReplicaReconciler(ReplicaReconcilerOptions{Registry: registry, DB: db})
		rc.reclaimAbandonedLoads(context.Background())
		actual, err := registry.GetLoadJob(context.Background(), job.TrackingKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(actual.State).To(Equal(LoadJobStateFailed))
		Expect(actual.WorkUncertain).To(BeTrue())
		_, claimed, err := registry.ClaimLoadJob(context.Background(), job.TrackingKey, "new-owner")
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(BeFalse())
	})
	It("rejects late publication from a failed generation transactionally", func() {
		db := testutil.SetupTestDB()
		registry, err := NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		job, _, err := registry.ClaimLoadJob(context.Background(), "late-model", "owner")
		Expect(err).NotTo(HaveOccurred())
		Expect(registry.FailLoadJob(context.Background(), job.Ref(), "deadline")).To(Succeed())
		ctx := context.WithValue(context.Background(), loadOwnershipKey{}, job.Ref())
		err = registry.SetNodeModel(ctx, "node", "late-model", 0, "loaded", "address", 0)
		Expect(err).To(MatchError(ErrStaleLoadJob))
	})
	It("stops heartbeat writes after attempt cancellation", func() {
		store := &fakeModelRouter{}
		job, _, err := store.ClaimLoadJob(context.Background(), "cancelled", "owner")
		Expect(err).NotTo(HaveOccurred())
		router := NewSmartRouter(store, SmartRouterOptions{})
		ctx, cancel := context.WithCancel(context.Background())
		stop := router.startLoadJobHeartbeat(ctx, job.Ref(), newLoadPhaseReporter())
		defer stop()
		cancel()
		Consistently(func() time.Time {
			actual, _ := store.GetLoadJob(context.Background(), "cancelled")
			return actual.LastProgress
		}, 2*loadJobHeartbeatInterval).Should(Equal(job.LastProgress))
	})

	It("fails promptly on a known boot change while a 45-minute load survives, then fences its completion", func() {
		db := testutil.SetupTestDB()
		registry, err := NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		node := &BackendNode{Name: "restart-worker", Address: "127.0.0.1:1234", NodeType: NodeTypeBackend, WorkerIncarnation: "boot-one", TotalVRAM: 64_000_000_000, AvailableVRAM: 64_000_000_000}
		Expect(registry.Register(context.Background(), node, true)).To(Succeed())
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		backend := &stubBackend{healthResult: true, loadResult: &pb.Result{Success: true}, loadHook: func(*pb.ModelOptions) { close(entered); <-release }}
		router := NewSmartRouter(registry, SmartRouterOptions{DB: db, Unloader: &fakeUnloader{installReply: &workerctl.BackendInstallReply{Success: true, Address: "127.0.0.1:1234"}}, ClientFactory: &stubClientFactory{client: backend}})
		router.modelLoadCeiling = 45 * time.Minute
		job, _, err := registry.ClaimLoadJob(context.Background(), "restart-model", "owner")
		Expect(err).NotTo(HaveOccurred())
		router.startLoadJob(context.Background(), &routeAttempt{trackingKey: "restart-model", modelName: "synthetic.gguf", backendType: "backend", modelOpts: &pb.ModelOptions{Model: "synthetic.gguf"}}, job.Ref())
		DeferCleanup(func() {
			once.Do(func() { close(release) })
			Eventually(func() int { return len(coldLoadAdmission) }, 10*time.Second).Should(Equal(0))
		})
		Eventually(entered, 10*time.Second).Should(BeClosed())
		Expect(registry.Heartbeat(context.Background(), node.ID, &HeartbeatUpdate{WorkerIncarnation: "boot-two"})).To(Succeed())
		Eventually(func() string { j, _ := registry.GetLoadJob(context.Background(), job.TrackingKey); return j.State }, 10*time.Second).Should(Equal(LoadJobStateFailed))
		failed, err := registry.GetLoadJob(context.Background(), job.TrackingKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(failed.WorkUncertain).To(BeTrue())
		Consistently(func() time.Time {
			j, _ := registry.GetLoadJob(context.Background(), job.TrackingKey)
			return j.LastProgress
		}, 2*time.Second).Should(Equal(failed.LastProgress))
		once.Do(func() { close(release) })
		Eventually(func() int { return len(coldLoadAdmission) }, 10*time.Second).Should(Equal(0))
		replica, err := registry.GetNodeModel(context.Background(), node.ID, job.TrackingKey, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(replica.State).NotTo(Equal("loaded"))
	})
	It("keeps a live zero-byte load and reports DB errors as uncertain", func() {
		db := testutil.SetupTestDB()
		registry, err := NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		job, _, err := registry.ClaimLoadJob(context.Background(), "valid-model", "owner")
		Expect(err).NotTo(HaveOccurred())
		service := &LoadRecoveryService{Registry: registry}
		result, err := service.Reconcile(context.Background(), job.Ref())
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Outcome).To(Equal(LoadStillActive))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result, err = service.Reconcile(ctx, job.Ref())
		Expect(err).To(HaveOccurred())
		Expect(result.Outcome).To(Equal(LoadUncertain))
		actual, err := registry.GetLoadJob(context.Background(), job.TrackingKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(actual.TerminalUntil).To(BeNil())
	})

	It("fails on its ordinary deadline even when install ignores cancellation", func() {
		db := testutil.SetupTestDB()
		registry, err := NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		node := &BackendNode{Name: "deadline-worker", NodeType: NodeTypeBackend, Address: "127.0.0.1:1234", TotalVRAM: 64_000_000_000, AvailableVRAM: 64_000_000_000}
		Expect(registry.Register(context.Background(), node, true)).To(Succeed())
		entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var once sync.Once
		unloader := &fakeUnloader{installReply: &workerctl.BackendInstallReply{Success: true, Address: node.Address}, installHook: func() { close(entered); <-release; close(returned) }}
		router := NewSmartRouter(registry, SmartRouterOptions{DB: db, Unloader: unloader, ClientFactory: &stubClientFactory{client: &stubBackend{healthResult: true}}})
		router.modelLoadCeiling = 150 * time.Millisecond
		job, _, err := registry.ClaimLoadJob(context.Background(), "deadline-model", "owner")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			once.Do(func() { close(release) })
			Eventually(returned, 5*time.Second).Should(BeClosed())
			Eventually(func() int { return len(coldLoadAdmission) }, 5*time.Second).Should(Equal(0))
		})
		router.startLoadJob(context.Background(), &routeAttempt{trackingKey: job.TrackingKey, modelName: "model", backendType: "backend", modelOpts: &pb.ModelOptions{Model: "model"}}, job.Ref())
		Eventually(entered, 5*time.Second).Should(BeClosed())
		Eventually(func() string { j, _ := registry.GetLoadJob(context.Background(), job.TrackingKey); return j.State }, 5*time.Second).Should(Equal(LoadJobStateFailed))
		actual, err := registry.GetLoadJob(context.Background(), job.TrackingKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(actual.WorkUncertain).To(BeTrue())
		Expect(returned).NotTo(BeClosed())
	})

})
