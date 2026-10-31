package studio_test

import (
	"context"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/toolargs"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/egress"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func tlsTo(host string) egress.Request {
	return egress.Request{Host: host, Port: 443, Kind: egress.KindConnect, TLS: true}
}

// waitCard waits until the chat has a pending card and returns it.
func waitCard(st store.Store, user, chat string) egress.Card {
	var card egress.Card
	Eventually(func() bool {
		rec, err := st.GetChat(context.Background(), user, chat)
		if err != nil {
			return false
		}
		p := rec.Egress.Pending()
		if len(p) == 0 {
			return false
		}
		card = p[0]
		return true
	}, 5*time.Second, 10*time.Millisecond).Should(BeTrue(), "a card")
	return card
}

// waitCardAfter starts a check that raises a card and returns the card. The
// check goes on in the background and ends with the test's wait.
func waitCardAfter(svc *studio.Service, st store.Store, user, rid, chat string) egress.Card {
	_ = checkAsync(svc, user, rid, "op1", tlsTo("example.com"))
	return waitCard(st, user, chat)
}

type checkResult struct {
	v   egress.Verdict
	err error
}

func checkAsync(svc *studio.Service, user, rid, op string, r egress.Request) <-chan checkResult {
	out := make(chan checkResult, 1)
	go func() {
		defer GinkgoRecover()
		v, err := svc.EgressCheck(context.Background(), user, rid, op, r)
		out <- checkResult{v, err}
	}()
	return out
}

var _ = Describe("the network cards of a chat", func() {
	ctx := context.Background()
	var (
		svc      *studio.Service
		st       store.Store
		settings egress.Settings
		id, rid  string
	)
	newSvc := func(bus studio.Bus) *studio.Service {
		return newServiceOn(st, studiotest.NewGateHarness(), func(d *studio.Deps) {
			d.EgressSettings = func() egress.Settings { return settings }
			d.IsAdmin = func(_ context.Context, user string) bool { return user == alice }
			d.EgressCardWait = 3 * time.Second
			if bus != nil {
				d.Bus = bus
			}
		})
	}
	BeforeEach(func() {
		st, _ = store.NewJSON("")
		settings = egress.Settings{Ceiling: egress.PresetAll}
		svc = newSvc(nil)
		id = create(svc, alice)
		rid = toolargs.ActionResourceID(id)
	})

	It("asks for a host the preset does not allow and lets the command go on after 'Allow once', for the rest of that call", func() {
		got := checkAsync(svc, alice, rid, "op1", tlsTo("example.com"))
		card := waitCard(st, alice, id)
		Expect(card).To(SatisfyAll(HaveField("Host", "example.com"), HaveField("Port", 443), HaveField("State", egress.CardPending), HaveField("Op", "op1")))
		Expect(svc.EgressDecide(ctx, alice, id, card.ID, studio.EgressOnce)).To(Succeed())
		var r checkResult
		Eventually(got, 2*time.Second).Should(Receive(&r), "the answer wakes the waiting check at once")
		Expect(r.v).To(Equal(egress.Verdict{Action: egress.Allow, Reason: egress.ReasonOnce}))
		v, _ := svc.EgressCheck(ctx, alice, rid, "op1", tlsTo("example.com"))
		Expect(v.Action).To(Equal(egress.Allow), "the rest of the command")
		got2 := checkAsync(svc, alice, rid, "op2", tlsTo("example.com"))
		_ = waitCard(st, alice, id)
		Expect(got2).NotTo(Receive(), "another call asks again")
	})

	It("stores a rule with 'Allow for this chat' and remembers 'Deny', so neither asks again", func() {
		got := checkAsync(svc, alice, rid, "op1", tlsTo("example.com"))
		card := waitCard(st, alice, id)
		Expect(svc.EgressDecide(ctx, alice, id, card.ID, studio.EgressChat)).To(Succeed())
		Expect((<-got).v).To(Equal(egress.Verdict{Action: egress.Allow, Reason: egress.ReasonRule}))
		v, _ := svc.EgressCheck(ctx, alice, rid, "op9", tlsTo("example.com"))
		Expect(v.Reason).To(Equal(egress.ReasonRule))

		got = checkAsync(svc, alice, rid, "op1", tlsTo("evil.example"))
		card = waitCard(st, alice, id)
		Expect(svc.EgressDecide(ctx, alice, id, card.ID, studio.EgressDeny)).To(Succeed())
		Expect((<-got).v).To(Equal(egress.Verdict{Action: egress.Block, Reason: egress.ReasonDenied}))
		v, _ = svc.EgressCheck(ctx, alice, rid, "op9", tlsTo("evil.example"))
		Expect(v.Reason).To(Equal(egress.ReasonDenied))
		Expect(svc.EgressDecide(ctx, alice, id, card.ID, studio.EgressOnce)).To(MatchError(studio.ErrEgressAnswered))
	})

	It("shows one card per host and port, and at most three at a time", func() {
		a := checkAsync(svc, alice, rid, "op1", tlsTo("a.example"))
		b := checkAsync(svc, alice, rid, "op1", tlsTo("a.example"))
		card := waitCard(st, alice, id)
		Consistently(func() int {
			rec, _ := st.GetChat(ctx, alice, id)
			return len(rec.Egress.Pending())
		}, 300*time.Millisecond).Should(Equal(1))
		_ = checkAsync(svc, alice, rid, "op1", tlsTo("b.example"))
		_ = checkAsync(svc, alice, rid, "op1", tlsTo("c.example"))
		Eventually(func() int {
			rec, _ := st.GetChat(ctx, alice, id)
			return len(rec.Egress.Pending())
		}, 2*time.Second).Should(Equal(3))
		v, _ := svc.EgressCheck(ctx, alice, rid, "op1", tlsTo("d.example"))
		Expect(v.Reason).To(Equal(egress.ReasonCardsFull))
		Expect(svc.EgressDecide(ctx, alice, id, card.ID, studio.EgressChat)).To(Succeed())
		Expect((<-a).v.Action).To(Equal(egress.Allow))
		Expect((<-b).v.Action).To(Equal(egress.Allow), "the second connection waited on the same card")
	})

	It("offers no card when the ceiling is off, nor to an unattended run or a sub-agent", func() {
		settings.Ceiling = egress.PresetOff
		v, _ := svc.EgressCheck(ctx, alice, rid, "op1", tlsTo("example.com"))
		Expect(v.Reason).To(Equal(egress.ReasonCeiling))
		settings.Ceiling = egress.PresetAll

		now := time.Now().UTC()
		Expect(st.CreateChat(ctx, session.ChatRecord{ID: "run1", Principal: alice, Title: "Run", Created: now, Updated: now, Origin: session.OriginTrigger, AgentID: "ag"})).To(Succeed())
		v, _ = svc.EgressCheck(ctx, alice, toolargs.ActionResourceID("run1"), "op1", tlsTo("example.com"))
		Expect(v.Reason).To(Equal(egress.ReasonUnattended))

		Expect(st.UpdateEgress(ctx, alice, id, func(s *egress.ChatState) error { s.Preset = egress.PresetRegistries; return nil })).To(Succeed())
		Expect(st.CreateChat(ctx, session.ChatRecord{ID: "sub1", Principal: alice, Title: "Sub", Created: now, Updated: now, Kind: session.KindSubAgent, ParentID: id})).To(Succeed())
		sub := toolargs.ActionResourceID("sub1")
		v, _ = svc.EgressCheck(ctx, alice, sub, "op1", tlsTo("pypi.org"))
		Expect(v.Action).To(Equal(egress.Allow), "a sub-agent follows its parent's preset")
		v, _ = svc.EgressCheck(ctx, alice, sub, "op1", tlsTo("example.com"))
		Expect(v.Reason).To(Equal(egress.ReasonUnattended), "and gets no card")
	})

	It("lets only an administrator allow a private target; anyone may deny it", func() {
		settings.AllowPrivate = true
		bobChat := create(svc, bob)
		got := checkAsync(svc, bob, toolargs.ActionResourceID(bobChat), "op1", tlsTo("10.0.0.5"))
		card := waitCard(st, bob, bobChat)
		Expect(card.Private).To(BeTrue())
		Expect(svc.EgressDecide(ctx, bob, bobChat, card.ID, studio.EgressOnce)).To(MatchError(studio.ErrEgressDecision))
		Expect(svc.EgressDecide(ctx, bob, bobChat, card.ID, studio.EgressDeny)).To(Succeed())
		Expect((<-got).v.Reason).To(Equal(egress.ReasonDenied))

		got = checkAsync(svc, alice, rid, "op1", tlsTo("10.0.0.6"))
		card = waitCard(st, alice, id)
		Expect(svc.EgressDecide(ctx, alice, id, card.ID, studio.EgressChat)).To(Succeed())
		Expect((<-got).v).To(Equal(egress.Verdict{Action: egress.Allow, Reason: egress.ReasonRule, AllowPrivate: true}))
	})

	It("blocks with no answer after the wait, takes a late 'once' for the next call, and expires cards at the end of the turn", func() {
		svc = newServiceOn(st, studiotest.NewGateHarness(), func(d *studio.Deps) {
			d.EgressSettings = func() egress.Settings { return settings }
			d.EgressCardWait = 200 * time.Millisecond
		})
		v, _ := svc.EgressCheck(ctx, alice, rid, "op1", tlsTo("late.example"))
		Expect(v.Reason).To(Equal(egress.ReasonNoAnswer))
		svc.SettleEgressCardsForTest(ctx, alice, id, time.Now().Add(-time.Hour))
		rec, _ := st.GetChat(ctx, alice, id)
		Expect(rec.Egress.Cards).To(ConsistOf(HaveField("State", egress.CardExpired)))
		Expect(svc.EgressDecide(ctx, alice, id, rec.Egress.Cards[0].ID, studio.EgressOnce)).To(Succeed(), "an expired card can still be answered")
		v, _ = svc.EgressCheck(ctx, alice, rid, "op2", tlsTo("late.example"))
		Expect(v.Reason).To(Equal(egress.ReasonOnce), "the next call takes the late grant")
		v, _ = svc.EgressCheck(ctx, alice, rid, "op3", tlsTo("late.example"))
		Expect(v.Action).NotTo(Equal(egress.Allow), "and keeps it for itself")
	})

	Describe("cards left by a frontend that died mid-turn", func() {
		// leave stores three pending cards of the turn "dead", as a frontend
		// that crashed while its connections waited leaves them.
		leave := func() {
			Expect(st.ClaimTurn(ctx, alice, id, "dead")).To(Succeed())
			Expect(st.UpdateEgress(ctx, alice, id, func(s *egress.ChatState) error {
				for _, h := range []string{"a.example", "b.example", "c.example"} {
					s.PutCard(egress.Card{ID: "dead-" + h, Host: h, Port: 443, Kind: egress.KindConnect, State: egress.CardPending, Op: "old", Turn: "dead", AskedAt: time.Now()})
				}
				return nil
			})).To(Succeed())
		}

		It("do not count toward the cap of the next turn, read as expired, and expire for good when the next card is asked", func() {
			leave()
			// The claim of the dead turn ended without the turn-end path (an
			// older reaper, a store restored by hand); another turn holds the
			// chat now.
			Expect(st.ReleaseTurn(ctx, alice, id, "dead", store.TurnInterrupted)).To(Succeed())
			Expect(st.ClaimTurn(ctx, alice, id, "next")).To(Succeed())

			v, err := svc.Egress(ctx, alice, id)
			Expect(err).NotTo(HaveOccurred())
			Expect(v.Cards).To(HaveEach(HaveField("State", egress.CardExpired)), "the view shows them as not answered")

			got := checkAsync(svc, alice, rid, "op1", tlsTo("d.example"))
			var rec session.ChatRecord
			Eventually(func() []egress.Card {
				rec, _ = st.GetChat(ctx, alice, id)
				return rec.Egress.Pending()
			}, 5*time.Second, 10*time.Millisecond).Should(ConsistOf(SatisfyAll(HaveField("Host", "d.example"), HaveField("Turn", "next"))),
				"a new card, not cards-full, and the dead cards expired in the record")
			card := rec.Egress.Pending()[0]
			Expect(svc.EgressDecide(ctx, alice, id, card.ID, studio.EgressChat)).To(Succeed())
			Expect((<-got).v.Action).To(Equal(egress.Allow))
			Expect(svc.EgressDecide(ctx, alice, id, "dead-a.example", studio.EgressOnce)).To(Succeed(), "a dead card can still be answered late")
		})

		It("expire when the reaper ends the dead turn", func() {
			leave()
			n, err := svc.ReapStale(ctx, 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(1))
			rec, _ := st.GetChat(ctx, alice, id)
			Expect(rec.Egress.Cards).To(HaveLen(3))
			Expect(rec.Egress.Pending()).To(BeEmpty())
		})

		It("expire when the next turn starts", func() {
			leave()
			Expect(st.ReleaseTurn(ctx, alice, id, "dead", store.TurnInterrupted)).To(Succeed())
			sent(svc, alice, id, "hi")
			Eventually(func() int {
				rec, _ := st.GetChat(ctx, alice, id)
				return len(rec.Egress.Pending())
			}, 5*time.Second).Should(BeZero())
		})
	})

	It("refuses a waiting card when the ceiling goes down", func() {
		got := checkAsync(svc, alice, rid, "op1", tlsTo("example.com"))
		card := waitCard(st, alice, id)
		settings.Ceiling = egress.PresetRegistries
		var r checkResult
		Eventually(got, 4*time.Second).Should(Receive(&r))
		Expect(r.v.Reason).To(Equal(egress.ReasonCeiling))
		rec, _ := st.GetChat(ctx, alice, id)
		c, _ := rec.Egress.Card(card.ID)
		Expect(c.State).To(Equal(egress.CardRefused))
	})

	Describe("an answer to a card the limit no longer admits", func() {
		// The root of the 409 loop: the limit goes down while the card waits,
		// the click finds the card refused, and every later click used to be a
		// bare conflict. Now each answer gets the same precise reason.
		It("gives egress_above_ceiling on every answer, not a conflict, and the view says no answer can work", func() {
			card := waitCardAfter(svc, st, alice, rid, id)
			settings.Ceiling = egress.PresetOff
			v, err := svc.Egress(ctx, alice, id)
			Expect(err).NotTo(HaveOccurred())
			Expect(v.CardAbove).To(HaveKeyWithValue(card.ID, true))
			Expect(svc.EgressDecide(ctx, alice, id, card.ID, studio.EgressOnce)).To(MatchError(studio.ErrEgressHostAboveCeiling))
			Expect(svc.EgressDecide(ctx, alice, id, card.ID, studio.EgressOnce)).To(MatchError(studio.ErrEgressHostAboveCeiling))
			Expect(svc.EgressDecide(ctx, alice, id, card.ID, studio.EgressChat)).To(MatchError(studio.ErrEgressHostAboveCeiling))
			rec, _ := st.GetChat(ctx, alice, id)
			rules := rec.Egress.Rules
			Expect(rules).To(BeEmpty(), "no rule was stored")
			Expect(rec.Egress.Grants("", time.Now())).To(BeEmpty(), "no grant either")
		})

		It("treats the same answer twice as one: the second click changes nothing and succeeds", func() {
			got := checkAsync(svc, alice, rid, "op1", tlsTo("example.com"))
			card := waitCard(st, alice, id)
			Expect(svc.EgressDecide(ctx, alice, id, card.ID, studio.EgressChat)).To(Succeed())
			Expect(svc.EgressDecide(ctx, alice, id, card.ID, studio.EgressChat)).To(Succeed())
			Expect((<-got).v.Reason).To(Equal(egress.ReasonRule))
			rec, _ := st.GetChat(ctx, alice, id)
			Expect(rec.Egress.Rules).To(HaveLen(1))
			Expect(svc.EgressDecide(ctx, alice, id, card.ID, studio.EgressDeny)).To(MatchError(studio.ErrEgressAnswered), "another answer is still a conflict")
		})
	})

	Describe("two frontends", func() {
		It("an answer through frontend B wakes the check waiting on frontend A at once, and a lost message within the re-read", func() {
			net := testutil.NewFakeBus()
			a := newSvc(studio.NewBroadcastBus(net, nil))
			b := newSvc(studio.NewBroadcastBus(net, nil))
			got := checkAsync(a, alice, rid, "op1", tlsTo("example.com"))
			card := waitCard(st, alice, id)
			start := time.Now()
			Expect(b.EgressDecide(ctx, alice, id, card.ID, studio.EgressChat)).To(Succeed())
			Expect((<-got).v.Action).To(Equal(egress.Allow))
			Expect(time.Since(start)).To(BeNumerically("<", time.Second), "the bus message woke it")

			// Two buses that do not talk: only the re-read of the card wakes A.
			c := newSvc(studio.NewLocalBus())
			d := newSvc(studio.NewLocalBus())
			got = checkAsync(c, alice, rid, "op2", tlsTo("other.example"))
			card = waitCard(st, alice, id)
			start = time.Now()
			Expect(d.EgressDecide(ctx, alice, id, card.ID, studio.EgressChat)).To(Succeed())
			var r checkResult
			Eventually(got, 3*time.Second).Should(Receive(&r))
			Expect(r.v.Action).To(Equal(egress.Allow))
			Expect(time.Since(start)).To(BeNumerically("<=", 2500*time.Millisecond))
		})

		It("a settings change through one frontend is seen by the other at the next check", func() {
			var mu sync.Mutex
			shared := egress.Settings{Ceiling: egress.PresetOff}
			read := func() egress.Settings { mu.Lock(); defer mu.Unlock(); return shared }
			a := newServiceOn(st, studiotest.NewGateHarness(), func(d *studio.Deps) { d.EgressSettings = read })
			b := newServiceOn(st, studiotest.NewGateHarness(), func(d *studio.Deps) { d.EgressSettings = read })
			Expect(a.SetEgressPreset(ctx, alice, id, egress.PresetRegistries)).To(MatchError(studio.ErrEgressAboveCeiling))
			mu.Lock()
			shared.Ceiling = egress.PresetRegistries
			mu.Unlock()
			Expect(a.SetEgressPreset(ctx, alice, id, egress.PresetRegistries)).To(Succeed())
			v, _ := b.EgressCheck(ctx, alice, rid, "op1", tlsTo("pypi.org"))
			Expect(v.Action).To(Equal(egress.Allow), "the preset set through A, in the shared record")
		})
	})

	Describe("saved agents and unattended runs", func() {
		It("a trigger run follows its agent's network field, capped by the ceiling, and never asks; a run without an agent is off", func() {
			ags, _ := agents.NewJSON("")
			svc = newServiceOn(st, studiotest.NewGateHarness(), func(d *studio.Deps) {
				d.EgressSettings = func() egress.Settings { return settings }
				d.Agents = ags
			})
			now := time.Now().UTC()
			Expect(ags.Put(ctx, agents.Agent{ID: "ag1", Principal: alice, Name: "Builder", Network: "registries", Created: now, Updated: now})).To(Succeed())
			Expect(ags.Put(ctx, agents.Agent{ID: "ag2", Principal: alice, Name: "Quiet", Created: now, Updated: now})).To(Succeed())
			for _, c := range []session.ChatRecord{
				{ID: "run1", AgentID: "ag1"}, {ID: "run2", AgentID: "ag2"}, {ID: "run3"},
			} {
				c.Principal, c.Title, c.Created, c.Updated, c.Origin = alice, "Run", now, now, session.OriginTrigger
				Expect(st.CreateChat(ctx, c)).To(Succeed())
			}
			v, _ := svc.EgressCheck(ctx, alice, toolargs.ActionResourceID("run1"), "op1", tlsTo("pypi.org"))
			Expect(v.Action).To(Equal(egress.Allow), "the agent's registries")
			v, _ = svc.EgressCheck(ctx, alice, toolargs.ActionResourceID("run1"), "op1", tlsTo("example.com"))
			Expect(v.Reason).To(Equal(egress.ReasonUnattended), "blocked, no card")
			v, _ = svc.EgressCheck(ctx, alice, toolargs.ActionResourceID("run2"), "op1", tlsTo("pypi.org"))
			Expect(v.Action).NotTo(Equal(egress.Allow), "an agent with no network field is off")
			v, _ = svc.EgressCheck(ctx, alice, toolargs.ActionResourceID("run3"), "op1", tlsTo("pypi.org"))
			Expect(v.Action).NotTo(Equal(egress.Allow), "a run with no agent is off")
			settings.Ceiling = egress.PresetOff
			v, _ = svc.EgressCheck(ctx, alice, toolargs.ActionResourceID("run1"), "op1", tlsTo("pypi.org"))
			Expect(v.Reason).To(Equal(egress.ReasonCeiling), "capped by the ceiling")
		})

		It("a chat a person starts with an agent takes the agent's network, under the ceiling", func() {
			ags, _ := agents.NewJSON("")
			settings.Ceiling = egress.PresetRegistries
			svc = newServiceOn(st, studiotest.NewGateHarness(), func(d *studio.Deps) {
				d.EgressSettings = func() egress.Settings { return settings }
				d.Agents = ags
			})
			now := time.Now().UTC()
			Expect(ags.Put(ctx, agents.Agent{ID: "ag1", Principal: alice, Name: "Builder", Model: "m", Network: "all", Created: now, Updated: now})).To(Succeed())
			rec, err := svc.Create(ctx, alice, studio.CreateInput{Model: "m", AgentID: "ag1"})
			Expect(err).NotTo(HaveOccurred())
			Expect(rec.Egress.Preset).To(Equal(egress.PresetRegistries))
		})
	})
})
