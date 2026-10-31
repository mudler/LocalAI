// Package loopstest checks a loops.Store against the contract of the package.
package loopstest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/loops"
)

// NewStore returns an empty Store for one spec. It registers its own cleanup.
type NewStore func() loops.Store

var base = time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)

func sample(id, owner, chat string, n int) loops.Loop {
	return loops.Loop{ID: id, Owner: owner, ChatID: chat, Prompt: "Check the build.", Interval: 5 * time.Minute,
		State: loops.StateActive, Next: base.Add(time.Duration(n) * time.Minute), Created: base.Add(time.Duration(n) * time.Second),
		Updated: base}
}

// RunStoreSuite registers the specs of the Store contract.
func RunStoreSuite(newStore NewStore) {
	var (
		s   loops.Store
		ctx context.Context
	)
	BeforeEach(func() {
		s = newStore()
		ctx = context.Background()
	})

	It("adds a loop and reads it back the same", func() {
		l := sample("l1", "u1", "c1", 0)
		l.Reason, l.Runs, l.Failures, l.Wait, l.Scheduled, l.Running = "why", 4, 1, "build", true, true
		l.LastRun = base.Add(-time.Minute)
		Expect(s.Add(ctx, l, loops.Limits{})).To(Succeed())
		got, err := s.Get(ctx, "l1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Prompt).To(Equal(l.Prompt))
		Expect(got.Interval).To(Equal(l.Interval))
		Expect(got.State).To(Equal(loops.StateActive))
		Expect(got.Reason).To(Equal("why"))
		Expect(got.Runs).To(Equal(4))
		Expect(got.Failures).To(Equal(1))
		Expect(got.Wait).To(Equal("build"))
		Expect(got.Scheduled).To(BeTrue())
		Expect(got.Running).To(BeTrue())
		Expect(got.Next.Equal(l.Next)).To(BeTrue(), "next = %v", got.Next)
		Expect(got.LastRun.Equal(l.LastRun)).To(BeTrue(), "last run = %v", got.LastRun)
		Expect(got.Created.Equal(l.Created)).To(BeTrue(), "created = %v", got.Created)
	})

	It("returns ErrNotFound for an unknown loop", func() {
		_, err := s.Get(ctx, "nope")
		Expect(err).To(MatchError(loops.ErrNotFound))
		_, err = s.Update(ctx, "nope", func(*loops.Loop) error { return nil })
		Expect(err).To(MatchError(loops.ErrNotFound))
		Expect(s.Delete(ctx, "nope")).To(Succeed())
	})

	It("refuses an invalid loop and a taken ID", func() {
		bad := sample("l1", "u1", "c1", 0)
		bad.Prompt = " "
		Expect(s.Add(ctx, bad, loops.Limits{})).To(MatchError(loops.ErrInvalid))
		Expect(s.Add(ctx, sample("l1", "u1", "c1", 0), loops.Limits{})).To(Succeed())
		Expect(s.Add(ctx, sample("l1", "u1", "c2", 0), loops.Limits{})).To(MatchError(loops.ErrInvalid))
	})

	It("counts only loops that are not ended against the limits of the chat and the owner", func() {
		lim := loops.Limits{PerChat: 2, PerUser: 3}
		Expect(s.Add(ctx, sample("a", "u1", "c1", 0), lim)).To(Succeed())
		Expect(s.Add(ctx, sample("b", "u1", "c1", 1), lim)).To(Succeed())
		Expect(s.Add(ctx, sample("c", "u1", "c1", 2), lim)).To(MatchError(loops.ErrTooManyInChat))
		Expect(s.Add(ctx, sample("d", "u1", "c2", 3), lim)).To(Succeed())
		Expect(s.Add(ctx, sample("e", "u1", "c3", 4), lim)).To(MatchError(loops.ErrTooManyForOwner))
		// Another owner has limits of their own.
		Expect(s.Add(ctx, sample("f", "u2", "c1", 5), lim)).To(Succeed())
		// An ended loop frees its place.
		_, err := s.Update(ctx, "a", func(l *loops.Loop) error { l.State = loops.StateEnded; return nil })
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Add(ctx, sample("c", "u1", "c1", 6), lim)).To(Succeed())
	})

	It("admits one loop over the limit only once when two add at the same time", func() {
		lim := loops.Limits{PerChat: 1}
		var wg sync.WaitGroup
		var ok atomic.Int32
		for i := range 8 {
			wg.Go(func() {
				if s.Add(ctx, sample(fmt.Sprintf("x%d", i), "u1", "c1", i), lim) == nil {
					ok.Add(1)
				}
			})
		}
		wg.Wait()
		Expect(ok.Load()).To(Equal(int32(1)))
	})

	It("updates in one step and keeps the identity", func() {
		Expect(s.Add(ctx, sample("l1", "u1", "c1", 0), loops.Limits{})).To(Succeed())
		got, err := s.Update(ctx, "l1", func(l *loops.Loop) error {
			l.Runs, l.Running, l.Owner, l.ChatID = 7, true, "evil", "other"
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Runs).To(Equal(7))
		Expect(got.Owner).To(Equal("u1"))
		Expect(got.ChatID).To(Equal("c1"))
		back, _ := s.Get(ctx, "l1")
		Expect(back.Runs).To(Equal(7))
		Expect(back.Running).To(BeTrue())
	})

	It("leaves the loop as it was when the change fails", func() {
		Expect(s.Add(ctx, sample("l1", "u1", "c1", 0), loops.Limits{})).To(Succeed())
		boom := errors.New("boom")
		_, err := s.Update(ctx, "l1", func(l *loops.Loop) error { l.Runs = 9; return boom })
		Expect(err).To(MatchError(boom))
		back, _ := s.Get(ctx, "l1")
		Expect(back.Runs).To(Equal(0))
	})

	It("loses no change when updates race", func() {
		Expect(s.Add(ctx, sample("l1", "u1", "c1", 0), loops.Limits{})).To(Succeed())
		var wg sync.WaitGroup
		for range 10 {
			wg.Go(func() {
				_, err := s.Update(ctx, "l1", func(l *loops.Loop) error { l.Runs++; return nil })
				Expect(err).NotTo(HaveOccurred())
			})
		}
		wg.Wait()
		back, _ := s.Get(ctx, "l1")
		Expect(back.Runs).To(Equal(10))
	})

	It("lists by chat and by owner, oldest first, and the working loops of everyone", func() {
		Expect(s.Add(ctx, sample("b", "u1", "c1", 2), loops.Limits{})).To(Succeed())
		Expect(s.Add(ctx, sample("a", "u1", "c1", 1), loops.Limits{})).To(Succeed())
		p := sample("p", "u1", "c2", 3)
		p.State = loops.StatePaused
		Expect(s.Add(ctx, p, loops.Limits{})).To(Succeed())
		r := sample("r", "u2", "c9", 4)
		r.State, r.Running = loops.StatePaused, true // paused while its run goes on
		Expect(s.Add(ctx, r, loops.Limits{})).To(Succeed())
		e := sample("e", "u2", "c9", 5)
		e.State = loops.StateEnded
		Expect(s.Add(ctx, e, loops.Limits{})).To(Succeed())

		chat, err := s.ListChat(ctx, "u1", "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(ids(chat)).To(Equal([]string{"a", "b"}))
		other, _ := s.ListChat(ctx, "u2", "c1")
		Expect(other).To(BeEmpty())
		own, _ := s.ListOwner(ctx, "u1")
		Expect(ids(own)).To(Equal([]string{"a", "b", "p"}))
		work, _ := s.Working(ctx)
		Expect(ids(work)).To(Equal([]string{"a", "b", "r"}))
	})

	It("deletes one loop, and every loop of a chat with its consent", func() {
		Expect(s.Add(ctx, sample("a", "u1", "c1", 0), loops.Limits{})).To(Succeed())
		Expect(s.Add(ctx, sample("b", "u1", "c1", 1), loops.Limits{})).To(Succeed())
		Expect(s.Add(ctx, sample("c", "u1", "c2", 2), loops.Limits{})).To(Succeed())
		Expect(s.SetConsented(ctx, "u1", "c1")).To(Succeed())
		Expect(s.Delete(ctx, "a")).To(Succeed())
		_, err := s.Get(ctx, "a")
		Expect(err).To(MatchError(loops.ErrNotFound))
		n, err := s.DeleteChat(ctx, "u1", "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1))
		left, _ := s.ListOwner(ctx, "u1")
		Expect(ids(left)).To(Equal([]string{"c"}))
		ok, _ := s.Consented(ctx, "u1", "c1")
		Expect(ok).To(BeFalse())
	})

	It("remembers the consent of a chat per owner", func() {
		ok, err := s.Consented(ctx, "u1", "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
		Expect(s.SetConsented(ctx, "u1", "c1")).To(Succeed())
		Expect(s.SetConsented(ctx, "u1", "c1")).To(Succeed())
		ok, _ = s.Consented(ctx, "u1", "c1")
		Expect(ok).To(BeTrue())
		ok, _ = s.Consented(ctx, "u2", "c1")
		Expect(ok).To(BeFalse())
	})

	It("takes runs from the daily budget of an owner and starts again the next day", func() {
		for range 3 {
			ok, err := s.TakeRun(ctx, "u1", "2026-10-31", 3)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
		}
		ok, _ := s.TakeRun(ctx, "u1", "2026-10-31", 3)
		Expect(ok).To(BeFalse())
		ok, _ = s.TakeRun(ctx, "u2", "2026-10-31", 3)
		Expect(ok).To(BeTrue(), "another owner has a budget of their own")
		ok, _ = s.TakeRun(ctx, "u1", "2026-11-01", 3)
		Expect(ok).To(BeTrue(), "a new day has a new budget")
		ok, _ = s.TakeRun(ctx, "u1", "2026-11-01", 0)
		Expect(ok).To(BeTrue(), "no budget is no limit")
	})

	It("never takes the last run of the day twice", func() {
		var wg sync.WaitGroup
		var ok atomic.Int32
		for range 8 {
			wg.Go(func() {
				if took, err := s.TakeRun(ctx, "u1", "2026-10-31", 3); err == nil && took {
					ok.Add(1)
				}
			})
		}
		wg.Wait()
		Expect(ok.Load()).To(Equal(int32(3)))
	})
}

func ids(ls []loops.Loop) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.ID
	}
	return out
}
