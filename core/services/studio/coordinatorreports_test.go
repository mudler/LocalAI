package studio_test

import (
	"context"
	"errors"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/coordinator"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// repNode is one frontend of a cluster whose coordinator tells helpers by
// script: GO starts a helper that answers at once, GOHOLD one that never ends,
// GOFAIL one that fails, DEEP a helper (MID) that starts a helper (LEAF) which
// waits for leaf to close and then ends, and RUNHOLD starts a helper that
// never ends and keeps its own turn running.
type repNode struct {
	*coordRig
	sh *shared
}

func newRepNode(sh *shared, st coordinator.Store, never, leaf <-chan struct{}) *repNode {
	script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
		switch {
		case text == "GO":
			return []studiotest.ToolStep{spawnBG("WORK"), {Say: "started"}}
		case text == "GOHOLD":
			return []studiotest.ToolStep{spawnBG("HOLD"), {Say: "started"}}
		case text == "GOFAIL":
			return []studiotest.ToolStep{spawnBG("FAIL"), {Say: "started"}}
		case text == "DEEP":
			return []studiotest.ToolStep{spawnBG("MID"), {Say: "started"}}
		case text == "RUNHOLD":
			return []studiotest.ToolStep{spawnBG("HOLD"), {Block: never}}
		case text == "WORK":
			return []studiotest.ToolStep{{Say: "work result"}}
		case text == "FAIL":
			return []studiotest.ToolStep{{Err: errors.New("the model broke")}}
		case text == "HOLD":
			return []studiotest.ToolStep{{Block: never}}
		case text == "MID":
			return []studiotest.ToolStep{spawnBG("LEAF"), {Say: "mid first turn"}}
		case text == "LEAF":
			return []studiotest.ToolStep{{Block: leaf}, {Say: "leaf result"}}
		case strings.HasPrefix(text, "[Sub-agent finished: Sub-agent: LEAF"):
			return []studiotest.ToolStep{{Say: "mid final"}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
	r := sh.rig(script, studio.TriggerOptions{})
	host, err := studio.NewCoordinators(r.svc, studio.CoordinatorOptions{Store: st, Clock: sh.clock.Now})
	Expect(err).NotTo(HaveOccurred())
	r.svc.SetCoordinators(host)
	DeferCleanup(host.Close)
	return &repNode{coordRig: &coordRig{trigRig: r, host: host, store: st}, sh: sh}
}

// newRepCluster is a frontend over fresh stores.
func newRepCluster() (*repNode, chan struct{}, chan struct{}) {
	st, err := coordinator.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	never, leaf := make(chan struct{}), make(chan struct{})
	DeferCleanup(func() { close(never) })
	a := newRepNode(newShared(), st, never, leaf)
	return a, never, leaf
}

// desk makes a coordinator that wakes only on events.
func (r *repNode) desk() (agentID, chat string) {
	v, err := r.host.Create(context.Background(), alice, studio.CoordinatorInput{Agent: agents.Agent{Name: "Desk", Model: "m"}})
	Expect(err).NotTo(HaveOccurred())
	return v.Agent.ID, v.Config.ChatID
}

func (r *repNode) say(chat, text string) {
	_, err := r.svc.Send(context.Background(), alice, chat, text)
	Expect(err).NotTo(HaveOccurred())
}

func (r *repNode) tick() { Expect(r.host.Tick(context.Background())).To(Succeed()) }

// reports returns the texts that reached the model of the chat and that tell
// of a helper, over every frontend given.
func reports(chat string, nodes ...*repNode) []string {
	var out []string
	for _, n := range nodes {
		for _, t := range n.h.Sent(chat) {
			if strings.HasPrefix(t, "[Sub-agent ") {
				out = append(out, t)
			}
		}
	}
	return out
}

// helperNamed returns the helper chat of the task.
func (r *repNode) helperNamed(task string) session.ChatRecord {
	all, err := r.svc.List(context.Background(), alice, 200)
	Expect(err).NotTo(HaveOccurred())
	for _, c := range all {
		if c.Kind == session.KindSubAgent && c.Title == "Sub-agent: "+task {
			return c
		}
	}
	Fail("no helper for " + task)
	return session.ChatRecord{}
}

func (r *repNode) due() []coordinator.Report {
	got, err := r.store.DueReports(context.Background(), r.sh.clock.Now().Add(100*24*time.Hour), 100)
	Expect(err).NotTo(HaveOccurred())
	return got
}

func (r *repNode) running(chat string) bool { return turn(r.svc, alice, chat).Running }

var _ = Describe("coordinator reports", func() {
	ctx := context.Background()

	It("a finished helper reports once although the goroutine and the sweeper both run, in the old message format", func() {
		a, _, _ := newRepCluster()
		_, chat := a.desk()
		a.say(chat, "GO")
		until("the report", func() bool { return len(reports(chat, a)) == 1 })
		for range 3 {
			a.tick()
		}
		idle(a.svc, alice, chat)
		time.Sleep(150 * time.Millisecond)
		Expect(reports(chat, a)).To(Equal([]string{"[Sub-agent finished: Sub-agent: WORK]\nwork result"}))
		Expect(a.due()).To(BeEmpty(), "the report is done")
		n, err := a.host.DeliverDue(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeZero())
	})

	It("a stopped, a failed and an interrupted helper are each reported with their status", func() {
		a, _, _ := newRepCluster()
		_, chat := a.desk()
		a.say(chat, "GOFAIL")
		until("the failure report", func() bool { return len(reports(chat, a)) == 1 })
		Expect(reports(chat, a)[0]).To(Equal("[Sub-agent failed: Sub-agent: FAIL]\nno output\nIts work may be incomplete."))
		idle(a.svc, alice, chat)

		a.say(chat, "GOHOLD")
		idle(a.svc, alice, chat)
		hold := a.helperNamed("HOLD")
		until("the helper to run", func() bool { return a.running(hold.ID) })
		Expect(a.svc.Stop(ctx, alice, hold.ID)).To(Succeed())
		until("the stopped report", func() bool { return len(reports(chat, a)) == 2 })
		Expect(reports(chat, a)[1]).To(Equal("[Sub-agent stopped: Sub-agent: HOLD]\nno output\nIts work may be incomplete."))
		idle(a.svc, alice, chat)
	})

	It("an interrupted helper is reported as interrupted", func() {
		a, _, _ := newRepCluster()
		_, chat := a.desk()
		a.say(chat, "GOHOLD")
		idle(a.svc, alice, chat)
		hold := a.helperNamed("HOLD")
		until("the helper to run", func() bool { return a.running(hold.ID) })
		n, err := a.svc.ReapStale(ctx, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1))
		a.tick()
		until("the interrupted report", func() bool { return len(reports(chat, a)) == 1 })
		Expect(reports(chat, a)[0]).To(HavePrefix("[Sub-agent interrupted: Sub-agent: HOLD]\n"))
		Expect(reports(chat, a)[0]).To(HaveSuffix("Its work may be incomplete."))
	})

	It("a helper whose frontend died is reported by another frontend", func() {
		sh := newShared()
		net := testutil.NewFakeBus()
		sh.net = net
		sh.deps = func(d *studio.Deps) {
			d.Bus = studio.NewBroadcastBus(net, nil)
			d.CloseTimeout = 200 * time.Millisecond
		}
		st, err := coordinator.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		never, leaf := make(chan struct{}), make(chan struct{})
		DeferCleanup(func() { close(never) })
		a := newRepNode(sh, st, never, leaf)
		b := newRepNode(sh, st, never, leaf)
		_, chat := a.desk()
		a.say(chat, "GOHOLD")
		idle(a.svc, alice, chat)
		hold := a.helperNamed("HOLD")
		until("the helper to run", func() bool { return a.running(hold.ID) })

		// A dies: its goroutines stop. Its claim on the turn of the helper stays
		// until the reaper of the cluster, running on B, finds it stale.
		_ = a.svc.Close(ctx)
		Expect(a.due()).To(HaveLen(1), "the row is in the store, not in A")
		n, err := b.svc.ReapStale(ctx, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1))
		b.tick()
		until("B to deliver the report", func() bool { return len(reports(chat, b)) == 1 })
		Expect(reports(chat, b)[0]).To(MatchRegexp(`^\[Sub-agent (interrupted|stopped): Sub-agent: HOLD\]`))
		Expect(reports(chat, a)).To(BeEmpty())
		Expect(b.due()).To(BeEmpty())
	})

	It("a report claimed by a dead frontend is delivered once after the lease", func() {
		a, _, _ := newRepCluster()
		id, chat := a.desk()
		// The report is due but not delivered: the coordinator is paused when
		// the helper ends.
		a.say(chat, "GOHOLD")
		idle(a.svc, alice, chat)
		hold := a.helperNamed("HOLD")
		until("the helper to run", func() bool { return a.running(hold.ID) })
		_, _, err := a.store.SetPaused(ctx, id, true, alice, a.sh.clock.Now())
		Expect(err).NotTo(HaveOccurred())
		Expect(a.svc.Stop(ctx, alice, hold.ID)).To(Succeed())
		until("the helper to end", func() bool { return turn(a.svc, alice, hold.ID).Last == store.TurnStopped })
		_, _, err = a.store.SetPaused(ctx, id, false, "", a.sh.clock.Now())
		Expect(err).NotTo(HaveOccurred())

		// Another frontend claimed the report and died before it finished.
		won, err := a.store.ClaimReport(ctx, hold.ID, a.sh.clock.Now(), a.sh.clock.Now().Add(2*time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(won).To(BeTrue())

		a.tick()
		delivered, err := a.host.DeliverReport(ctx, hold.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(delivered).To(BeFalse(), "the lease is still running")
		time.Sleep(100 * time.Millisecond)
		Expect(reports(chat, a)).To(BeEmpty())

		a.sh.clock.Advance(3 * time.Minute)
		a.tick()
		until("the report after the lease", func() bool { return len(reports(chat, a)) == 1 })
		// The late original attempt, and more ticks, deliver nothing.
		delivered, err = a.host.DeliverReport(ctx, hold.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(delivered).To(BeFalse())
		a.tick()
		idle(a.svc, alice, chat)
		time.Sleep(150 * time.Millisecond)
		Expect(reports(chat, a)).To(HaveLen(1))
	})

	It("two frontends that deliver at once deliver once", func() {
		sh := newShared()
		st, err := coordinator.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		never, leaf := make(chan struct{}), make(chan struct{})
		DeferCleanup(func() { close(never) })
		a := newRepNode(sh, st, never, leaf)
		b := newRepNode(sh, st, never, leaf)
		id, chat := a.desk()
		a.say(chat, "GOHOLD")
		idle(a.svc, alice, chat)
		hold := a.helperNamed("HOLD")
		until("the helper to run", func() bool { return a.running(hold.ID) })
		_, _, err = a.store.SetPaused(ctx, id, true, alice, sh.clock.Now())
		Expect(err).NotTo(HaveOccurred())
		Expect(a.svc.Stop(ctx, alice, hold.ID)).To(Succeed())
		until("the helper to end", func() bool { return turn(a.svc, alice, hold.ID).Last == store.TurnStopped })
		_, _, err = a.store.SetPaused(ctx, id, false, "", sh.clock.Now())
		Expect(err).NotTo(HaveOccurred())
		done := make(chan struct{}, 2)
		for _, n := range []*repNode{a, b} {
			go func() { defer GinkgoRecover(); n.tick(); done <- struct{}{} }()
		}
		<-done
		<-done
		until("the report", func() bool { return len(reports(chat, a, b)) >= 1 })
		time.Sleep(200 * time.Millisecond)
		Expect(reports(chat, a, b)).To(HaveLen(1))
	})

	It("a resumed coordinator is told that its helper was interrupted", func() {
		sh := newShared()
		net := testutil.NewFakeBus()
		sh.net = net
		sh.deps = func(d *studio.Deps) { d.Bus = studio.NewBroadcastBus(net, nil); d.AutoResume = true }
		st, err := coordinator.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		never, leaf := make(chan struct{}), make(chan struct{})
		DeferCleanup(func() { close(never) })
		a := newRepNode(sh, st, never, leaf)
		_, chat := a.desk()
		a.say(chat, "RUNHOLD")
		until("the helper to run", func() bool {
			kids := childrenOf(a.svc, alice, chat)
			return len(kids) == 1 && a.running(kids[0].ID)
		})
		// The process dies; a new one starts on the same stores. The turn of the
		// coordinator and the turn of its helper end as interrupted. The helper
		// is not resumed (its parent decides); the coordinator is.
		_ = a.svc.Close(ctx)
		b := newRepNode(sh, st, never, leaf)
		_, err = b.svc.ReapStale(ctx, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(turn(b.svc, alice, chat).Running).To(BeFalse())
		Expect(turn(b.svc, alice, chat).Last).To(Equal(store.TurnInterrupted))
		Expect(turn(b.svc, alice, chat).WillResume).To(BeTrue(), "the coordinator chat resumes by itself")
		hold := childrenOf(b.svc, alice, chat)[0]
		Expect(turn(b.svc, alice, hold.ID).WillResume).To(BeFalse())
		Expect(turn(b.svc, alice, hold.ID).Last).To(Equal(store.TurnInterrupted))

		b.tick()
		until("the notice", func() bool { return len(reports(chat, b)) == 1 })
		Expect(reports(chat, b)[0]).To(HavePrefix("[Sub-agent interrupted: Sub-agent: HOLD]"))
		idle(b.svc, alice, chat)
		Expect(b.due()).To(BeEmpty())
		Expect(reports(chat, a, b)).To(HaveLen(1))
	})

	It("reports wait while the coordinator is paused and arrive after resume", func() {
		a, _, _ := newRepCluster()
		id, chat := a.desk()
		a.say(chat, "GOHOLD")
		idle(a.svc, alice, chat)
		hold := a.helperNamed("HOLD")
		until("the helper to run", func() bool { return a.running(hold.ID) })
		owner := studio.CoordinatorActor{User: alice}
		_, err := a.host.Pause(ctx, owner, id)
		Expect(err).NotTo(HaveOccurred())
		until("the helper to be cut", func() bool { return turn(a.svc, alice, hold.ID).Last == store.TurnStopped })
		a.tick()
		time.Sleep(150 * time.Millisecond)
		Expect(reports(chat, a)).To(BeEmpty(), "no noise while paused")
		Expect(pendingTexts(a.svc, alice, chat)).To(BeEmpty())
		Expect(a.due()).To(HaveLen(1), "the report is kept")

		_, err = a.host.Resume(ctx, owner, id)
		Expect(err).NotTo(HaveOccurred())
		a.tick()
		until("the report after resume", func() bool { return len(reports(chat, a)) == 1 })
		Expect(reports(chat, a)[0]).To(HavePrefix("[Sub-agent stopped: Sub-agent: HOLD]"))
	})

	It("a helper with running helpers of its own is not reported until they end", func() {
		a, _, leaf := newRepCluster()
		_, chat := a.desk()
		a.say(chat, "DEEP")
		mid := func() session.ChatRecord { return a.helperNamed("MID") }
		until("MID to end its first turn", func() bool {
			all, _ := a.svc.List(ctx, alice, 200)
			for _, c := range all {
				if c.Title == "Sub-agent: LEAF" {
					return !turn(a.svc, alice, mid().ID).Running && turn(a.svc, alice, mid().ID).Last != ""
				}
			}
			return false
		})
		leafChat := a.helperNamed("LEAF")
		until("LEAF to run", func() bool { return a.running(leafChat.ID) })
		a.tick()
		time.Sleep(150 * time.Millisecond)
		Expect(reports(chat, a)).To(BeEmpty(), "MID ended its turn but its helper runs")
		Expect(a.due()).To(HaveLen(2), "MID and LEAF are both open")

		// LEAF ends after the first turn of its parent. Its report starts a
		// second turn of MID, and only the end of that turn reports MID.
		close(leaf)
		until("LEAF to be reported to MID", func() bool { return len(reports(mid().ID, a)) == 1 })
		Expect(reports(mid().ID, a)[0]).To(Equal("[Sub-agent finished: Sub-agent: LEAF]\nleaf result"))
		Expect(reports(chat, a)).To(BeEmpty())
		until("MID to end its second turn", func() bool {
			a.tick()
			return len(reports(chat, a)) == 1
		})
		Expect(reports(chat, a)[0]).To(Equal("[Sub-agent finished: Sub-agent: MID]\nmid final"))
		idle(a.svc, alice, chat)
		Expect(a.due()).To(BeEmpty())
		Expect(reports(chat, a)).To(HaveLen(1))
	})

	It("a chat that is not in a coordinator tree gets no report row", func() {
		a, _, _ := newRepCluster()
		id := create(a.svc, alice)
		sent(a.svc, alice, id, "GO")
		until("the report as a message", func() bool { return len(reports(id, a)) == 1 })
		Expect(reports(id, a)[0]).To(Equal("[Sub-agent finished: Sub-agent: WORK]\nwork result"))
		Expect(a.due()).To(BeEmpty())
	})

	It("a deleted parent chat finishes the report without a message and the next wake recreates the chat", func() {
		a, _, _ := newRepCluster()
		id, chat := a.desk()
		a.say(chat, "GOHOLD")
		idle(a.svc, alice, chat)
		hold := a.helperNamed("HOLD")
		until("the helper to run", func() bool { return a.running(hold.ID) })
		Expect(a.svc.Delete(ctx, alice, chat)).To(Succeed())
		Expect(a.svc.Stop(ctx, alice, hold.ID)).To(Succeed())
		a.tick()
		until("the report to be settled", func() bool { return len(a.due()) == 0 })
		Expect(reports(chat, a)).To(BeEmpty())

		Expect(a.host.Wake(ctx, id, studio.WakeOwner)).To(Succeed())
		cfg, err := a.store.Get(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.ChatID).NotTo(Equal(chat))
		until("the new chat to run", func() bool { return len(a.h.Sent(cfg.ChatID)) == 1 })
		Expect(reports(cfg.ChatID, a)).To(BeEmpty())
	})

	It("a report older than 7 days is dropped", func() {
		a, _, _ := newRepCluster()
		id, chat := a.desk()
		a.say(chat, "GOHOLD")
		idle(a.svc, alice, chat)
		hold := a.helperNamed("HOLD")
		until("the helper to run", func() bool { return a.running(hold.ID) })
		_, _, err := a.store.SetPaused(ctx, id, true, alice, a.sh.clock.Now())
		Expect(err).NotTo(HaveOccurred())
		a.tick()
		Expect(a.svc.Stop(ctx, alice, hold.ID)).To(Succeed())
		Expect(a.due()).To(HaveLen(1))
		_, _, err = a.store.SetPaused(ctx, id, false, "", a.sh.clock.Now())
		Expect(err).NotTo(HaveOccurred())
		a.sh.clock.Advance(8 * 24 * time.Hour)
		a.tick()
		Expect(a.due()).To(BeEmpty())
		time.Sleep(150 * time.Millisecond)
		Expect(reports(chat, a)).To(BeEmpty(), "an expired report is dropped, not delivered")
	})
})
