// Package proposalstest is the contract suite every proposals.Store passes.
// Run registers Ginkgo specs, so call it inside a Describe of the
// implementation's test package.
package proposalstest

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// NewStore returns an empty store. Each spec calls it once.
type NewStore func() proposals.Store

var t0 = time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)

// Sample returns a pending proposal of owner, made at t0 plus age.
func Sample(id, owner, agent string, age time.Duration) contract.ChangeProposal {
	return contract.ChangeProposal{V: contract.ChangeProposalVersion, ID: id, Owner: owner, Agent: agent, Kind: contract.ChangeMemory,
		Scope: contract.ScopeUser, Target: "Memory/index.md", Summary: "a note", Content: "- home: Rome\n", BaseHash: "abc",
		Source: contract.ChangeSource{Kind: "connector", ID: "b1", Chat: "c1", Thread: "t1", Sender: "s1"}, Status: contract.ProposalPending,
		Created: t0.Add(age), Expires: t0.Add(age + contract.ChangeExpiry)}
}

// Run registers the specs that check the store.
func Run(newStore NewStore) {
	ctx := context.Background()
	lim := proposals.Limits{MaxPending: contract.MaxPendingChanges, PerWindow: contract.MaxChangesPerHour, Window: time.Hour}

	It("round-trips a proposal and hides it from other owners", func() {
		s := newStore()
		p := Sample("p1", "u1", "a1", 0)
		Expect(s.Create(ctx, p, lim)).To(Succeed())
		got, err := s.Get(ctx, "u1", "p1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Content).To(Equal(p.Content))
		Expect(got.Source).To(Equal(p.Source))
		Expect(got.Created.Equal(p.Created)).To(BeTrue())
		Expect(got.Answered.IsZero()).To(BeTrue())
		_, err = s.Get(ctx, "u2", "p1")
		Expect(err).To(MatchError(proposals.ErrNotFound))
		Expect(s.Create(ctx, p, lim)).To(MatchError(proposals.ErrExists))
	})

	It("lists newest first and filters by status", func() {
		s := newStore()
		for i := range 3 {
			Expect(s.Create(ctx, Sample(fmt.Sprintf("p%d", i), "u1", "", time.Duration(i)*time.Minute), proposals.Limits{})).To(Succeed())
		}
		Expect(s.Create(ctx, Sample("other", "u2", "", 0), proposals.Limits{})).To(Succeed())
		ok, err := s.Settle(ctx, "u1", "p0", contract.ProposalPending, contract.ProposalDismissed, t0, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		all, _ := s.List(ctx, "u1")
		Expect(ids(all)).To(Equal([]string{"p2", "p1", "p0"}))
		pend, _ := s.List(ctx, "u1", contract.ProposalPending)
		Expect(ids(pend)).To(Equal([]string{"p2", "p1"}))
	})

	It("caps the pending proposals of one owner", func() {
		s := newStore()
		for i := range contract.MaxPendingChanges {
			// Different agents keep the hourly rate out of this spec.
			Expect(s.Create(ctx, Sample(fmt.Sprintf("p%d", i), "u1", fmt.Sprintf("a%d", i), 0), lim)).To(Succeed())
		}
		Expect(s.Create(ctx, Sample("over", "u1", "a99", 0), lim)).To(MatchError(proposals.ErrTooManyPending))
		Expect(s.Create(ctx, Sample("bob", "u2", "a99", 0), lim)).To(Succeed(), "the cap is per owner")
		ok, _ := s.Settle(ctx, "u1", "p0", contract.ProposalPending, contract.ProposalDismissed, t0, 0)
		Expect(ok).To(BeTrue())
		Expect(s.Create(ctx, Sample("again", "u1", "a99", 0), lim)).To(Succeed(), "an answered proposal frees a place")
	})

	It("limits the proposals of one agent in an hour", func() {
		s := newStore()
		for i := range contract.MaxChangesPerHour {
			Expect(s.Create(ctx, Sample(fmt.Sprintf("p%d", i), "u1", "a1", time.Duration(i)*time.Minute), lim)).To(Succeed())
			_, _ = s.Settle(ctx, "u1", fmt.Sprintf("p%d", i), contract.ProposalPending, contract.ProposalDismissed, t0, 0)
		}
		Expect(s.Create(ctx, Sample("over", "u1", "a1", 10*time.Minute), lim)).To(MatchError(proposals.ErrRateLimited))
		Expect(s.Create(ctx, Sample("other-agent", "u1", "a2", 10*time.Minute), lim)).To(Succeed())
		Expect(s.Create(ctx, Sample("later", "u1", "a1", 2*time.Hour), lim)).To(Succeed(), "the window moves on")
	})

	It("settles a proposal once", func() {
		s := newStore()
		Expect(s.Create(ctx, Sample("p1", "u1", "", 0), lim)).To(Succeed())
		var won atomic.Int32
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ok, err := s.Settle(ctx, "u1", "p1", contract.ProposalPending, contract.ProposalApplied, t0.Add(time.Minute), 0)
				if err == nil && ok {
					won.Add(1)
				}
			}()
		}
		wg.Wait()
		Expect(won.Load()).To(BeEquivalentTo(1))
		Expect(s.SetRev(ctx, "u1", "p1", 42)).To(Succeed())
		ok, err := s.Settle(ctx, "u1", "p1", contract.ProposalApplied, contract.ProposalReverted, t0.Add(time.Hour), 43)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		got, _ := s.Get(ctx, "u1", "p1")
		Expect(got.Status).To(Equal(contract.ProposalReverted))
		Expect(got.Rev).To(BeEquivalentTo(42))
		Expect(got.RevertRev).To(BeEquivalentTo(43))
		Expect(got.Answered.Equal(t0.Add(time.Hour))).To(BeTrue())
		_, err = s.Settle(ctx, "u2", "p1", contract.ProposalReverted, contract.ProposalApplied, t0, 0)
		Expect(err).To(MatchError(proposals.ErrNotFound))
		Expect(s.SetRev(ctx, "u2", "p1", 1)).To(MatchError(proposals.ErrNotFound))
	})

	It("expires pending proposals at their time and reports each once", func() {
		s := newStore()
		Expect(s.Create(ctx, Sample("old", "u1", "a1", 0), lim)).To(Succeed())
		Expect(s.Create(ctx, Sample("new", "u1", "a2", 23*time.Hour), lim)).To(Succeed())
		none, err := s.Expire(ctx, t0.Add(23*time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(none).To(BeEmpty())
		got, err := s.Expire(ctx, t0.Add(24*time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(ids(got)).To(Equal([]string{"old"}))
		again, _ := s.Expire(ctx, t0.Add(24*time.Hour))
		Expect(again).To(BeEmpty())
		p, _ := s.Get(ctx, "u1", "old")
		Expect(p.Status).To(Equal(contract.ProposalExpired))
		n, _ := s.Get(ctx, "u1", "new")
		Expect(n.Status).To(Equal(contract.ProposalPending))
	})

	It("keeps an audit per owner, newest first", func() {
		s := newStore()
		for i, a := range []string{proposals.ActionPropose, proposals.ActionConfirm} {
			Expect(s.AddEvent(ctx, proposals.Event{At: t0.Add(time.Duration(i) * time.Minute), Owner: "u1", Action: a, Proposal: "p1",
				Kind: contract.ChangeMemory, Source: contract.ChangeSource{Kind: "connector", ID: "b1"}, Hash: "h"})).To(Succeed())
		}
		Expect(s.AddEvent(ctx, proposals.Event{At: t0, Owner: "u2", Action: proposals.ActionPropose})).To(Succeed())
		evs, err := s.Events(ctx, "u1", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(evs).To(HaveLen(2))
		Expect(evs[0].Action).To(Equal(proposals.ActionConfirm))
		Expect(evs[0].Source.ID).To(Equal("b1"))
		one, _ := s.Events(ctx, "u1", 1)
		Expect(one).To(HaveLen(1))
	})
}

func ids(l []contract.ChangeProposal) []string {
	out := make([]string, len(l))
	for i, p := range l {
		out[i] = p.ID
	}
	return out
}
