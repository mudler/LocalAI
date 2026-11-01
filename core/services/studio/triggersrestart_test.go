package studio_test

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// openFrontend builds the stores of a standalone server with no database: JSON
// files under one data directory, as the application does. The files service and
// the clock are the ones of sh, so a "restart" is a new frontend on the same data.
func openFrontend(dir string, sh *shared) *shared {
	st, err := store.NewJSON(filepath.Join(dir, "chats"))
	Expect(err).NotTo(HaveOccurred())
	ag, err := agents.NewJSON(filepath.Join(dir, "chats", "agents"))
	Expect(err).NotTo(HaveOccurred())
	ns, err := notify.NewJSON(filepath.Join(dir, "chats", "notifications"))
	Expect(err).NotTo(HaveOccurred())
	ts, err := triggers.NewJSON(filepath.Join(dir, "chats", "triggers"))
	Expect(err).NotTo(HaveOccurred())
	return &shared{chats: st, files: sh.files, agents: ag, notices: ns, trg: ts, clock: sh.clock, addons: sh.addons}
}

var _ = Describe("triggersrestart", func() {
	It("agents triggers and notifications survive a restart with no database", func() {
		ctx, dir := context.Background(), GinkgoT().TempDir()
		base := newShared()

		first := openFrontend(dir, base)
		r := first.rig(saySummary, studio.TriggerOptions{})
		ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Reviewer", Addons: []string{"office"}})
		Expect(err).NotTo(HaveOccurred())
		tr := r.schedule(studio.TriggerInput{AgentID: ag.ID, Kind: triggers.KindSchedule, Schedule: "once 2026-10-27 08:00", TZ: "UTC"})
		r.host.Close()

		// The server was down across the due time. The new process finds everything.
		base.clock.Advance(2 * time.Hour)
		second := openFrontend(dir, base)
		r2 := second.rig(saySummary, studio.TriggerOptions{})
		list, err := r2.svc.Agents(ctx, alice)
		Expect(err).NotTo(HaveOccurred(), "agents after the restart: %+v %v", list, err)
		Expect(list).To(HaveLen(1), "agents after the restart: %+v %v", list, err)
		Expect(list[0].Name).To(Equal("Reviewer"), "agents after the restart: %+v %v", list, err)
		got, err := r2.host.GetTrigger(ctx, alice, tr.ID)
		Expect(err).NotTo(HaveOccurred(), "trigger after the restart: %+v %v", got, err)
		Expect(got.AgentID).To(Equal(ag.ID), "trigger after the restart: %+v %v", got, err)
		r2.pass()
		run := r2.waitEnded(tr.ID)
		Expect(run.Status).To(Equal(triggers.StatusDone), "run = %+v", run)
		r2.pass()
		runs, _ := r2.host.TriggerRuns(ctx, alice, tr.ID, 0)
		Expect(runs).To(HaveLen(1), "a missed once-trigger ran %d times", len(runs))
		r2.host.Close()

		// A third start still has the notification and does not run the trigger again.
		third := openFrontend(dir, base)
		r3 := third.rig(saySummary, studio.TriggerOptions{})
		r3.pass()
		runs, _ = r3.host.TriggerRuns(ctx, alice, tr.ID, 0)
		Expect(runs).To(HaveLen(1), "runs after a third start: %d", len(runs))
		n, err := r3.inbox.Count(ctx, alice)
		Expect(err).NotTo(HaveOccurred(), "unread after a restart = %d, %v", n, err)
		Expect(n).To(BeNumerically("==", 1), "unread after a restart = %d, %v", n, err)
	})

	It("after long downtime a daily trigger catches up once", func() {
		ctx, dir := context.Background(), GinkgoT().TempDir()
		base := newShared()
		r := openFrontend(dir, base).rig(saySummary, studio.TriggerOptions{})
		tr := r.schedule(studio.TriggerInput{})
		r.host.Close()

		base.clock.Advance(3*24*time.Hour + time.Hour)
		r2 := openFrontend(dir, base).rig(saySummary, studio.TriggerOptions{})
		r2.pass()
		run := r2.waitEnded(tr.ID)
		Expect(run.Status).To(Equal(triggers.StatusDone), "run = %+v, want a finished catch-up run", run)
		Expect(run.Reason).To(Equal(triggers.ReasonCaughtUp), "run = %+v, want a finished catch-up run", run)
		r2.pass()
		runs, _ := r2.host.TriggerRuns(ctx, alice, tr.ID, 0)
		Expect(runs).To(HaveLen(1), "three missed days made %d runs, want one catch-up run", len(runs))
	})

	It("a notification of a run on one frontend reaches the client of another", func() {
		sh := newShared()
		sh.net = testutil.NewFakeBus()
		leader := sh.rig(saySummary, studio.TriggerOptions{})
		other := sh.rig(saySummary, studio.TriggerOptions{})
		ch, cancel := other.inbox.Subscribe(alice)
		defer cancel()
		tr := leader.schedule(studio.TriggerInput{})
		leader.due()
		leader.waitEnded(tr.ID)
		select {
		case n := <-ch:
			if n.Kind != notify.KindRunEnded || n.Source != notify.SourceTrigger || n.Principal != "" && n.Principal != alice {
				Fail(fmt.Sprintf("notification = %+v", n))
			}
		case <-time.After(10 * time.Second):
			Fail("the other frontend's client heard nothing")
		}
	})
})
