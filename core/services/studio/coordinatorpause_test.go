package studio_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/coordinator"
	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/testutil"
)

var _ = Describe("coordinator pause", func() {
	bg := context.Background()
	owner := studio.CoordinatorActor{User: alice}
	admin := studio.CoordinatorActor{User: "root", Admin: true}

	It("a user who is neither owner nor admin gets not found; an admin pauses another user's coordinator", func() {
		r := newGateRig(0, 0)
		id, chat := r.desk("Desk")
		_, err := r.host.Pause(bg, studio.CoordinatorActor{User: "mallory"}, id)
		Expect(err).To(MatchError(coordinator.ErrNotFound))
		_, err = r.host.Resume(bg, studio.CoordinatorActor{User: "mallory"}, id)
		Expect(err).To(MatchError(coordinator.ErrNotFound))
		_, err = r.host.Pause(bg, owner, "no-such-coordinator")
		Expect(err).To(MatchError(coordinator.ErrNotFound))
		cfg, err := r.store.Get(bg, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Paused).To(BeFalse())

		v, err := r.host.Pause(bg, admin, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.Config.Paused).To(BeTrue())
		Expect(v.Config.PausedBy).To(Equal("root"))
		Expect(r.send(bg, chat, "x").Held).To(BeTrue())
		v, err = r.host.Resume(bg, admin, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.Config.Paused).To(BeFalse())
	})

	It("pause stops the running turn and its background helpers", func() {
		r := newGateRig(0, 0)
		id, chat := r.desk("Desk")
		Expect(r.send(bg, chat, "HELPER").Started).To(BeTrue())
		var kid session.ChatRecord
		until("the helper to run", func() bool {
			kids := childrenOf(r.svc, alice, chat)
			if len(kids) != 1 || !turn(r.svc, alice, kids[0].ID).Running {
				return false
			}
			kid = kids[0]
			return turn(r.svc, alice, chat).Running
		})
		_, err := r.host.Pause(bg, owner, id)
		Expect(err).NotTo(HaveOccurred())
		until("the turn and the helper to stop", func() bool {
			return turn(r.svc, alice, chat).Last == store.TurnStopped && turn(r.svc, alice, kid.ID).Last == store.TurnStopped
		})
	})

	It("pause pauses the goal and the active loops of the chat", func() {
		r := newGateRig(0, 0)
		id, chat := r.desk("Desk")
		lp, err := loops.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		lh, err := studio.NewLoops(r.svc, studio.LoopOptions{Store: lp, Tick: time.Hour})
		Expect(err).NotTo(HaveOccurred())
		r.svc.SetLoops(lh)
		DeferCleanup(lh.Close)
		// The model starts loops in a coordinator chat; the store has one.
		Expect(lp.Add(bg, loops.Loop{ID: "l1", Owner: alice, ChatID: chat, Prompt: "check", Interval: time.Hour,
			State: loops.StateActive, Next: time.Now().Add(time.Hour), Created: time.Now(), Updated: time.Now()}, loops.Limits{})).To(Succeed())
		Expect(r.sh.chats.UpdateGoal(bg, alice, chat, func(*session.Goal) (*session.Goal, error) {
			return &session.Goal{Text: "report", State: session.GoalActive}, nil
		})).To(Succeed())

		_, err = r.host.Pause(bg, owner, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(goalOf(r.svc, chat).State).To(Equal(session.GoalPaused))
		got, err := lh.List(bg, alice, chat)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1))
		Expect(got[0].State).To(Equal(loops.StatePaused))
	})

	It("pausing twice writes one audit row", func() {
		r := newGateRig(0, 0)
		id, _ := r.desk("Desk")
		for range 2 {
			_, err := r.host.Pause(bg, admin, id)
			Expect(err).NotTo(HaveOccurred())
		}
		rows, err := r.host.Audit(bg, owner, coordinator.AuditQuery{CoordinatorID: id})
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].Kind).To(Equal(coordinator.KindPause))
		Expect(rows[0].Outcome).To(Equal(coordinator.OutcomeInfo))
		Expect(rows[0].Actor).To(Equal("root"))
		Expect(rows[0].Principal).To(Equal(alice))
		_, err = r.host.Resume(bg, owner, id)
		Expect(err).NotTo(HaveOccurred())
		_, err = r.host.Resume(bg, owner, id)
		Expect(err).NotTo(HaveOccurred())
		rows, err = r.host.Audit(bg, owner, coordinator.AuditQuery{CoordinatorID: id})
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(2), "one resume row, newest first")
		Expect(rows[0].Kind).To(Equal(coordinator.KindResume))
	})

	It("resume starts the held messages once, in order", func() {
		r := newGateRig(0, 0)
		id, chat := r.desk("Desk")
		_, err := r.host.Pause(bg, owner, id)
		Expect(err).NotTo(HaveOccurred())
		for _, t := range []string{"first", "second", "third"} {
			Expect(r.send(bg, chat, t).Held).To(BeTrue())
		}
		Expect(r.h.Sent(chat)).To(BeEmpty())
		_, err = r.host.Resume(bg, owner, id)
		Expect(err).NotTo(HaveOccurred())
		until("the held messages to run", func() bool { return len(r.h.Sent(chat)) == 1 })
		idle(r.svc, alice, chat)
		sent := r.h.Sent(chat)
		Expect(sent).To(HaveLen(1), "one turn, no duplicate")
		Expect(sent[0]).To(MatchRegexp(`(?s)first.*second.*third`))
		Expect(pendingTexts(r.svc, alice, chat)).To(BeEmpty())
		// A second resume, or a release with nothing waiting, starts nothing.
		cfg, err := r.store.Get(bg, id)
		Expect(err).NotTo(HaveOccurred())
		started, err := r.host.Release(bg, cfg)
		Expect(err).NotTo(HaveOccurred())
		Expect(started).To(BeFalse())
		Expect(r.h.Sent(chat)).To(HaveLen(1))
	})

	It("release leaves the messages held while the coordinator is paused", func() {
		r := newGateRig(0, 0)
		id, chat := r.desk("Desk")
		_, err := r.host.Pause(bg, owner, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.send(bg, chat, "held").Held).To(BeTrue())
		cfg, _ := r.store.Get(bg, id)
		started, err := r.host.Release(bg, cfg)
		Expect(err).NotTo(HaveOccurred())
		Expect(started).To(BeFalse())
		Expect(pendingTexts(r.svc, alice, chat)).To(Equal([]string{"held"}))
	})

	It("a pause on frontend A stops a turn running on frontend B and B holds new messages", func() {
		sh := newShared()
		net := testutil.NewFakeBus()
		sh.net = net
		sh.deps = func(d *studio.Deps) { d.Bus = studio.NewBroadcastBus(net, nil) }
		st, err := coordinator.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		block := make(chan struct{})
		a := newGateRigOn(sh, st, block, nil)
		b := newGateRigOn(sh, st, block, nil)
		id, chat := a.desk("Desk")

		Expect(b.send(bg, chat, "BLOCK").Started).To(BeTrue())
		until("the turn to run on B", func() bool { return len(b.h.Sent(chat)) == 1 })
		Expect(a.h.Sent(chat)).To(BeEmpty())

		_, err = a.host.Pause(bg, admin, id)
		Expect(err).NotTo(HaveOccurred())
		until("the turn on B to stop", func() bool { return turn(a.svc, alice, chat).Last == store.TurnStopped })
		Expect(a.send(bg, chat, "from A").Held).To(BeTrue())
		Expect(b.send(bg, chat, "from B").Held).To(BeTrue())
		Expect(pendingTexts(a.svc, alice, chat)).To(Equal([]string{"from A", "from B"}))

		// Resume on B releases what both held, and a turn runs on B once.
		_, err = b.host.Resume(bg, owner, id)
		Expect(err).NotTo(HaveOccurred())
		until("the held messages to run", func() bool { return len(a.h.Sent(chat))+len(b.h.Sent(chat)) >= 2 })
		idle(a.svc, alice, chat)
		Expect(len(a.h.Sent(chat))+len(b.h.Sent(chat))).To(Equal(2), "the BLOCK turn and one turn for the held messages")
	})

	It("Audit of an owner never returns another user's rows", func() {
		r := newGateRig(0, 0)
		mine, _ := r.desk("Mine")
		Expect(r.store.AddAudit(bg, coordinator.AuditEntry{CoordinatorID: "other", Principal: "bob", Kind: coordinator.KindContact, Outcome: coordinator.OutcomeSent})).To(Succeed())
		_, err := r.host.Pause(bg, owner, mine)
		Expect(err).NotTo(HaveOccurred())

		rows, err := r.host.Audit(bg, owner, coordinator.AuditQuery{})
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].Principal).To(Equal(alice))
		rows, err = r.host.Audit(bg, owner, coordinator.AuditQuery{Principal: "bob"})
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1), "the principal of the query is overridden, not honoured")
		Expect(rows[0].Principal).To(Equal(alice))
		rows, err = r.host.Audit(bg, admin, coordinator.AuditQuery{})
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(2))
	})
})
