package nodes

import (
	"context"
	"fmt"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"time"
)

var _ = Describe("Independent Task4 probes", func() {
	It("remote waiter retention", func() {
		t := GinkgoT()
		r, err := NewNodeRegistry(testutil.SetupTestDB())
		if err != nil {
			t.Fatal(err)
		}
		router := NewSmartRouter(r, SmartRouterOptions{})
		for i := 0; i < 12; i++ {
			j, _, err := r.ClaimLoadJob(context.Background(), fmt.Sprintf("review-%d", 0), "remote-owner")
			if err != nil {
				t.Fatal(err)
			}
			ch := router.loadWaiterChan(loadWaiterKey(j.Ref()))
			if err = r.DeleteLoadJob(context.Background(), j.Ref()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			err = router.waitForLoadJob(ctx, j.TrackingKey, ch, j.Ref())
			cancel()
			if err != nil {
				t.Fatal(err)
			}
		}
		if n := len(router.loadWaiters); n != 0 {
			t.Fatalf("remote-completed generations permanently retained: %d", n)
		}
	})
})

var _ = Describe("Load recovery waiter lifecycle", func() {
	It("keeps remaining waiters registered when one concurrent request cancels", func() {
		r, err := NewNodeRegistry(testutil.SetupTestDB())
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		j, _, err := r.ClaimLoadJob(ctx, "multi", "remote")
		Expect(err).NotTo(HaveOccurred())
		router := NewSmartRouter(r, SmartRouterOptions{})
		key := loadWaiterKey(j.Ref())
		first, second := router.loadWaiterChan(key), router.loadWaiterChan(key)
		Expect(first).To(Equal(second))
		cancelCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		otherCtx, otherCancel := context.WithTimeout(ctx, 10*time.Second)
		defer otherCancel()
		one, two := make(chan error, 1), make(chan error, 1)
		go func() { one <- router.waitForLoadJob(cancelCtx, j.TrackingKey, first, j.Ref()) }()
		go func() { two <- router.waitForLoadJob(otherCtx, j.TrackingKey, second, j.Ref()) }()
		cancel()
		Eventually(one).Should(Receive(HaveOccurred()))
		router.loadWaitersMu.Lock()
		entry := router.loadWaiters[key]
		refs := entry.refs
		router.loadWaitersMu.Unlock()
		Expect(refs).To(Equal(1))
		Expect(second).NotTo(BeClosed())
		current, err := r.GetLoadJob(ctx, j.TrackingKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(current.CancelRequested).To(BeFalse())
		Expect(r.DeleteLoadJob(ctx, j.Ref())).To(Succeed())
		router.closeLoadWaiters(key)
		Expect(second).To(BeClosed())
		Eventually(two).Should(Receive(BeNil()))
		Expect(router.loadWaiters).To(BeEmpty())
	})
	It("releases remote failures and expired completion evidence without touching generation B", func() {
		r, err := NewNodeRegistry(testutil.SetupTestDB())
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		router := NewSmartRouter(r, SmartRouterOptions{})
		failed, _, err := r.ClaimLoadJob(ctx, "failed", "remote")
		Expect(err).NotTo(HaveOccurred())
		ch := router.loadWaiterChan(loadWaiterKey(failed.Ref()))
		Expect(r.FailLoadJob(ctx, failed.Ref(), "remote failure")).To(Succeed())
		Expect(router.waitForLoadJob(ctx, failed.TrackingKey, ch, failed.Ref())).To(MatchError(ContainSubstring("remote failure")))
		Expect(router.loadWaiters).To(BeEmpty())
		a, _, err := r.ClaimLoadJob(ctx, "expiry", "remote")
		Expect(err).NotTo(HaveOccurred())
		old := router.loadWaiterChan(loadWaiterKey(a.Ref()))
		Expect(r.DeleteLoadJob(ctx, a.Ref())).To(Succeed())
		Expect(r.db.Model(&LoadJobTombstone{}).Where("generation = ?", a.Generation).Update("expires_at", time.Now().Add(-time.Hour)).Error).To(Succeed())
		Expect(router.waitForLoadJob(ctx, a.TrackingKey, old, a.Ref())).To(MatchError(ErrStaleLoadJob))
		Expect(router.loadWaiters).To(BeEmpty())
		old = router.loadWaiterChan(loadWaiterKey(a.Ref()))
		b, _, err := r.ClaimLoadJob(ctx, a.TrackingKey, "replacement")
		Expect(err).NotTo(HaveOccurred())
		replacement := router.loadWaiterChan(loadWaiterKey(b.Ref()))
		Expect(router.waitForLoadJob(ctx, a.TrackingKey, old, a.Ref())).To(MatchError(ErrStaleLoadJob))
		router.closeLoadWaiters(loadWaiterKey(a.Ref()))
		Expect(replacement).NotTo(BeClosed())
		current, err := r.GetLoadJob(ctx, a.TrackingKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(current.Ref()).To(Equal(b.Ref()))
		Expect(current.CancelRequested).To(BeFalse())
		// Even reuse of the SAME key cannot let an old channel release the new one.
		key := loadWaiterKey(b.Ref())
		router.closeLoadWaiters(key)
		fresh := router.loadWaiterChan(key)
		router.releaseLoadWaiter(key, replacement)
		Expect(router.loadWaiters[key].refs).To(Equal(1))
		Expect(fresh).NotTo(BeClosed())
		router.releaseLoadWaiter(key, fresh)
		Expect(router.loadWaiters).To(BeEmpty())
	})
})

type frozenTargetProbe struct {
	recoveryStopProbe
	entered chan string
	resume  chan struct{}
}

func (p *frozenTargetProbe) ListRunningModels(node string) (*workerctl.ModelsRunningReply, error) {
	p.entered <- node
	<-p.resume
	return p.recoveryStopProbe.ListRunningModels(node)
}

var _ = Describe("Load recovery placement fencing", func() {
	It("fences owner updates after commit and dispatches only the frozen exact target", func() {
		r, err := NewNodeRegistry(testutil.SetupTestDB())
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		j, _, err := r.ClaimLoadJob(ctx, "frozen", "owner")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.UpdateLoadJob(ctx, j.Ref(), LoadJobUpdate{NodeID: "node-a"})).To(Succeed())
		p := &frozenTargetProbe{entered: make(chan string, 1), resume: make(chan struct{}), recoveryStopProbe: recoveryStopProbe{inventory: &workerctl.ModelsRunningReply{Incarnation: "boot", Models: []workerctl.RunningModelInfo{{ModelID: j.TrackingKey, Address: "address", ProcessInstance: "process", Operation: &workerctl.OperationIdentity{TrackingKey: j.TrackingKey, Generation: j.Generation, Incarnation: "boot"}}}}}}
		done := make(chan error, 1)
		go func() {
			_, err := (&LoadRecoveryService{Registry: r}).CancelOnNode(ctx, j.Ref(), "node-a", p)
			done <- err
		}()
		Eventually(p.entered).Should(Receive(Equal("node-a")))
		// Inventory starts only after commit. A second connection sees cancellation,
		// and an ordinary owner placement update cannot move this canceled work.
		current, err := r.GetLoadJob(ctx, j.TrackingKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(current.CancelRequested).To(BeTrue())
		Expect(r.UpdateLoadJob(ctx, j.Ref(), LoadJobUpdate{NodeID: "node-b"})).To(MatchError(ErrStaleLoadJob))
		close(p.resume)
		Eventually(done).Should(Receive(BeNil()))
		Expect(p.nodes).To(Equal([]string{"node-a", "node-a"}))
		Expect(p.refs).To(Equal([]LoadJobRef{j.Ref()}))
		current, err = r.GetLoadJob(ctx, j.TrackingKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(current.NodeID).To(Equal("node-a"))
	})
})
