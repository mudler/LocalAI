// SPDX-License-Identifier: MIT
package nodes

import (
	"context"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These fixtures join real durable ownership, runner, reconciliation, and waiter
// APIs. Only backend work is mocked; the blocked call deliberately ignores ctx.
var _ = Describe("Load recovery end to end", func() {
	for _, cause := range []string{"restart", "deadline", "remote cancel", "legacy deadline"} {
		It("quarantines a pending call after "+cause+" and fences its delayed success", func() {
			ctx := context.Background()
			db := testutil.SetupTestDB()
			owner, err := NewNodeRegistry(db)
			Expect(err).NotTo(HaveOccurred())
			observer, err := NewNodeRegistry(db)
			Expect(err).NotTo(HaveOccurred())
			boot := "boot-one"
			if cause == "legacy deadline" {
				boot = ""
			}
			node := &BackendNode{Name: "worker", Address: "127.0.0.1:1234", NodeType: NodeTypeBackend, WorkerIncarnation: boot, TotalVRAM: 64_000_000_000, AvailableVRAM: 64_000_000_000}
			Expect(owner.Register(ctx, node, true)).To(Succeed())
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			backend := &stubBackend{healthResult: true, loadResult: &pb.Result{Success: true}, loadHook: func(*pb.ModelOptions) { close(entered); <-release }}
			router := NewSmartRouter(owner, SmartRouterOptions{DB: db, Unloader: &fakeUnloader{installReply: &workerctl.BackendInstallReply{Success: true, Address: node.Address}}, ClientFactory: &stubClientFactory{client: backend}})
			router.modelLoadCeiling = 45 * time.Minute
			if cause == "deadline" || cause == "legacy deadline" {
				router.modelLoadCeiling = 2 * time.Second
				// Entering checkpoint loading extends the phase budget; cap the
				// entire attempt to exercise actual deadline expiry in seconds.
				router.modelLoadAbsoluteMax = 2 * time.Second
			}
			job, claimed, err := owner.ClaimLoadJob(ctx, "model", "frontend-a")
			Expect(err).NotTo(HaveOccurred())
			Expect(claimed).To(BeTrue())
			DeferCleanup(func() {
				once.Do(func() { close(release) })
				Eventually(func() int { return len(coldLoadAdmission) }, 10*time.Second).Should(BeZero())
			})
			router.startLoadJob(ctx, &routeAttempt{trackingKey: job.TrackingKey, modelName: "model", backendType: "backend", modelOpts: &pb.ModelOptions{Model: "model"}}, job.Ref())
			Eventually(entered, 10*time.Second).Should(BeClosed())
			// Old StartedAt is not an expired owner lease: no duplicate admission.
			Expect(db.Model(&ModelLoadJob{}).Where("tracking_key = ?", job.TrackingKey).Update("started_at", time.Now().Add(-96*time.Hour)).Error).To(Succeed())
			result, err := (&LoadRecoveryService{Registry: observer}).Reconcile(ctx, job.Ref())
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Outcome).To(Equal(LoadStillActive))
			_, claimed, err = observer.ClaimLoadJob(ctx, job.TrackingKey, "frontend-b")
			Expect(err).NotTo(HaveOccurred())
			Expect(claimed).To(BeFalse())
			switch cause {
			case "restart":
				Expect(owner.Heartbeat(ctx, node.ID, &HeartbeatUpdate{WorkerIncarnation: "boot-two"})).To(Succeed())
			case "remote cancel":
				result, err = (&LoadRecoveryService{Registry: observer}).Cancel(ctx, job.Ref(), nil)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Outcome).To(Equal(LoadUncertain))
			}
			Eventually(func() string {
				j, e := observer.GetLoadJob(ctx, job.TrackingKey)
				if e != nil {
					return e.Error()
				}
				return j.State
			}, 10*time.Second).Should(Equal(LoadJobStateFailed))
			failed, err := observer.GetLoadJob(ctx, job.TrackingKey)
			Expect(err).NotTo(HaveOccurred())
			Expect(failed.WorkUncertain).To(BeTrue())
			Expect(len(coldLoadAdmission)).To(Equal(1), "uncooperative work still holds admission")
			otherRouter := NewSmartRouter(observer, SmartRouterOptions{})
			waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			Expect(otherRouter.waitForLoadJob(waitCtx, job.TrackingKey, otherRouter.loadWaiterChan(loadWaiterKey(job.Ref())), job.Ref())).To(HaveOccurred())
			once.Do(func() { close(release) })
			Eventually(func() int { return len(coldLoadAdmission) }, 10*time.Second).Should(BeZero())
			replica, err := observer.GetNodeModel(ctx, node.ID, job.TrackingKey, 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(replica.State).NotTo(Equal("loaded"))
			Expect(db.Model(&ModelLoadJob{}).Where("tracking_key = ?", job.TrackingKey).Update("terminal_until", time.Now().Add(-time.Hour)).Error).To(Succeed())
			_, claimed, err = observer.ClaimLoadJob(ctx, job.TrackingKey, "frontend-b")
			Expect(err).NotTo(HaveOccurred())
			Expect(claimed).To(BeFalse(), "neither elapsed grace nor returned RPC proves safe replacement")
			backend.mu.Lock()
			calls := len(backend.loadOpts)
			backend.mu.Unlock()
			Expect(calls).To(Equal(1))
		})
	}
	It("passively quarantines migrated legacy and owned orphans without replicas or inference", func() {
		ctx := context.Background()
		db := testutil.SetupTestDB()
		registry, err := NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		for _, generation := range []string{"", "owned-generation"} {
			job := ModelLoadJob{TrackingKey: "orphan-" + generation, Generation: generation, State: LoadJobStateStaging, LastProgress: time.Now().Add(-96 * time.Hour)}
			Expect(db.Create(&job).Error).To(Succeed())
		}
		restarted, err := NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		NewReplicaReconciler(ReplicaReconcilerOptions{Registry: restarted, DB: db}).reclaimAbandonedLoads(ctx)
		for _, generation := range []string{"", "owned-generation"} {
			job, claimed, err := registry.ClaimLoadJob(ctx, "orphan-"+generation, "new-owner")
			Expect(err).NotTo(HaveOccurred())
			Expect(claimed).To(BeFalse())
			Expect(job.State).To(Equal(LoadJobStateFailed))
			Expect(job.WorkUncertain).To(BeTrue())
			result, err := (&LoadRecoveryService{Registry: restarted}).Reconcile(ctx, job.Ref())
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Outcome).To(Equal(LoadUncertain))
		}
		var count int64
		Expect(db.Model(&NodeModel{}).Count(&count).Error).To(Succeed())
		Expect(count).To(BeZero())
	})
	It("publishes only the authorized generation across completion and same-key reuse", func() {
		ctx := context.Background()
		registry, err := NewNodeRegistry(testutil.SetupTestDB())
		Expect(err).NotTo(HaveOccurred())
		router := NewSmartRouter(registry, SmartRouterOptions{})
		a, _, err := registry.ClaimLoadJob(ctx, "aba", "a")
		Expect(err).NotTo(HaveOccurred())
		aCtx := context.WithValue(ctx, loadOwnershipKey{}, a.Ref())
		Expect(registry.SetNodeModel(aCtx, "node", "aba", 0, "loaded", "first", 0)).To(Succeed())
		router.finishLoadJob(ctx, a.Ref())
		Expect(registry.RemoveNodeModel(ctx, "node", "aba", 0)).To(Succeed())
		b, claimed, err := registry.ClaimLoadJob(ctx, "aba", "b")
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(BeTrue())
		waiter := router.loadWaiterChan(loadWaiterKey(b.Ref()))
		router.finishLoadJob(ctx, a.Ref())
		Expect(waiter).NotTo(BeClosed())
		Expect(registry.SetNodeModel(aCtx, "node", "aba", 0, "loaded", "late", 0)).To(MatchError(ErrStaleLoadJob))
		bCtx := context.WithValue(ctx, loadOwnershipKey{}, b.Ref())
		Expect(registry.SetNodeModel(bCtx, "node", "aba", 0, "loaded", "second", 0)).To(Succeed())
		router.finishLoadJob(ctx, b.Ref())
		Expect(waiter).To(BeClosed())
		replica, err := registry.GetNodeModel(ctx, "node", "aba", 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(replica.Address).To(Equal("second"))
	})
})
