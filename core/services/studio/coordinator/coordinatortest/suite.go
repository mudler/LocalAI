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
}
