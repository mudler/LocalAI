// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("fail-soft reconcile", func() {
	It("keeps a record whose existence cannot be established and settles the rest", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "policy")
		f := open(ModeNamedUser, dir)
		owner := named("owner")
		ctx := context.Background()
		down, _ := f.policy.Register(ctx, owner, "tables")
		made, _ := f.policy.Register(ctx, owner, "fixture")
		gone, _ := f.policy.Register(ctx, owner, "fixture")
		kept, _ := f.policy.Register(ctx, owner, "fixture")
		Expect(f.policy.Commit(ctx, owner, kept)).To(Succeed())
		Expect(f.store.Close()).To(Succeed())
		g := open(ModeNamedUser, dir)
		exists := func(_ context.Context, k ResourceKey) (bool, error) {
			switch k.ResourceID {
			case down.ResourceID:
				return false, ErrUnavailable
			case made.ResourceID:
				return true, nil
			}
			return false, nil
		}
		rep, err := g.policy.ReconcileReport(ctx, exists)
		Expect(err).NotTo(HaveOccurred())
		Expect(rep).To(Equal(ReconcileReport{Settled: 2, Kept: 1}))
		pend, _ := g.policy.Pending()
		Expect(pend).To(Equal([]string{down.ResourceID}))
		Expect(allowed(g, owner, made, ActionRead)).To(BeTrue())
		Expect(allowed(g, owner, kept, ActionRead)).To(BeTrue())
		Expect(allowed(g, owner, down, ActionRead)).To(BeFalse())
		Expect(allowed(g, owner, gone, ActionRead)).To(BeFalse())
		// A later pass with an answer settles the kept record.
		Expect(g.policy.Reconcile(ctx, func(context.Context, ResourceKey) (bool, error) { return true, nil })).To(Succeed())
		Expect(allowed(g, owner, down, ActionRead)).To(BeTrue())
	})

	It("never drops a committed resource and counts pending quota unchanged", func() {
		f := open(ModeNamedUser, filepath.Join(GinkgoT().TempDir(), "policy"))
		owner := named("owner")
		ctx := context.Background()
		c, _ := f.policy.Register(ctx, owner, "tables")
		Expect(f.policy.Commit(ctx, owner, c)).To(Succeed())
		_, _ = f.policy.Register(ctx, owner, "tables")
		before, _ := f.policy.Pending()
		for i := 0; i < 3; i++ {
			Expect(f.policy.Reconcile(ctx, func(context.Context, ResourceKey) (bool, error) { return false, errors.New("down") })).To(Succeed())
		}
		after, _ := f.policy.Pending()
		Expect(after).To(Equal(before))
		Expect(allowed(f, owner, c, ActionRead)).To(BeTrue())
	})

	It("retries kept records from the loop on a tick and on a kick, and stops on Close", func() {
		f := open(ModeNamedUser, filepath.Join(GinkgoT().TempDir(), "policy"))
		owner := named("owner")
		ctx := context.Background()
		a, _ := f.policy.Register(ctx, owner, "tables")
		b, _ := f.policy.Register(ctx, owner, "tables")
		ready := false
		exists := func(context.Context, ResourceKey) (bool, error) {
			if !ready {
				return false, ErrUnavailable
			}
			return true, nil
		}
		ticks := make(chan time.Time)
		reports := make(chan ReconcileReport, 4)
		l := NewReconcileLoop(f.policy, exists, ticks)
		l.Report = func(r ReconcileReport, _ error) { reports <- r }
		l.Start(ctx)
		ticks <- time.Time{}
		Expect(<-reports).To(Equal(ReconcileReport{Kept: 2}))
		ready = true
		l.Kick()
		Expect(<-reports).To(Equal(ReconcileReport{Settled: 2}))
		Expect(allowed(f, owner, a, ActionRead)).To(BeTrue())
		Expect(allowed(f, owner, b, ActionRead)).To(BeTrue())
		// Nothing pending: a tick does no work and reports nothing.
		ticks <- time.Time{}
		l.Close()
		Expect(reports).To(BeEmpty())
		l.Close()
	})

	It("closes a loop that never started without waiting", func() {
		f := open(ModeNamedUser, filepath.Join(GinkgoT().TempDir(), "policy"))
		NewReconcileLoop(f.policy, nil, nil).Close()
	})
})
