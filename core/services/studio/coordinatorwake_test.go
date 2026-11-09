package studio_test

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/coordinator"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// wakeRig is a frontend whose coordinator host reads the clock of the shared
// stores, so a spec moves the time, and whose turns end at once unless the
// message has BLOCK in it.
type wakeRig struct {
	*coordRig
	sh *shared
}

func newWakeRigOn(sh *shared, st coordinator.Store, never <-chan struct{}, lim func() studio.CoordinatorLimits, runLimit time.Duration) *wakeRig {
	script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
		if strings.Contains(text, "BLOCK") {
			return []studiotest.ToolStep{{Block: never}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
	r := sh.rig(script, studio.TriggerOptions{})
	o := studio.CoordinatorOptions{Store: st, Clock: sh.clock.Now, Limits: lim}
	if runLimit > 0 {
		o.RunLimit = func() time.Duration { return runLimit }
	}
	host, err := studio.NewCoordinators(r.svc, o)
	Expect(err).NotTo(HaveOccurred())
	r.svc.SetCoordinators(host)
	DeferCleanup(host.Close)
	return &wakeRig{coordRig: &coordRig{trigRig: r, host: host, store: st}, sh: sh}
}

func newWakeRig(lim func() studio.CoordinatorLimits, runLimit time.Duration) (*wakeRig, chan struct{}) {
	st, err := coordinator.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	never := make(chan struct{})
	DeferCleanup(func() { close(never) })
	return newWakeRigOn(newShared(), st, never, lim, runLimit), never
}

// desk makes a coordinator that wakes every ten minutes with a note.
func (r *wakeRig) desk(note string) (agentID, chat string) {
	in := studio.CoordinatorInput{Agent: agents.Agent{Name: "Desk", Model: "m"},
		Config: coordinator.Config{WakeEvery: 10 * time.Minute, WakeNote: note}}
	v, err := r.host.Create(context.Background(), alice, in)
	Expect(err).NotTo(HaveOccurred())
	return v.Agent.ID, v.Config.ChatID
}

func (r *wakeRig) tick() {
	Expect(r.host.Tick(context.Background())).To(Succeed())
}

func (r *wakeRig) cfg(id string) coordinator.Config {
	c, err := r.store.Get(context.Background(), id)
	Expect(err).NotTo(HaveOccurred())
	return c
}

func (r *wakeRig) wakeRows(id string) []coordinator.AuditEntry {
	rows, err := r.host.Audit(context.Background(), studio.CoordinatorActor{User: alice}, coordinator.AuditQuery{CoordinatorID: id, Kind: coordinator.KindWake})
	Expect(err).NotTo(HaveOccurred())
	return rows
}

var _ = Describe("coordinator wake", func() {
	ctx := context.Background()
	oneRun := func() studio.CoordinatorLimits { return studio.CoordinatorLimits{RunsPerDay: 1} }

	It("wakes a due coordinator once and moves NextWake by WakeEvery", func() {
		r, _ := newWakeRig(nil, 0)
		id, chat := r.desk("Check the queue.")
		now := r.sh.clock.Now()
		r.tick()
		until("the wake", func() bool { return len(r.h.Sent(chat)) == 1 })
		Expect(r.cfg(id).NextWake).To(BeTemporally("==", now.Add(10*time.Minute)))
		Expect(r.cfg(id).LastWake).To(BeTemporally("==", now))
		idle(r.svc, alice, chat)
		r.tick()
		time.Sleep(100 * time.Millisecond)
		Expect(r.h.Sent(chat)).To(HaveLen(1), "not due again yet")
		rows := r.wakeRows(id)
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].Outcome).To(Equal(coordinator.OutcomeInfo))
	})

	It("two frontends ticking at once wake it once", func() {
		sh := newShared()
		st, err := coordinator.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		never := make(chan struct{})
		DeferCleanup(func() { close(never) })
		a, b := newWakeRigOn(sh, st, never, nil, 0), newWakeRigOn(sh, st, never, nil, 0)
		_, chat := a.desk("go")
		done := make(chan struct{}, 2)
		for _, r := range []*wakeRig{a, b} {
			go func() { defer GinkgoRecover(); r.tick(); done <- struct{}{} }()
		}
		<-done
		<-done
		until("the wake", func() bool { return len(a.h.Sent(chat))+len(b.h.Sent(chat)) >= 1 })
		time.Sleep(150 * time.Millisecond)
		Expect(len(a.h.Sent(chat)) + len(b.h.Sent(chat))).To(Equal(1))
	})

	It("a missed period wakes once, not once per missed period", func() {
		r, _ := newWakeRig(nil, 0)
		_, chat := r.desk("go")
		r.tick()
		idle(r.svc, alice, chat)
		r.sh.clock.Advance(3 * time.Hour) // eighteen periods
		r.tick()
		until("the second wake", func() bool { return len(r.h.Sent(chat)) == 2 })
		idle(r.svc, alice, chat)
		r.tick()
		time.Sleep(100 * time.Millisecond)
		Expect(r.h.Sent(chat)).To(HaveLen(2))
	})

	It("does not wake a paused coordinator or one with WakeEvery 0", func() {
		r, _ := newWakeRig(nil, 0)
		id, chat := r.desk("go")
		_, err := r.host.Pause(ctx, studio.CoordinatorActor{User: alice}, id)
		Expect(err).NotTo(HaveOccurred())
		in := studio.CoordinatorInput{Agent: agents.Agent{Name: "Quiet", Model: "m"}}
		quiet, err := r.host.Create(ctx, alice, in)
		Expect(err).NotTo(HaveOccurred())
		r.tick()
		time.Sleep(150 * time.Millisecond)
		Expect(r.h.Sent(chat)).To(BeEmpty())
		Expect(r.h.Sent(quiet.Config.ChatID)).To(BeEmpty())
		Expect(pendingTexts(r.svc, alice, chat)).To(BeEmpty(), "a paused coordinator is not even held a wake")
		Expect(r.wakeRows(id)).To(BeEmpty())
	})

	It("skips the wake while the previous turn runs", func() {
		r, _ := newWakeRig(nil, 0)
		id, chat := r.desk("BLOCK")
		r.tick()
		until("the turn to run", func() bool { return turn(r.svc, alice, chat).Running })
		r.sh.clock.Advance(time.Hour)
		r.tick()
		time.Sleep(100 * time.Millisecond)
		Expect(r.h.Sent(chat)).To(HaveLen(1))
		Expect(pendingTexts(r.svc, alice, chat)).To(BeEmpty())
		Expect(r.wakeRows(id)).To(HaveLen(1), "the skipped wake writes nothing")
		Expect(r.cfg(id).NextWake).To(BeTemporally(">", r.sh.clock.Now()), "the next wake still moved")
	})

	It("skips the wake while messages wait", func() {
		r, _ := newWakeRig(oneRun, 0)
		id, chat := r.desk("go")
		r.tick() // takes the one run of the day
		idle(r.svc, alice, chat)
		res, err := r.svc.Send(studio.WithWake(ctx, studio.WakeReply), alice, chat, "a reply")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Held).To(BeTrue())
		r.sh.clock.Advance(time.Hour)
		r.tick()
		time.Sleep(100 * time.Millisecond)
		Expect(r.h.Sent(chat)).To(HaveLen(1))
		Expect(pendingTexts(r.svc, alice, chat)).To(Equal([]string{"a reply"}))
		Expect(r.wakeRows(id)).To(HaveLen(1))
	})

	It("a wake over the budget is audited with its reason and NextWake still moves", func() {
		r, _ := newWakeRig(oneRun, 0)
		id, chat := r.desk("go")
		r.tick()
		idle(r.svc, alice, chat)
		r.sh.clock.Advance(15 * time.Minute)
		r.tick()
		time.Sleep(100 * time.Millisecond)
		Expect(r.h.Sent(chat)).To(HaveLen(1))
		rows := r.wakeRows(id)
		Expect(rows).To(HaveLen(2))
		Expect(rows[0].Outcome).To(Equal(coordinator.OutcomeDenied))
		Expect(rows[0].Reason).To(Equal(coordinator.ReasonBudget))
		Expect(r.cfg(id).NextWake).To(BeTemporally("==", r.sh.clock.Now().Add(10*time.Minute)))
	})

	It("a wake that overruns the run limit is stopped and audited timeout", func() {
		r, _ := newWakeRig(nil, 300*time.Millisecond)
		id, chat := r.desk("BLOCK")
		r.tick()
		until("the turn to be stopped", func() bool { return turn(r.svc, alice, chat).Last == store.TurnStopped })
		until("the audit row", func() bool { return len(r.wakeRows(id)) == 2 })
		Expect(r.wakeRows(id)[0].Reason).To(Equal(coordinator.ReasonTimeout))
		Expect(r.wakeRows(id)[0].Outcome).To(Equal(coordinator.OutcomeDenied))
	})

	It("three wakes land in the same chat and the model reads the note and the time", func() {
		r, _ := newWakeRig(nil, 0)
		_, chat := r.desk("Work the topics.")
		for i := range 3 {
			r.tick()
			until("the wake", func() bool { return len(r.h.Sent(chat)) == i+1 })
			idle(r.svc, alice, chat)
			r.sh.clock.Advance(11 * time.Minute)
		}
		sent := r.h.Sent(chat)
		Expect(sent[0]).To(Equal("[Coordinator wake: schedule at 2026-10-27T07:00:00Z]\nWork the topics."))
		Expect(sent[1]).To(HavePrefix("[Coordinator wake: schedule at 2026-10-27T07:11:00Z]"))
		Expect(sent[2]).To(ContainSubstring("2026-10-27T07:22:00Z"))
		all, err := r.svc.List(ctx, alice, 20)
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(HaveLen(1), "one long-lived chat")
	})

	It("says what to do when the creator wrote no note", func() {
		r, _ := newWakeRig(nil, 0)
		_, chat := r.desk("")
		r.tick()
		until("the wake", func() bool { return len(r.h.Sent(chat)) == 1 })
		Expect(r.h.Sent(chat)[0]).To(ContainSubstring("write your state"))
	})

	It("a deleted chat is recreated on the next wake", func() {
		r, _ := newWakeRig(nil, 0)
		id, chat := r.desk("go")
		r.tick()
		idle(r.svc, alice, chat)
		Expect(r.svc.Delete(ctx, alice, chat)).To(Succeed())
		r.sh.clock.Advance(11 * time.Minute)
		r.tick()
		until("the new chat to get the wake", func() bool {
			c := r.cfg(id).ChatID
			return c != chat && len(r.h.Sent(c)) == 1
		})
	})

	It("a deleted coordinator is not woken and nothing is made for it", func() {
		r, _ := newWakeRig(nil, 0)
		id, _ := r.desk("go")
		Expect(r.svc.DeleteAgent(ctx, alice, id)).To(Succeed())
		r.tick()
		Expect(r.host.Wake(ctx, id, studio.WakeSchedule)).To(MatchError(studio.ErrNotACoordinator))
	})

	It("Tick starts a message that waited for the budget once the day turns", func() {
		r, _ := newWakeRig(oneRun, 0)
		_, chat := r.desk("go")
		r.tick()
		idle(r.svc, alice, chat)
		_, err := r.svc.Send(studio.WithWake(ctx, studio.WakeReply), alice, chat, "a reply")
		Expect(err).NotTo(HaveOccurred())
		r.tick()
		time.Sleep(100 * time.Millisecond)
		Expect(pendingTexts(r.svc, alice, chat)).To(Equal([]string{"a reply"}), "still over the budget")
		r.sh.clock.Advance(20 * time.Hour) // past UTC midnight
		r.tick()
		until("the held message to run", func() bool { return len(r.h.Sent(chat)) >= 2 })
		Expect(r.h.Sent(chat)).To(ContainElement("a reply"))
	})

	It("Tick prunes audit rows older than the audit days", func() {
		r, _ := newWakeRig(func() studio.CoordinatorLimits { return studio.CoordinatorLimits{AuditDays: 2} }, 0)
		id, _ := r.desk("go")
		now := r.sh.clock.Now()
		Expect(r.store.AddAudit(ctx, coordinator.AuditEntry{At: now.AddDate(0, 0, -3), CoordinatorID: id, Principal: alice, Kind: coordinator.KindPause, Outcome: coordinator.OutcomeInfo, Reason: coordinator.ReasonOK})).To(Succeed())
		Expect(r.store.AddAudit(ctx, coordinator.AuditEntry{At: now.AddDate(0, 0, -1), CoordinatorID: id, Principal: alice, Kind: coordinator.KindResume, Outcome: coordinator.OutcomeInfo, Reason: coordinator.ReasonOK})).To(Succeed())
		r.tick()
		rows, err := r.host.Audit(ctx, studio.CoordinatorActor{User: alice}, coordinator.AuditQuery{CoordinatorID: id})
		Expect(err).NotTo(HaveOccurred())
		for _, e := range rows {
			Expect(e.Kind).NotTo(Equal(coordinator.KindPause), "the old row is gone")
		}
		Expect(rows).NotTo(BeEmpty())
	})
})
