package nodes

import (
	"context"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"time"
)

var _ = Describe("Load recovery waiters", func() {
	It("rechecks durable failure after a local wake rather than reporting success", func() {
		store := &fakeModelRouter{}
		ctx := context.Background()
		job, _, err := store.ClaimLoadJob(ctx, "wake-model", "owner")
		Expect(err).NotTo(HaveOccurred())
		router := NewSmartRouter(store, SmartRouterOptions{})
		waiter := router.loadWaiterChan(job.TrackingKey)
		Expect(store.FailLoadJob(ctx, job.Ref(), "uncertain failure")).To(Succeed())
		router.closeLoadWaiters(job.TrackingKey)
		waitCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		Expect(router.waitForLoadJob(waitCtx, job.TrackingKey, waiter)).To(MatchError(ContainSubstring("uncertain failure")))
	})
})

var _ = Describe("Load recovery cancellation", func() {
	It("persists cross-frontend intent and never cancels a replacement", func() {
		db := testutil.SetupTestDB()
		a, err := NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		b, err := NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		job, _, err := a.ClaimLoadJob(ctx, "cancel-model", "a")
		Expect(err).NotTo(HaveOccurred())
		result, err := (&LoadRecoveryService{Registry: b}).Cancel(ctx, job.Ref(), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Outcome).To(Equal(LoadUncertain))
		current, err := a.GetLoadJob(ctx, job.TrackingKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(current.CancelRequested).To(BeTrue())
		Expect(a.DeleteLoadJob(ctx, job.Ref())).To(MatchError(ErrStaleLoadJob))
		_, err = (&LoadRecoveryService{Registry: b}).Cancel(ctx, LoadJobRef{job.TrackingKey, "other"}, nil)
		Expect(err).To(MatchError(ErrLoadJobConflict))
	})
})

var _ = Describe("Load recovery tombstones", func() {
	It("retains completed generation without changing a replacement", func() {
		db := testutil.SetupTestDB()
		r, err := NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		a, _, err := r.ClaimLoadJob(ctx, "history", "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.DeleteLoadJob(ctx, a.Ref())).To(Succeed())
		b, _, err := r.ClaimLoadJob(ctx, "history", "b")
		Expect(err).NotTo(HaveOccurred())
		result, err := (&LoadRecoveryService{Registry: r}).Cancel(ctx, a.Ref(), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Outcome).To(Equal(LoadTerminalConfirmed))
		current, err := r.GetLoadJob(ctx, "history")
		Expect(err).NotTo(HaveOccurred())
		Expect(current.Ref()).To(Equal(b.Ref()))
		Expect(current.CancelRequested).To(BeFalse())
	})
	It("rejects replacement success when an old generation wakes", func() {
		store := &fakeModelRouter{}
		ctx := context.Background()
		a, _, err := store.ClaimLoadJob(ctx, "aba-wake", "a")
		Expect(err).NotTo(HaveOccurred())
		router := NewSmartRouter(store, SmartRouterOptions{})
		waiter := router.loadWaiterChan(a.TrackingKey)
		Expect(store.DeleteLoadJob(ctx, a.Ref())).To(Succeed())
		_, _, err = store.ClaimLoadJob(ctx, a.TrackingKey, "b")
		Expect(err).NotTo(HaveOccurred())
		router.closeLoadWaiters(a.TrackingKey)
		Expect(router.waitForLoadJob(ctx, a.TrackingKey, waiter, a.Ref())).To(MatchError(ErrStaleLoadJob))
	})
})

var _ = Describe("Load recovery passive wait", func() {
	It("recovers an expired owner without another request", func() {
		db := testutil.SetupTestDB()
		r, err := NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		job, _, err := r.ClaimLoadJob(ctx, "orphan-wait", "dead")
		Expect(err).NotTo(HaveOccurred())
		Expect(db.Model(&ModelLoadJob{}).Where("tracking_key = ?", job.TrackingKey).Update("last_progress", time.Now().Add(-2*loadJobOrphanWindow)).Error).To(Succeed())
		router := NewSmartRouter(r, SmartRouterOptions{})
		waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		Expect(router.waitForLoadJob(waitCtx, job.TrackingKey, router.loadWaiterChan(loadWaiterKey(job.Ref())), job.Ref())).To(MatchError(ContainSubstring("termination is not proven")))
		current, err := r.GetLoadJob(ctx, job.TrackingKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(current.TerminalUntil).NotTo(BeNil())
		Expect(current.WorkUncertain).To(BeTrue())
	})
	It("keeps generation channels distinct across a delayed old completion", func() {
		router := NewSmartRouter(&fakeModelRouter{}, SmartRouterOptions{})
		a := LoadJobRef{"same", "a"}
		b := LoadJobRef{"same", "b"}
		old := router.loadWaiterChan(loadWaiterKey(a))
		replacement := router.loadWaiterChan(loadWaiterKey(b))
		router.closeLoadWaiters(loadWaiterKey(a))
		Expect(old).To(BeClosed())
		Expect(replacement).NotTo(BeClosed())
	})
})

type recoveryStopProbe struct {
	inventory *workerctl.ModelsRunningReply
	nodes     []string
	refs      []LoadJobRef
}

func (p *recoveryStopProbe) ListRunningModels(node string) (*workerctl.ModelsRunningReply, error) {
	p.nodes = append(p.nodes, node)
	return p.inventory, nil
}
func (p *recoveryStopProbe) StopLoadOperation(_ context.Context, node string, ref LoadJobRef, boot, key, address, instance, revision string) (workerctl.ModelStopReply, error) {
	p.nodes = append(p.nodes, node)
	p.refs = append(p.refs, ref)
	return workerctl.ModelStopReply{Matched: true, Terminated: true, OperationAcknowledged: true}, nil
}

var _ = Describe("Load recovery exact cancellation", func() {
	It("stops only the matching generation and never equates a stop with admission exclusion", func() {
		r, err := NewNodeRegistry(testutil.SetupTestDB())
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		job, _, err := r.ClaimLoadJob(ctx, "exact", "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.UpdateLoadJob(ctx, job.Ref(), LoadJobUpdate{NodeID: "target"})).To(Succeed())
		probe := &recoveryStopProbe{inventory: &workerctl.ModelsRunningReply{Incarnation: "boot", Models: []workerctl.RunningModelInfo{
			{ModelID: "exact", Address: "addr", ProcessInstance: "instance", Operation: &workerctl.OperationIdentity{TrackingKey: "exact", Generation: job.Generation, Incarnation: "boot"}},
			{ModelID: "exact", Address: "replacement", ProcessInstance: "other", Operation: &workerctl.OperationIdentity{TrackingKey: "exact", Generation: "replacement", Incarnation: "boot"}},
		}}}
		result, err := (&LoadRecoveryService{Registry: r}).Cancel(ctx, job.Ref(), probe)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Outcome).To(Equal(LoadUncertain))
		Expect(probe.refs).To(Equal([]LoadJobRef{job.Ref()}))
		Expect(probe.nodes).To(Equal([]string{"target", "target"}))
		current, err := r.GetLoadJob(ctx, "exact")
		Expect(err).NotTo(HaveOccurred())
		Expect(current.WorkUncertain).To(BeTrue())
	})
})

var _ = Describe("Load recovery retry evidence", func() {
	It("does not infer confirmation after tombstone expiry", func() {
		db := testutil.SetupTestDB()
		r, err := NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		job, _, err := r.ClaimLoadJob(ctx, "expired", "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.DeleteLoadJob(ctx, job.Ref())).To(Succeed())
		Expect(db.Model(&LoadJobTombstone{}).Where("generation = ?", job.Generation).Update("expires_at", time.Now().Add(-time.Hour)).Error).To(Succeed())
		_, err = (&LoadRecoveryService{Registry: r}).Cancel(ctx, job.Ref(), nil)
		Expect(err).To(MatchError(ErrLoadJobUnknown))
	})
	It("propagates storage failures without claiming cancellation", func() {
		r, err := NewNodeRegistry(testutil.SetupTestDB())
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result, err := (&LoadRecoveryService{Registry: r}).Cancel(ctx, LoadJobRef{"m", "generation"}, nil)
		Expect(err).To(HaveOccurred())
		Expect(result.Outcome).NotTo(Equal(LoadTerminalConfirmed))
	})
	It("client disconnect leaves the job untouched", func() {
		store := &fakeModelRouter{}
		job, _, err := store.ClaimLoadJob(context.Background(), "disconnect", "owner")
		Expect(err).NotTo(HaveOccurred())
		router := NewSmartRouter(store, SmartRouterOptions{})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(router.waitForLoadJob(ctx, job.TrackingKey, router.loadWaiterChan(loadWaiterKey(job.Ref())), job.Ref())).To(HaveOccurred())
		current, err := store.GetLoadJob(context.Background(), job.TrackingKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(current.Ref()).To(Equal(job.Ref()))
		Expect(current.TerminalUntil).To(BeNil())
	})
})
