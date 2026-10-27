package studio_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// openFrontend builds the stores of a standalone server with no database: JSON
// files under one data directory, as the application does. The files service and
// the clock are the ones of sh, so a "restart" is a new frontend on the same data.
func openFrontend(t *testing.T, dir string, sh *shared) *shared {
	t.Helper()
	st, err := store.NewJSON(filepath.Join(dir, "chats"))
	if err != nil {
		t.Fatal(err)
	}
	ag, err := agents.NewJSON(filepath.Join(dir, "chats", "agents"))
	if err != nil {
		t.Fatal(err)
	}
	ns, err := notify.NewJSON(filepath.Join(dir, "chats", "notifications"))
	if err != nil {
		t.Fatal(err)
	}
	ts, err := triggers.NewJSON(filepath.Join(dir, "chats", "triggers"))
	if err != nil {
		t.Fatal(err)
	}
	return &shared{chats: st, files: sh.files, agents: ag, notices: ns, trg: ts, clock: sh.clock, addons: sh.addons}
}

func TestAgentsTriggersAndNotificationsSurviveARestartWithNoDatabase(t *testing.T) {
	ctx, dir := context.Background(), t.TempDir()
	base := newShared(t)

	first := openFrontend(t, dir, base)
	r := first.rig(t, saySummary, studio.TriggerOptions{})
	ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Reviewer", Addons: []string{"office"}})
	if err != nil {
		t.Fatal(err)
	}
	tr := r.schedule(t, studio.TriggerInput{AgentID: ag.ID, Kind: triggers.KindSchedule, Schedule: "once 2026-10-27 08:00", TZ: "UTC"})
	r.host.Close()

	// The server was down across the due time. The new process finds everything.
	base.clock.Advance(2 * time.Hour)
	second := openFrontend(t, dir, base)
	r2 := second.rig(t, saySummary, studio.TriggerOptions{})
	if list, err := r2.svc.Agents(ctx, alice); err != nil || len(list) != 1 || list[0].Name != "Reviewer" {
		t.Fatalf("agents after the restart: %+v %v", list, err)
	}
	if got, err := r2.host.GetTrigger(ctx, alice, tr.ID); err != nil || got.AgentID != ag.ID {
		t.Fatalf("trigger after the restart: %+v %v", got, err)
	}
	r2.pass(t)
	run := r2.waitEnded(t, tr.ID)
	if run.Status != triggers.StatusDone {
		t.Fatalf("run = %+v", run)
	}
	r2.pass(t)
	if runs, _ := r2.host.TriggerRuns(ctx, alice, tr.ID, 0); len(runs) != 1 {
		t.Fatalf("a missed once-trigger ran %d times", len(runs))
	}
	r2.host.Close()

	// A third start still has the notification and does not run the trigger again.
	third := openFrontend(t, dir, base)
	r3 := third.rig(t, saySummary, studio.TriggerOptions{})
	r3.pass(t)
	if runs, _ := r3.host.TriggerRuns(ctx, alice, tr.ID, 0); len(runs) != 1 {
		t.Fatalf("runs after a third start: %d", len(runs))
	}
	if n, err := r3.inbox.Count(ctx, alice); err != nil || n != 1 {
		t.Fatalf("unread after a restart = %d, %v", n, err)
	}
}

func TestAfterLongDowntimeADailyTriggerCatchesUpOnce(t *testing.T) {
	ctx, dir := context.Background(), t.TempDir()
	base := newShared(t)
	r := openFrontend(t, dir, base).rig(t, saySummary, studio.TriggerOptions{})
	tr := r.schedule(t, studio.TriggerInput{})
	r.host.Close()

	base.clock.Advance(3*24*time.Hour + time.Hour)
	r2 := openFrontend(t, dir, base).rig(t, saySummary, studio.TriggerOptions{})
	r2.pass(t)
	run := r2.waitEnded(t, tr.ID)
	if run.Status != triggers.StatusDone || run.Reason != triggers.ReasonCaughtUp {
		t.Fatalf("run = %+v, want a finished catch-up run", run)
	}
	r2.pass(t)
	if runs, _ := r2.host.TriggerRuns(ctx, alice, tr.ID, 0); len(runs) != 1 {
		t.Fatalf("three missed days made %d runs, want one catch-up run", len(runs))
	}
}

func TestANotificationOfARunOnOneFrontendReachesTheClientOfAnother(t *testing.T) {
	sh := newShared(t)
	sh.net = testutil.NewFakeBus()
	leader := sh.rig(t, saySummary, studio.TriggerOptions{})
	other := sh.rig(t, saySummary, studio.TriggerOptions{})
	ch, cancel := other.inbox.Subscribe(alice)
	defer cancel()
	tr := leader.schedule(t, studio.TriggerInput{})
	leader.due(t)
	leader.waitEnded(t, tr.ID)
	select {
	case n := <-ch:
		if n.Kind != notify.KindRunEnded || n.Principal != "" && n.Principal != alice {
			t.Fatalf("notification = %+v", n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the other frontend's client heard nothing")
	}
}
