// Package coordinatortest is the contract suite every coordinator.Store
// passes. Run registers Ginkgo specs, so call it inside a Describe of the
// implementation's test package.
package coordinatortest

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/coordinator"
)

// NewStore returns an empty store. Each spec calls it once; clean up with
// DeferCleanup.
type NewStore func() coordinator.Store

var t0 = time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)

// Sample is a valid config for an agent.
func Sample(agentID, principal string) coordinator.Config {
	return coordinator.Config{
		AgentID: agentID, Principal: principal, Page: "board",
		Contacts: []coordinator.Contact{
			{ID: "ops", Name: "Ops", Description: "the ops team", Kind: coordinator.ContactExternal, Binding: "b1", Chat: "c1", Sender: "s1"},
			{ID: "boss", Name: "Boss", Kind: coordinator.ContactUser, User: "u9"},
		},
		WakeEvery: time.Hour, WakeNote: "check the board",
		SendPerMinute: 5, SendPerDay: 50, MaxHelpers: 6, MaxDepth: 2,
		Created: t0, Updated: t0,
	}
}

// Run registers the specs that check the store.
func Run(newStore NewStore) {
	ctx := context.Background()

	It("RoundTrip", func() {
		s := newStore()
		c := Sample("a1", "u1")
		Expect(s.Put(ctx, c)).To(Succeed())
		got, err := s.Get(ctx, "a1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(c))
		list, err := s.List(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(Equal([]coordinator.Config{c}))
		_, err = s.Get(ctx, "nope")
		Expect(err).To(MatchError(coordinator.ErrNotFound))
	})

	It("PutRefusesAnInvalidConfig", func() {
		s := newStore()
		c := Sample("a1", "u1")
		c.WakeEvery = time.Minute
		Expect(s.Put(ctx, c)).To(MatchError(coordinator.ErrInvalid))
	})

	It("PutKeepsRuntimeState", func() {
		s := newStore()
		Expect(s.Put(ctx, Sample("a1", "u1"))).To(Succeed())
		_, err := s.SetChat(ctx, "a1", "chat-1")
		Expect(err).NotTo(HaveOccurred())
		_, _, err = s.SetPaused(ctx, "a1", true, "admin", t0)
		Expect(err).NotTo(HaveOccurred())
		next := t0.Add(time.Hour)
		ok, err := s.ClaimWake(ctx, "a1", t0, next)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())

		edited := Sample("a1", "u1")
		edited.WakeNote = "new note"
		edited.Created = t0.Add(48 * time.Hour)
		edited.Updated = t0.Add(time.Minute)
		Expect(s.Put(ctx, edited)).To(Succeed())
		got, err := s.Get(ctx, "a1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.WakeNote).To(Equal("new note"))
		Expect(got.Updated).To(Equal(t0.Add(time.Minute)))
		Expect(got.ChatID).To(Equal("chat-1"))
		Expect(got.Paused).To(BeTrue())
		Expect(got.PausedBy).To(Equal("admin"))
		Expect(got.PausedAt).To(Equal(t0))
		Expect(got.NextWake).To(Equal(next))
		Expect(got.LastWake).To(Equal(t0))
		Expect(got.Created).To(Equal(t0))
	})

	It("SetChatFirstWins", func() {
		s := newStore()
		Expect(s.Put(ctx, Sample("a1", "u1"))).To(Succeed())
		a, err := s.SetChat(ctx, "a1", "first")
		Expect(err).NotTo(HaveOccurred())
		b, err := s.SetChat(ctx, "a1", "second")
		Expect(err).NotTo(HaveOccurred())
		Expect(a).To(Equal("first"))
		Expect(b).To(Equal("first"))
		_, err = s.SetChat(ctx, "missing", "x")
		Expect(err).To(MatchError(coordinator.ErrNotFound))
	})

	It("ReplaceChatOnlyReplacesTheExpectedOne", func() {
		s := newStore()
		Expect(s.Put(ctx, Sample("a1", "u1"))).To(Succeed())
		_, _ = s.SetChat(ctx, "a1", "old")
		got, err := s.ReplaceChat(ctx, "a1", "other", "new")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("old"), "the stored chat is not the expected one: nothing changes")
		got, err = s.ReplaceChat(ctx, "a1", "old", "new")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("new"))
		got, err = s.ReplaceChat(ctx, "a1", "old", "again")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("new"), "a second caller that saw the same dead chat loses")
		_, err = s.ReplaceChat(ctx, "missing", "a", "b")
		Expect(err).To(MatchError(coordinator.ErrNotFound))
	})

	It("SetPausedReportsChange", func() {
		s := newStore()
		Expect(s.Put(ctx, Sample("a1", "u1"))).To(Succeed())
		c, changed, err := s.SetPaused(ctx, "a1", true, "admin", t0)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())
		Expect(c.Paused).To(BeTrue())
		Expect(c.PausedBy).To(Equal("admin"))
		_, changed, err = s.SetPaused(ctx, "a1", true, "other", t0)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeFalse())
		c, _ = s.Get(ctx, "a1")
		Expect(c.PausedBy).To(Equal("admin"), "a second pause does not change who paused")
		c, changed, err = s.SetPaused(ctx, "a1", false, "admin", t0)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())
		Expect(c.Paused).To(BeFalse())
		Expect(c.PausedBy).To(BeEmpty())
		Expect(c.PausedAt.IsZero()).To(BeTrue())
		_, _, err = s.SetPaused(ctx, "missing", true, "x", t0)
		Expect(err).To(MatchError(coordinator.ErrNotFound))
	})

	It("ClaimWakeIsConditional", func() {
		s := newStore()
		Expect(s.Put(ctx, Sample("a1", "u1"))).To(Succeed())
		next := t0.Add(time.Hour)
		ok, err := s.ClaimWake(ctx, "a1", t0, next)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		ok, _ = s.ClaimWake(ctx, "a1", t0, next)
		Expect(ok).To(BeFalse(), "NextWake is in the future now")
		ok, _ = s.ClaimWake(ctx, "a1", next, next.Add(time.Hour))
		Expect(ok).To(BeTrue(), "claimable again at NextWake")

		Expect(s.Put(ctx, Sample("a2", "u1"))).To(Succeed())
		var wins atomic.Int32
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				ok, err := s.ClaimWake(ctx, "a2", t0, next)
				Expect(err).NotTo(HaveOccurred())
				if ok {
					wins.Add(1)
				}
			}()
		}
		wg.Wait()
		Expect(wins.Load()).To(Equal(int32(1)))
	})

	It("ReserveStopsAtTheLimit", func() {
		s := newStore()
		Expect(s.Put(ctx, Sample("a1", "u1"))).To(Succeed())
		for range 3 {
			ok, err := s.Reserve(ctx, "a1", "send:2026-11-08", 3)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
		}
		ok, err := s.Reserve(ctx, "a1", "send:2026-11-08", 3)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
		Expect(s.Used(ctx, "a1", "send:2026-11-08")).To(Equal(3))
	})

	It("ReserveIsAtomic", func() {
		s := newStore()
		Expect(s.Put(ctx, Sample("a1", "u1"))).To(Succeed())
		var wins atomic.Int32
		var wg sync.WaitGroup
		for range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				ok, err := s.Reserve(ctx, "a1", "runs:2026-11-08", 5)
				Expect(err).NotTo(HaveOccurred())
				if ok {
					wins.Add(1)
				}
			}()
		}
		wg.Wait()
		Expect(wins.Load()).To(Equal(int32(5)))
		Expect(s.Used(ctx, "a1", "runs:2026-11-08")).To(Equal(5))
	})

	It("BucketsAreIndependent", func() {
		s := newStore()
		Expect(s.Put(ctx, Sample("a1", "u1"))).To(Succeed())
		Expect(s.Put(ctx, Sample("a2", "u1"))).To(Succeed())
		ok, _ := s.Reserve(ctx, "a1", "b1", 1)
		Expect(ok).To(BeTrue())
		ok, _ = s.Reserve(ctx, "a1", "b1", 1)
		Expect(ok).To(BeFalse())
		ok, _ = s.Reserve(ctx, "a1", "b2", 1)
		Expect(ok).To(BeTrue(), "another bucket")
		ok, _ = s.Reserve(ctx, "a2", "b1", 1)
		Expect(ok).To(BeTrue(), "another agent")
		Expect(s.Used(ctx, "a2", "never")).To(Equal(0))
	})

	It("DeleteRemovesTheRowAndItsCounters", func() {
		s := newStore()
		Expect(s.Put(ctx, Sample("a1", "u1"))).To(Succeed())
		Expect(s.Put(ctx, Sample("a2", "u1"))).To(Succeed())
		_, _ = s.Reserve(ctx, "a1", "b1", 5)
		_, _ = s.Reserve(ctx, "a2", "b1", 5)
		Expect(s.Delete(ctx, "a1")).To(Succeed())
		_, err := s.Get(ctx, "a1")
		Expect(err).To(MatchError(coordinator.ErrNotFound))
		Expect(s.Used(ctx, "a1", "b1")).To(Equal(0))
		Expect(s.Used(ctx, "a2", "b1")).To(Equal(1))
		Expect(s.Delete(ctx, "a1")).To(MatchError(coordinator.ErrNotFound))
	})

	addAudit := func(s coordinator.Store, e coordinator.AuditEntry) {
		GinkgoHelper()
		Expect(s.AddAudit(ctx, e)).To(Succeed())
	}
	entry := func(coordID, principal, kind, outcome string, at time.Time) coordinator.AuditEntry {
		return coordinator.AuditEntry{At: at, CoordinatorID: coordID, Principal: principal, Kind: kind, Outcome: outcome, Reason: coordinator.ReasonOK}
	}

	It("AuditNewestFirstWithCursor", func() {
		s := newStore()
		for i := range 5 {
			addAudit(s, entry("a1", "u1", coordinator.KindContact, coordinator.OutcomeSent, t0.Add(time.Duration(i)*time.Minute)))
		}
		page, err := s.Audit(ctx, coordinator.AuditQuery{Limit: 2})
		Expect(err).NotTo(HaveOccurred())
		Expect(page).To(HaveLen(2))
		Expect(page[0].ID).To(BeNumerically(">", page[1].ID))
		Expect(page[0].At).To(Equal(t0.Add(4 * time.Minute)))
		next, err := s.Audit(ctx, coordinator.AuditQuery{Limit: 2, Before: page[1].ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(next).To(HaveLen(2))
		Expect(next[0].ID).To(BeNumerically("<", page[1].ID))
		Expect(next[0].At).To(Equal(t0.Add(2 * time.Minute)))
		Expect(next[1].At).To(Equal(t0.Add(1 * time.Minute)))
		all, err := s.Audit(ctx, coordinator.AuditQuery{})
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(HaveLen(5), "Limit 0 is a page of 50")
	})

	It("AuditKeepsEveryField", func() {
		s := newStore()
		e := coordinator.AuditEntry{
			At: t0, CoordinatorID: "a1", Principal: "u1", Kind: coordinator.KindContact, Outcome: coordinator.OutcomeDenied,
			Reason: coordinator.ReasonNotAContact, Actor: "coordinator", Target: "ops", Via: "connector", Length: 42, Hash: "abc",
		}
		addAudit(s, e)
		got, err := s.Audit(ctx, coordinator.AuditQuery{})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1))
		e.ID = got[0].ID
		Expect(e.ID).NotTo(BeZero())
		Expect(got[0]).To(Equal(e))
	})

	It("AuditAtZeroIsNow", func() {
		s := newStore()
		before := time.Now().Add(-time.Second)
		addAudit(s, coordinator.AuditEntry{CoordinatorID: "a1", Kind: coordinator.KindWake, Outcome: coordinator.OutcomeInfo})
		got, err := s.Audit(ctx, coordinator.AuditQuery{})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1))
		Expect(got[0].At).To(BeTemporally(">", before))
	})

	It("AuditFilters", func() {
		s := newStore()
		addAudit(s, entry("a1", "u1", coordinator.KindContact, coordinator.OutcomeSent, t0))
		addAudit(s, entry("a1", "u1", coordinator.KindContact, coordinator.OutcomeDenied, t0.Add(time.Hour)))
		addAudit(s, entry("a2", "u2", coordinator.KindPause, coordinator.OutcomeInfo, t0.Add(2*time.Hour)))
		addAudit(s, entry("a2", "u2", coordinator.KindWake, coordinator.OutcomeInfo, t0.Add(3*time.Hour)))
		count := func(q coordinator.AuditQuery) int {
			GinkgoHelper()
			got, err := s.Audit(ctx, q)
			Expect(err).NotTo(HaveOccurred())
			return len(got)
		}
		Expect(count(coordinator.AuditQuery{CoordinatorID: "a1"})).To(Equal(2))
		Expect(count(coordinator.AuditQuery{Principal: "u2"})).To(Equal(2))
		Expect(count(coordinator.AuditQuery{Kind: coordinator.KindContact})).To(Equal(2))
		Expect(count(coordinator.AuditQuery{Outcome: coordinator.OutcomeDenied})).To(Equal(1))
		Expect(count(coordinator.AuditQuery{CoordinatorID: "a1", Outcome: coordinator.OutcomeSent})).To(Equal(1))
		Expect(count(coordinator.AuditQuery{Since: t0.Add(time.Hour)})).To(Equal(3), "Since is inclusive")
		Expect(count(coordinator.AuditQuery{Until: t0.Add(time.Hour)})).To(Equal(1), "Until is exclusive")
		Expect(count(coordinator.AuditQuery{Since: t0.Add(time.Hour), Until: t0.Add(3 * time.Hour)})).To(Equal(2))
		Expect(count(coordinator.AuditQuery{CoordinatorID: "nobody"})).To(BeZero())
	})

	It("AuditCapsTheLimit", func() {
		s := newStore()
		for i := range 510 {
			addAudit(s, entry("a1", "u1", coordinator.KindWake, coordinator.OutcomeInfo, t0.Add(time.Duration(i)*time.Second)))
		}
		got, err := s.Audit(ctx, coordinator.AuditQuery{Limit: 10000})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(500))
	})

	It("PruneDropsOldRowsAndOldCounters", func() {
		s := newStore()
		Expect(s.Put(ctx, Sample("a1", "u1"))).To(Succeed())
		addAudit(s, entry("a1", "u1", coordinator.KindWake, coordinator.OutcomeInfo, t0.Add(-100*24*time.Hour)))
		addAudit(s, entry("a1", "u1", coordinator.KindWake, coordinator.OutcomeInfo, t0))
		old, now := t0.Add(-100*24*time.Hour), t0
		for _, b := range []string{coordinator.DayBucket("send", old), coordinator.DayBucket("send", now), coordinator.MinuteBucket(old), coordinator.MinuteBucket(now)} {
			ok, err := s.Reserve(ctx, "a1", b, 5)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
		}
		n, err := s.Prune(ctx, t0.Add(-90*24*time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1))
		got, err := s.Audit(ctx, coordinator.AuditQuery{})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1))
		Expect(got[0].At).To(Equal(t0))
		Expect(s.Used(ctx, "a1", coordinator.DayBucket("send", old))).To(Equal(0))
		Expect(s.Used(ctx, "a1", coordinator.MinuteBucket(old))).To(Equal(0))
		Expect(s.Used(ctx, "a1", coordinator.DayBucket("send", now))).To(Equal(1))
		Expect(s.Used(ctx, "a1", coordinator.MinuteBucket(now))).To(Equal(1))
	})

	It("AuditSurvivesDelete", func() {
		s := newStore()
		Expect(s.Put(ctx, Sample("a1", "u1"))).To(Succeed())
		addAudit(s, entry("a1", "u1", coordinator.KindContact, coordinator.OutcomeSent, t0))
		Expect(s.Delete(ctx, "a1")).To(Succeed())
		got, err := s.Audit(ctx, coordinator.AuditQuery{CoordinatorID: "a1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1))
	})
}
