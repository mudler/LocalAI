// SPDX-License-Identifier: MIT
package nodes

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Load recovery generation", func() {
	It("does not treat an expired lease as proof remote work stopped", func() {
		db := testutil.SetupTestDB()
		registry, err := NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		_, claimed, err := registry.ClaimLoadJob(ctx, "synthetic-model", "frontend-a")
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(BeTrue())
		Expect(db.Model(&ModelLoadJob{}).Where("tracking_key = ?", "synthetic-model").Update("last_progress", time.Now().Add(-2*loadJobOrphanWindow)).Error).To(Succeed())
		job, claimed, err := registry.ClaimLoadJob(ctx, "synthetic-model", "frontend-b")
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(BeFalse(), "lease expiry cannot authorize overlapping remote work")
		Expect(job.OwnerReplica).To(Equal("frontend-a"))
	})

	It("fences delayed A writes after B and another A claim", func() {
		db := testutil.SetupTestDB()
		registry, err := NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		a, _, err := registry.ClaimLoadJob(ctx, "aba-model", "frontend-a")
		Expect(err).NotTo(HaveOccurred())
		Expect(a.Generation).NotTo(BeEmpty())
		Expect(registry.DeleteLoadJob(ctx, a.Ref())).To(Succeed())
		b, _, err := registry.ClaimLoadJob(ctx, "aba-model", "frontend-b")
		Expect(err).NotTo(HaveOccurred())
		Expect(registry.DeleteLoadJob(ctx, b.Ref())).To(Succeed())
		replacement, claimed, err := registry.ClaimLoadJob(ctx, "aba-model", "frontend-a")
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(BeTrue())
		Expect(replacement.Generation).NotTo(Equal(a.Generation))
		for _, stale := range []LoadJobRef{a.Ref(), b.Ref(), {TrackingKey: "aba-model"}} {
			Expect(registry.UpdateLoadJob(ctx, stale, LoadJobUpdate{State: LoadJobStateLoading})).To(MatchError(ErrStaleLoadJob))
			Expect(registry.FailLoadJob(ctx, stale, "late failure")).To(MatchError(ErrStaleLoadJob))
			Expect(registry.DeleteLoadJob(ctx, stale)).To(MatchError(ErrStaleLoadJob))
		}
		current, err := registry.GetLoadJob(ctx, "aba-model")
		Expect(err).NotTo(HaveOccurred())
		Expect(current.Ref()).To(Equal(replacement.Ref()))
		Expect(current.State).To(Equal(LoadJobStatePending))
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		writeErr := registry.UpdateLoadJob(cancelled, replacement.Ref(), LoadJobUpdate{})
		Expect(writeErr).To(HaveOccurred())
		Expect(writeErr).NotTo(MatchError(ErrStaleLoadJob))
	})

	It("quarantines legacy rows rather than assigning them apparent ownership", func() {
		db := testutil.SetupTestDB()
		registry, err := NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		legacy := ModelLoadJob{TrackingKey: "legacy-model", State: LoadJobStateStaging, OwnerReplica: "legacy-frontend", LastProgress: time.Now().Add(-24 * time.Hour)}
		Expect(db.Create(&legacy).Error).To(Succeed())
		job, claimed, err := registry.ClaimLoadJob(ctx, legacy.TrackingKey, "new-frontend")
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(BeFalse())
		Expect(job.Generation).To(BeEmpty())
		Expect(registry.UpdateLoadJob(ctx, job.Ref(), LoadJobUpdate{})).To(MatchError(ErrStaleLoadJob))
		Expect(registry.FailLoadJob(ctx, job.Ref(), "failure")).To(MatchError(ErrStaleLoadJob))
		Expect(registry.DeleteLoadJob(ctx, job.Ref())).To(MatchError(ErrStaleLoadJob))
	})

	It("retains failure and uncertainty durably without relying on a grace timer", func() {
		db := testutil.SetupTestDB()
		registry, err := NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		job, _, err := registry.ClaimLoadJob(ctx, "terminal-model", "frontend-a")
		Expect(err).NotTo(HaveOccurred())
		Expect(registry.FailLoadJob(ctx, job.Ref(), "remote outcome unknown")).To(Succeed())
		restarted, err := NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		terminal, claimed, err := restarted.ClaimLoadJob(ctx, job.TrackingKey, "frontend-b")
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(BeFalse())
		Expect(terminal.TerminalUntil).NotTo(BeNil())
		Expect(terminal.LastError).To(Equal("remote outcome unknown"))
		Expect(registry.UpdateLoadJob(ctx, job.Ref(), LoadJobUpdate{})).To(MatchError(ErrStaleLoadJob))
		Expect(registry.DeleteLoadJob(ctx, job.Ref())).To(MatchError(ErrStaleLoadJob))
		Expect(db.Model(&ModelLoadJob{}).Where("tracking_key = ?", job.TrackingKey).Update("terminal_until", time.Now().Add(-time.Hour)).Error).To(Succeed())
		_, claimed, err = restarted.ClaimLoadJob(ctx, job.TrackingKey, "frontend-b")
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(BeFalse(), "expired grace does not prove remote termination")
		// Stand in for future operation reconciliation proving remote work stopped.
		Expect(db.Model(&ModelLoadJob{}).Where("tracking_key = ? AND generation = ?", job.TrackingKey, job.Generation).Update("work_uncertain", false).Error).To(Succeed())
		replacement, claimed, err := restarted.ClaimLoadJob(ctx, job.TrackingKey, "frontend-b")
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(BeTrue())
		Expect(replacement.Generation).NotTo(Equal(job.Generation))
		Expect(registry.DeleteLoadJob(ctx, job.Ref())).To(MatchError(ErrStaleLoadJob))
	})

	It("does not wake replacement waiters on a delayed successful completion", func() {
		store := &fakeModelRouter{}
		ctx := context.Background()
		a, _, err := store.ClaimLoadJob(ctx, "runner-model", "frontend-a")
		Expect(err).NotTo(HaveOccurred())
		Expect(store.DeleteLoadJob(ctx, a.Ref())).To(Succeed())
		b, _, err := store.ClaimLoadJob(ctx, "runner-model", "frontend-b")
		Expect(err).NotTo(HaveOccurred())
		router := NewSmartRouter(store, SmartRouterOptions{})
		waiter := router.loadWaiterChan("runner-model")
		router.finishLoadJob(ctx, a.Ref())
		select {
		case <-waiter:
			Fail("stale completion woke replacement waiter")
		default:
		}
		current, err := store.GetLoadJob(ctx, "runner-model")
		Expect(err).NotTo(HaveOccurred())
		Expect(current.Ref()).To(Equal(b.Ref()))
		router.finishLoadJob(ctx, b.Ref())
		Eventually(waiter).Should(BeClosed())
	})
})
