package triggers_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

func TestADueScheduleStartsOneRunWithItsDueKey(t *testing.T) {
	r := newRig(t)
	tr := r.put(schedule("t1", "daily 08:00"))
	r.at(t0.Add(59 * time.Minute)) // 07:59
	if r.host.startCount() != 0 {
		t.Fatal("a run started before the due time")
	}
	r.at(t0.Add(time.Hour + 30*time.Second)) // 08:00:30
	if r.host.startCount() != 1 {
		t.Fatalf("%d runs started, want 1", r.host.startCount())
	}
	run := r.latest("t1")
	if run.Status != triggers.StatusRunning || run.DueKey != "2026-10-22T08:00:00Z" || !run.Due.Equal(t0.Add(time.Hour)) {
		t.Fatalf("run = %+v", run)
	}
	if run.ChatID != "chat-1" || run.Reason != "" || !run.Started.Equal(r.clock.Now()) {
		t.Fatalf("run = %+v", run)
	}
	if got := r.host.byRun[run.ID]; got.ID != tr.ID {
		t.Fatalf("Start got trigger %+v", got)
	}
	r.after(time.Minute)
	if r.host.startCount() != 1 {
		t.Fatal("the same due time started a second run")
	}
	r.finish(run, triggers.StatusDone, "")
	if got := r.latest("t1"); got.Status != triggers.StatusDone || got.Ended.IsZero() {
		t.Fatalf("finished run = %+v", got)
	}
	r.at(t0.Add(25*time.Hour + time.Minute)) // next day 08:01
	if r.host.startCount() != 2 || r.latest("t1").DueKey != "2026-10-23T08:00:00Z" {
		t.Fatalf("next day: %d starts, %+v", r.host.startCount(), r.latest("t1"))
	}
}

func TestATriggerThatNeverWatchedStartsToWatchNow(t *testing.T) {
	r := newRig(t)
	tr := schedule("t1", "daily 08:00")
	tr.Since = time.Time{}
	if err := r.store.PutTrigger(ctx, tr); err != nil {
		t.Fatal(err)
	}
	tr.Owner = "1"
	r.at(t0.Add(5 * time.Hour)) // 12:00: 08:00 has passed
	if r.host.startCount() != 0 {
		t.Fatal("a never armed trigger ran for the past")
	}
	if d, _ := r.store.LastDue(ctx, "t1"); !d.Equal(t0.Add(5 * time.Hour)) {
		t.Fatalf("last due = %v", d)
	}
	r.at(t0.Add(26 * time.Hour)) // next day 09:00
	if r.host.startCount() != 1 {
		t.Fatalf("%d starts the next day", r.host.startCount())
	}
}

func TestMissedRunsCatchUpOnce(t *testing.T) {
	r := newRig(t)
	tr := schedule("t1", "daily 08:00")
	tr.Since = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	r.put(tr)
	r.at(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)) // 19th, 20th, 21st and 22nd at 08:00 are past
	if r.host.startCount() != 1 {
		t.Fatalf("%d runs for several missed times, want 1", r.host.startCount())
	}
	run := r.latest("t1")
	if run.DueKey != "2026-10-22T08:00:00Z" || run.Reason != triggers.ReasonCaughtUp {
		t.Fatalf("run = %+v", run)
	}
	if len(r.runs("t1")) != 1 {
		t.Fatalf("%d runs recorded", len(r.runs("t1")))
	}
}

func TestALongGapLeavesNoCatchUpRunsForTheNextPasses(t *testing.T) {
	r := newRig(t)
	tr := schedule("t1", "* * * * *")
	tr.Since = t0.Add(-72 * 24 * time.Hour) // more missed minutes (103680) than one pass counts
	r.put(tr)
	r.sync()
	run := r.latest("t1")
	if run.DueKey != t0.Format(time.RFC3339) || run.Reason != triggers.ReasonCaughtUp {
		t.Fatalf("run = %+v", run)
	}
	if d, _ := r.store.LastDue(ctx, "t1"); !d.Equal(t0) {
		t.Fatalf("last due = %v, want the latest due time %v", d, t0)
	}
	r.finish(run, triggers.StatusDone, "")
	r.after(10 * time.Second)
	if len(r.runs("t1")) != 1 {
		t.Fatalf("%d runs after the next pass, want 1", len(r.runs("t1")))
	}
}

func TestACaughtUpReasonSurvivesAnEmptyFinish(t *testing.T) {
	r := newRig(t)
	tr := schedule("t1", "daily 08:00")
	tr.Since = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	r.put(tr)
	r.at(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	r.finish(r.latest("t1"), triggers.StatusDone, "")
	if got := r.latest("t1"); got.Reason != triggers.ReasonCaughtUp || got.Status != triggers.StatusDone {
		t.Fatalf("run = %+v", got)
	}
}

func TestADueTimeThatIsClaimedAgainAfterACrashStartsNoSecondRun(t *testing.T) {
	r := newRig(t)
	r.put(schedule("t1", "daily 08:00"))
	r.at(t0.Add(time.Hour + time.Second))
	run := r.latest("t1")
	r.finish(run, triggers.StatusDone, "")
	// The crash happened after Claim and before SetLastDue.
	if err := r.store.SetLastDue(ctx, "t1", t0); err != nil {
		t.Fatal(err)
	}
	r.runner = r.newRunner()
	r.after(time.Minute)
	if len(r.runs("t1")) != 1 || r.host.startCount() != 1 {
		t.Fatalf("%d runs, %d starts after a replay", len(r.runs("t1")), r.host.startCount())
	}
	if d, _ := r.store.LastDue(ctx, "t1"); !d.Equal(t0.Add(time.Hour)) {
		t.Fatalf("last due after the replay = %v", d)
	}
}

func TestAnEditedScheduleDoesNotRunForTheTimeBeforeTheEdit(t *testing.T) {
	r := newRig(t)
	first := schedule("t1", "daily 06:00")
	first.Since = t0.Add(-2 * time.Hour) // 05:00
	old := r.put(first)
	r.at(t0.Add(30 * time.Minute)) // 07:30, the 06:00 run starts
	r.finish(r.latest("t1"), triggers.StatusDone, "")
	edited := old
	edited.Schedule = "daily 07:10" // earlier than now, later than the last due time
	edited = triggers.Arm(&old, edited, r.clock.Now())
	if err := r.store.PutTrigger(ctx, edited); err != nil {
		t.Fatal(err)
	}
	r.after(time.Minute)
	if len(r.runs("t1")) != 1 {
		t.Fatalf("%d runs after the edit, want 1", len(r.runs("t1")))
	}
	r.at(t0.Add(24*time.Hour + 20*time.Minute)) // next day 07:20
	if len(r.runs("t1")) != 2 || r.latest("t1").DueKey != "2026-10-23T07:10:00Z" {
		t.Fatalf("runs = %+v", r.runs("t1"))
	}
}

func TestOnceFiresOnceAndNeverAgain(t *testing.T) {
	r := newRig(t)
	r.put(schedule("t1", "once 2026-10-22 09:00"))
	r.at(t0.Add(2*time.Hour + time.Second))
	r.finish(r.latest("t1"), triggers.StatusDone, "")
	r.at(t0.Add(72 * time.Hour))
	if len(r.runs("t1")) != 1 {
		t.Fatalf("%d runs of a once schedule", len(r.runs("t1")))
	}
}

func TestAOnceTimeMissedWhileDownRunsOnce(t *testing.T) {
	r := newRig(t)
	r.put(schedule("t1", "once 2026-10-22 09:00"))
	r.at(t0.Add(48 * time.Hour))
	if len(r.runs("t1")) != 1 || r.latest("t1").Status != triggers.StatusRunning {
		t.Fatalf("runs = %+v", r.runs("t1"))
	}
}

func TestAScheduleIsReadInItsTimeZone(t *testing.T) {
	if _, err := time.LoadLocation("Europe/Rome"); err != nil {
		t.Skip("no tzdata")
	}
	r := newRig(t)
	tr := schedule("t1", "daily 08:00")
	tr.TZ = "Europe/Rome"
	r.put(tr)
	r.at(t0.Add(30 * time.Minute)) // 07:30 UTC is 09:30 in Rome: 08:00 Rome (06:00 UTC) is past, but before Since
	if r.host.startCount() != 0 {
		t.Fatal("ran for a time before the trigger was armed")
	}
	r.at(t0.Add(23 * time.Hour)) // next day 06:00 UTC = 08:00 Rome
	if r.host.startCount() != 1 || r.latest("t1").DueKey != "2026-10-23T06:00:00Z" {
		t.Fatalf("starts %d, run %+v", r.host.startCount(), r.runs("t1"))
	}
}

func TestADayInTheSpringGapStillRunsOnce(t *testing.T) {
	if _, err := time.LoadLocation("America/New_York"); err != nil {
		t.Skip("no tzdata")
	}
	start := time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)
	r := newRig(t)
	r.clock.Set(start)
	tr := schedule("t1", "daily 02:30")
	tr.TZ = "America/New_York"
	tr.Since = start
	r.put(tr)
	r.at(time.Date(2026, 3, 8, 6, 59, 0, 0, time.UTC)) // before the gap ends
	if r.host.startCount() != 0 {
		t.Fatal("started in the gap")
	}
	r.at(time.Date(2026, 3, 8, 7, 1, 0, 0, time.UTC)) // 03:01 EDT
	if r.host.startCount() != 1 || r.latest("t1").DueKey != "2026-03-08T07:00:00Z" {
		t.Fatalf("starts %d, runs %+v", r.host.startCount(), r.runs("t1"))
	}
}

func TestARepeatedHourRunsOnce(t *testing.T) {
	if _, err := time.LoadLocation("America/New_York"); err != nil {
		t.Skip("no tzdata")
	}
	start := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	r := newRig(t)
	r.clock.Set(start)
	tr := schedule("t1", "daily 01:30")
	tr.TZ = "America/New_York"
	tr.Since = start
	r.put(tr)
	r.at(time.Date(2026, 11, 1, 5, 31, 0, 0, time.UTC)) // 01:31 EDT, the first 01:30
	r.finish(r.latest("t1"), triggers.StatusDone, "")
	r.at(time.Date(2026, 11, 1, 6, 31, 0, 0, time.UTC)) // 01:31 EST, the second 01:30
	if len(r.runs("t1")) != 1 {
		t.Fatalf("%d runs in a repeated hour, want 1", len(r.runs("t1")))
	}
}

func TestARunIsSkippedWhileThePreviousOneIsGoing(t *testing.T) {
	r := newRig(t)
	r.put(schedule("t1", "* * * * *"))
	r.after(time.Minute)
	first := r.latest("t1")
	r.after(time.Minute)
	if got := statuses(r.runs("t1")); got != "skipped,running" {
		t.Fatalf("statuses %s", got)
	}
	if skipped := r.latest("t1"); skipped.Reason != triggers.ReasonStillGoing || skipped.ChatID != "" {
		t.Fatalf("skipped run = %+v", skipped)
	}
	r.finish(first, triggers.StatusDone, "")
	r.after(time.Minute)
	if got := statuses(r.runs("t1")); got != "running,skipped,done" {
		t.Fatalf("statuses %s", got)
	}
}

func TestTheDailyCapHoldsAndResetsWithTheDay(t *testing.T) {
	r := newRig(t)
	tr := schedule("t1", "*/10 * * * *")
	tr.DailyCap = 2
	tr.Since = t0
	r.put(tr)
	for i := 0; i < 4; i++ {
		r.after(10 * time.Minute)
		if run := r.latest("t1"); run.Status == triggers.StatusRunning {
			r.finish(run, triggers.StatusDone, "")
		}
	}
	if got := statuses(r.runs("t1")); got != "skipped,skipped,done,done" {
		t.Fatalf("statuses %s", got)
	}
	if got := r.latest("t1").Reason; got != "daily limit of 2 runs reached" {
		t.Fatalf("reason %q", got)
	}
	r.at(time.Date(2026, 10, 23, 0, 10, 0, 0, time.UTC))
	if got := r.latest("t1"); got.Status != triggers.StatusRunning {
		t.Fatalf("the next day: %+v", got)
	}
}

func TestTheDailyCapCountsTheDayOfTheTriggersTimeZone(t *testing.T) {
	if _, err := time.LoadLocation("Pacific/Auckland"); err != nil {
		t.Skip("no tzdata")
	}
	// Auckland is UTC+13 in October: its day 23 starts at 11:00 UTC on the 22nd.
	start := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	r := newRig(t)
	r.clock.Set(start)
	tr := schedule("t1", "*/30 * * * *")
	tr.TZ = "Pacific/Auckland"
	tr.DailyCap = 1
	tr.Since = start
	r.put(tr)
	r.after(30 * time.Minute) // 10:30 UTC: Auckland 23:30 on the 22nd
	r.finish(r.latest("t1"), triggers.StatusDone, "")
	r.after(15 * time.Minute)                            // 10:45 UTC, nothing due
	r.at(time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)) // 00:00 on the 23rd in Auckland
	if got := r.latest("t1"); got.Status != triggers.StatusRunning {
		t.Fatalf("a new day in the trigger's zone: %s %q", got.Status, got.Reason)
	}
}

func TestARunDueBeforeMidnightCountsForTheDayItIsDueIn(t *testing.T) {
	r := newRig(t)
	tr := schedule("t1", "0,59 23 * * *")
	tr.DailyCap = 1
	tr.Since = t0
	r.put(tr)
	r.at(time.Date(2026, 10, 22, 23, 0, 30, 0, time.UTC))
	if got := r.latest("t1"); got.Status != triggers.StatusRunning {
		t.Fatalf("first run of the day: %+v", got)
	}
	r.finish(r.latest("t1"), triggers.StatusDone, "")
	r.at(time.Date(2026, 10, 23, 0, 1, 0, 0, time.UTC)) // the 23:59 run is claimed on the new day
	got := r.latest("t1")
	if got.DueKey != "2026-10-22T23:59:00Z" || got.Status != triggers.StatusSkipped || got.Reason != "daily limit of 1 runs reached" {
		t.Fatalf("a run due on the 22nd was admitted against the 23rd: %+v", got)
	}
}

func TestOnlyMaxRunningRunsAtOnceTheRestWaitInClaimOrder(t *testing.T) {
	r := newRig(t)
	for _, id := range []string{"t1", "t2", "t3"} {
		r.put(schedule(id, "daily 08:00"))
	}
	r.at(t0.Add(time.Hour + time.Second))
	if r.host.startCount() != 2 {
		t.Fatalf("%d runs at once, want 2", r.host.startCount())
	}
	if got := r.latest("t3"); got.Status != triggers.StatusQueued {
		t.Fatalf("third run: %+v", got)
	}
	r.finish(r.latest("t1"), triggers.StatusDone, "")
	r.sync() // Finish asks for a pass; the test makes it
	if got := r.latest("t3"); got.Status != triggers.StatusRunning || r.host.startCount() != 3 {
		t.Fatalf("after a slot was free: %+v, %d starts", got, r.host.startCount())
	}
}

func TestAStartErrorFailsTheRunWithTheErrorCutAt200Characters(t *testing.T) {
	r := newRig(t)
	r.put(schedule("t1", "daily 08:00"))
	r.host.err = errorString(strings.Repeat("é", 300))
	r.at(t0.Add(time.Hour + time.Second))
	got := r.latest("t1")
	if got.Status != triggers.StatusFailed || len([]rune(got.Reason)) != 200 || got.Ended.IsZero() {
		t.Fatalf("run = %q %d runes", got.Status, len([]rune(got.Reason)))
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }

func TestASkipErrorSkipsTheRun(t *testing.T) {
	r := newRig(t)
	r.put(schedule("t1", "daily 08:00"))
	r.host.err = &triggers.SkipError{Reason: triggers.ReasonNotReadable}
	r.at(t0.Add(time.Hour + time.Second))
	if got := r.latest("t1"); got.Status != triggers.StatusSkipped || got.Reason != "folder not readable" {
		t.Fatalf("run = %+v", got)
	}
}

func TestAFailedStartDoesNotStopOtherTriggers(t *testing.T) {
	r := newRig(t, func(o *triggers.Options) {
		inner := o.Start
		o.Start = func(c context.Context, tr triggers.Trigger, run triggers.Run) (string, error) {
			if tr.ID == "t1" {
				return "", errBoom
			}
			return inner(c, tr, run)
		}
	})
	r.put(schedule("t1", "daily 08:00"))
	r.put(schedule("t2", "daily 08:00"))
	r.at(t0.Add(time.Hour + time.Second))
	if r.latest("t1").Status != triggers.StatusFailed || r.latest("t2").Status != triggers.StatusRunning {
		t.Fatalf("t1 %s, t2 %s", r.latest("t1").Status, r.latest("t2").Status)
	}
}

func TestRestartFailsRunsThatNoLongerHaveWork(t *testing.T) {
	r := newRig(t)
	r.put(schedule("t1", "daily 08:00"))
	r.put(schedule("t2", "daily 08:00"))
	r.at(t0.Add(time.Hour + time.Second))
	live := r.latest("t2")
	r.host.live[live.ChatID] = true
	r.runner = r.newRunner() // a restart: the new Runner has not made a pass
	r.sync()
	if got := r.latest("t1"); got.Status != triggers.StatusFailed || got.Reason != "interrupted by a restart" || got.Ended.IsZero() {
		t.Fatalf("t1 = %+v", got)
	}
	if got := r.latest("t2"); got.Status != triggers.StatusRunning {
		t.Fatalf("a live run was failed: %+v", got)
	}
}

func TestRestartStartsQueuedRuns(t *testing.T) {
	r := newRig(t, func(o *triggers.Options) { o.MaxRunning = 1 })
	r.put(schedule("t1", "daily 08:00"))
	r.put(schedule("t2", "daily 08:00"))
	r.at(t0.Add(time.Hour + time.Second))
	if r.latest("t2").Status != triggers.StatusQueued {
		t.Fatal("setup: the second run is not queued")
	}
	r.runner = r.newRunner()
	r.sync() // the first run lost its work, the queued one starts
	if r.latest("t1").Status != triggers.StatusFailed || r.latest("t2").Status != triggers.StatusRunning {
		t.Fatalf("t1 %s, t2 %s", r.latest("t1").Status, r.latest("t2").Status)
	}
}

func TestSwitchedOffAndDeletedTriggersDropTheirQueuedRuns(t *testing.T) {
	r := newRig(t, func(o *triggers.Options) { o.MaxRunning = 1 })
	r.put(schedule("t1", "daily 08:00"))
	r.put(schedule("t2", "daily 08:00"))
	r.put(schedule("t3", "daily 08:00"))
	r.at(t0.Add(time.Hour + time.Second))
	off, _ := r.store.GetTrigger(ctx, "t2")
	off.Enabled = false
	_ = r.store.PutTrigger(ctx, off)
	_ = r.store.DeleteTrigger(ctx, "t3") // its runs go with it
	r.finish(r.latest("t1"), triggers.StatusDone, "")
	r.sync()
	if got := r.latest("t2"); got.Status != triggers.StatusSkipped || got.Reason != triggers.ReasonSwitchedOff {
		t.Fatalf("t2 = %+v", got)
	}
	if got, err := r.store.ListRuns(ctx, "t3", 0); err != nil || len(got) != 0 {
		t.Fatalf("runs of the deleted trigger: %+v, %v", got, err)
	}
	if got := r.host.startCount(); got != 1 {
		t.Fatalf("%d starts, want 1: the deleted trigger must not start", got)
	}
}

func TestRunNowStartsADisabledTriggerAtOnceAndIgnoresTheCap(t *testing.T) {
	r := newRig(t)
	tr := schedule("t1", "daily 08:00")
	tr.Enabled, tr.DailyCap = false, 1
	r.put(tr)
	first, err := r.runner.RunNow(ctx, "t1")
	if err != nil || first.Status != triggers.StatusRunning || first.ChatID != "chat-1" {
		t.Fatalf("RunNow = %+v, %v", first, err)
	}
	if !strings.HasPrefix(first.DueKey, "manual@") {
		t.Fatalf("due key %q", first.DueKey)
	}
	again, err := r.runner.RunNow(ctx, "t1")
	if err != nil || again.Status != triggers.StatusSkipped || again.Reason != triggers.ReasonStillGoing {
		t.Fatalf("second RunNow = %+v, %v", again, err)
	}
	r.finish(first, triggers.StatusDone, "")
	r.clock.Advance(time.Second)
	third, err := r.runner.RunNow(ctx, "t1")
	if err != nil || third.Status != triggers.StatusRunning {
		t.Fatalf("third RunNow = %+v, %v", third, err)
	}
	if _, err := r.runner.RunNow(ctx, "nope"); err == nil {
		t.Fatal("RunNow of an unknown trigger did not fail")
	}
}

func TestCancelDropsQueuedRunsAndStopsTheRunningOne(t *testing.T) {
	r := newRig(t, func(o *triggers.Options) { o.MaxRunning = 1 })
	r.put(schedule("t1", "daily 08:00"))
	r.at(t0.Add(time.Hour + time.Second))
	running := r.latest("t1")
	r.put(schedule("t2", "daily 08:00"))
	if err := r.store.SetLastDue(ctx, "t2", t0); err != nil {
		t.Fatal(err)
	}
	r.after(time.Second) // t2 is due and waits
	if err := r.runner.Cancel(ctx, "t2"); err != nil {
		t.Fatal(err)
	}
	if got := r.latest("t2"); got.Status != triggers.StatusSkipped || got.Reason != triggers.ReasonDropped {
		t.Fatalf("t2 = %+v", got)
	}
	if err := r.runner.Cancel(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	if len(r.host.stopped) != 1 || r.host.stopped[0] != running.ChatID {
		t.Fatalf("stopped %v, want [%s]", r.host.stopped, running.ChatID)
	}
}

func TestAnEditOrASwitchOffDuringARunLetsItFinishAndLaterRunsUseTheNewSettings(t *testing.T) {
	r := newRig(t)
	old := r.put(schedule("t1", "daily 08:00"))
	r.at(t0.Add(time.Hour + time.Second))
	first := r.latest("t1")
	edited := old
	edited.Enabled, edited.Prompt = false, "The new prompt."
	if err := r.store.PutTrigger(ctx, triggers.Arm(&old, edited, r.clock.Now())); err != nil {
		t.Fatal(err)
	}
	r.after(time.Minute)
	if got := r.latest("t1"); got.ID != first.ID || got.Status != triggers.StatusRunning {
		t.Fatalf("the run was disturbed by the switch-off: %+v", got)
	}
	r.finish(first, triggers.StatusDone, "")
	if got := r.latest("t1"); got.Status != triggers.StatusDone {
		t.Fatalf("the run could not finish: %+v", got)
	}
	again := edited
	again.Enabled = true
	if err := r.store.PutTrigger(ctx, triggers.Arm(&edited, again, r.clock.Now())); err != nil {
		t.Fatal(err)
	}
	r.at(t0.Add(25*time.Hour + time.Minute)) // the next day, 08:01
	runs := r.runs("t1")
	if len(runs) != 2 {
		t.Fatalf("%d runs, want 2", len(runs))
	}
	if got := r.host.byRun[runs[0].ID].Prompt; got != "The new prompt." {
		t.Fatalf("the second run got the prompt %q", got)
	}
}

func TestFinish(t *testing.T) {
	r := newRig(t)
	r.put(schedule("t1", "daily 08:00"))
	r.at(t0.Add(time.Hour + time.Second))
	run := r.latest("t1")
	if err := r.runner.Finish(run.ID, triggers.StatusRunning, ""); err == nil {
		t.Fatal("Finish accepted a status that is not an end")
	}
	if err := r.runner.Finish(run.ID, triggers.StatusFailed, strings.Repeat("x", 500)); err != nil {
		t.Fatal(err)
	}
	got := r.latest("t1")
	if got.Status != triggers.StatusFailed || len(got.Reason) != 200 {
		t.Fatalf("run = %q %d", got.Status, len(got.Reason))
	}
	if err := r.runner.Finish(run.ID, triggers.StatusDone, "late"); err != nil {
		t.Fatal(err)
	}
	if r.latest("t1").Status != triggers.StatusFailed || r.latest("t1").Reason == "late" {
		t.Fatal("a second Finish changed the run")
	}
	if err := r.runner.Finish("unknown", triggers.StatusDone, ""); err != nil {
		t.Fatalf("Finish of an unknown run: %v", err)
	}
}

func TestADisabledTriggerDoesNotRun(t *testing.T) {
	r := newRig(t)
	tr := schedule("t1", "daily 08:00")
	tr.Enabled = false
	r.put(tr)
	r.at(t0.Add(2 * time.Hour))
	if r.host.startCount() != 0 || len(r.runs("t1")) != 0 {
		t.Fatal("a disabled trigger ran")
	}
}

func TestABadScheduleInTheStoreIsLoggedAndSkipped(t *testing.T) {
	r := newRig(t)
	r.put(schedule("t1", "whenever"))
	r.put(schedule("t2", "daily 08:00"))
	r.at(t0.Add(time.Hour + time.Second))
	if len(r.runs("t1")) != 0 || len(r.runs("t2")) != 1 {
		t.Fatal("a bad schedule stopped the others")
	}
}

func TestRunDrivesPassesOnTicksAndNotifyAndStopsCleanly(t *testing.T) {
	r := newRig(t, func(o *triggers.Options) { o.Tick = time.Hour })
	r.put(schedule("t1", "daily 08:00"))
	before := runtime.NumGoroutine()
	cctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { r.runner.Run(cctx); close(done) }()
	time.Sleep(50 * time.Millisecond) // the first pass: nothing is due
	if r.host.startCount() != 0 {
		t.Fatal("started early")
	}
	r.clock.Set(t0.Add(time.Hour + time.Second))
	r.runner.Notify()
	waitFor(t, "a run after Notify", func() bool { return r.host.startCount() == 1 })
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}
	r.clock.Set(t0.Add(48 * time.Hour))
	r.runner.Notify()
	time.Sleep(50 * time.Millisecond)
	if r.host.startCount() != 1 {
		t.Fatal("a run started after Run returned")
	}
	waitFor(t, "no goroutine left", func() bool { return runtime.NumGoroutine() <= before })
}

func TestRunStartsAPassOnEachTick(t *testing.T) {
	r := newRig(t, func(o *triggers.Options) { o.Tick = 5 * time.Millisecond })
	r.put(schedule("t1", "daily 08:00"))
	cctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { r.runner.Run(cctx); close(done) }()
	defer func() { cancel(); <-done }()
	r.clock.Set(t0.Add(time.Hour + time.Second))
	waitFor(t, "a run on a tick", func() bool { return r.host.startCount() == 1 })
}

func TestNotifyNeverBlocks(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 1000; i++ {
		r.runner.Notify()
	}
}

func TestNewRunnerNeedsAStoreAndAStart(t *testing.T) {
	if _, err := triggers.NewRunner(triggers.Options{}); err == nil {
		t.Fatal("NewRunner accepted empty options")
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAPanicInStartFailsTheRunAndFreesTheSlot(t *testing.T) {
	once := true
	r := newRig(t, func(o *triggers.Options) {
		inner := o.Start
		o.Start = func(c context.Context, tr triggers.Trigger, run triggers.Run) (string, error) {
			if once {
				once = false
				panic("host bug")
			}
			return inner(c, tr, run)
		}
	})
	r.put(schedule("t1", "daily 08:00"))
	r.at(t0.Add(time.Hour + time.Second))
	got := r.latest("t1")
	if got.Status != triggers.StatusFailed || got.Reason != "could not start: host bug" || got.Ended.IsZero() {
		t.Fatalf("run = %+v", got)
	}
	r.at(t0.Add(25*time.Hour + time.Second))
	if got := r.latest("t1"); got.Status != triggers.StatusRunning {
		t.Fatalf("the next day: %+v", got)
	}
}

func TestRunNowAfterARestartFirstFailsTheRunsOfTheDeadProcess(t *testing.T) {
	r := newRig(t)
	r.put(schedule("t1", "daily 08:00"))
	r.at(t0.Add(time.Hour + time.Second))
	old := r.latest("t1")
	r.runner = r.newRunner() // a restart; no pass yet
	got, err := r.runner.RunNow(ctx, "t1")
	if err != nil || got.Status != triggers.StatusRunning {
		t.Fatalf("RunNow = %+v, %v", got, err)
	}
	if o, _ := r.store.GetRun(ctx, old.ID); o.Status != triggers.StatusFailed || o.Reason != "interrupted by a restart" {
		t.Fatalf("old run = %+v", o)
	}
	r.sync() // recovery must not touch the run that RunNow started
	if now, _ := r.store.GetRun(ctx, got.ID); now.Status != triggers.StatusRunning {
		t.Fatalf("the manual run was failed by recovery: %+v", now)
	}
}

func TestNothingStartsAfterRunHasReturned(t *testing.T) {
	r := newRig(t, func(o *triggers.Options) { o.MaxRunning = 1 })
	r.put(schedule("t1", "daily 08:00"))
	r.put(schedule("t2", "daily 08:00"))
	r.put(schedule("t3", "daily 09:00"))
	r.at(t0.Add(time.Hour + time.Second))
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	r.runner.Run(cctx)
	r.finish(r.latest("t1"), triggers.StatusDone, "")
	if _, err := r.runner.RunNow(ctx, "t3"); !errors.Is(err, triggers.ErrStopped) {
		t.Fatalf("RunNow after shutdown: %v", err)
	}
	if err := r.runner.Sync(ctx); !errors.Is(err, triggers.ErrStopped) {
		t.Fatalf("Sync after shutdown: %v", err)
	}
	if got := r.host.startCount(); got != 1 || r.latest("t2").Status != triggers.StatusQueued {
		t.Fatalf("%d starts, t2 %s", got, r.latest("t2").Status)
	}
}

func TestStartGetsAContextThatOutlivesTheRunNowRequest(t *testing.T) {
	var got context.Context
	r := newRig(t, func(o *triggers.Options) {
		inner := o.Start
		o.Start = func(c context.Context, tr triggers.Trigger, run triggers.Run) (string, error) {
			got = c
			return inner(c, tr, run)
		}
	})
	r.put(schedule("t1", "daily 08:00"))
	req, cancel := context.WithCancel(ctx)
	if _, err := r.runner.RunNow(req, "t1"); err != nil {
		t.Fatal(err)
	}
	cancel()
	if got == nil || got.Err() != nil {
		t.Fatal("the context of Start ended with the request")
	}
}

// failOnceStore fails the write that records the chat of a started run, once.
type failOnceStore struct {
	triggers.Store
	mu     sync.Mutex
	failed bool
}

func (s *failOnceStore) SetRunStatus(c context.Context, run triggers.Run) error {
	s.mu.Lock()
	fail := !s.failed && run.Status == triggers.StatusRunning && run.ChatID != ""
	if fail {
		s.failed = true
	}
	s.mu.Unlock()
	if fail {
		return errBoom
	}
	return s.Store.SetRunStatus(c, run)
}

func TestTheChatIDIsRecordedAfterOneFailedWrite(t *testing.T) {
	r := newRig(t, func(o *triggers.Options) { o.Store = &failOnceStore{Store: o.Store} })
	r.put(schedule("t1", "daily 08:00"))
	r.at(t0.Add(time.Hour + time.Second))
	if got := r.latest("t1"); got.Status != triggers.StatusRunning || got.ChatID != "chat-1" {
		t.Fatalf("run = %+v", got)
	}
}

func TestFinishCancelAndRunNowRaceCleanly(t *testing.T) {
	var rr *rig
	var mu sync.Mutex
	r := newRig(t, func(o *triggers.Options) {
		inner := o.Start
		o.Start = func(c context.Context, tr triggers.Trigger, run triggers.Run) (string, error) {
			id, err := inner(c, tr, run)
			go func() {
				mu.Lock()
				x := rr
				mu.Unlock()
				_ = x.runner.Finish(run.ID, triggers.StatusDone, "")
			}()
			return id, err
		}
	})
	mu.Lock()
	rr = r
	mu.Unlock()
	for _, id := range []string{"t1", "t2", "t3"} {
		r.put(schedule(id, "* * * * *"))
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		r.after(time.Minute)
		wg.Add(2)
		go func() { defer wg.Done(); _ = r.runner.Cancel(ctx, "t3") }()
		go func() { defer wg.Done(); _, _ = r.runner.RunNow(ctx, "t2") }()
	}
	wg.Wait()
	waitFor(t, "every run finished", func() bool {
		running, _ := r.store.RunningRuns(ctx)
		return len(running) == 0
	})
	r.sync()
	waitFor(t, "every run finished", func() bool {
		running, _ := r.store.RunningRuns(ctx)
		queued, _ := r.store.QueuedRuns(ctx)
		return len(running) == 0 && len(queued) == 0
	})
}

// passSignalStore closes passed at the first pass of the Runner.
type passSignalStore struct {
	triggers.Store
	once   sync.Once
	passed chan struct{}
}

func (s *passSignalStore) EnabledTriggers(c context.Context) ([]triggers.Trigger, error) {
	s.once.Do(func() { close(s.passed) })
	return s.Store.EnabledTriggers(c)
}

func TestRunNowRightAfterTheContextEndsIsRefused(t *testing.T) {
	for i := 0; i < 200; i++ {
		var dead bool
		var sig *passSignalStore
		r := newRig(t, func(o *triggers.Options) {
			o.Tick = time.Hour
			sig = &passSignalStore{Store: o.Store, passed: make(chan struct{})}
			o.Store = sig
			inner := o.Start
			o.Start = func(c context.Context, tr triggers.Trigger, run triggers.Run) (string, error) {
				if c.Err() != nil {
					dead = true
				}
				return inner(c, tr, run)
			}
		})
		r.put(schedule("t1", "daily 08:00"))
		cctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { r.runner.Run(cctx); close(done) }()
		<-sig.passed
		cancel()
		_, err := r.runner.RunNow(ctx, "t1") // Run may not have returned yet
		<-done
		if !errors.Is(err, triggers.ErrStopped) || dead || r.host.startCount() != 0 {
			t.Fatalf("iteration %d: err %v, dead ctx %v, %d starts", i, err, dead, r.host.startCount())
		}
	}
}

func TestAFailedStartDoesNotHoldItsSlotForThePass(t *testing.T) {
	r := newRig(t, func(o *triggers.Options) {
		o.MaxRunning = 1
		inner := o.Start
		o.Start = func(c context.Context, tr triggers.Trigger, run triggers.Run) (string, error) {
			if tr.ID == "t1" {
				return "", errBoom
			}
			return inner(c, tr, run)
		}
	})
	r.put(schedule("t1", "daily 08:00"))
	r.put(schedule("t2", "daily 08:00"))
	r.at(t0.Add(time.Hour + time.Second))
	if r.latest("t1").Status != triggers.StatusFailed || r.latest("t2").Status != triggers.StatusRunning {
		t.Fatalf("t1 %s, t2 %s", r.latest("t1").Status, r.latest("t2").Status)
	}
}

func TestAStartThatEndsWithTheRunnerLeavesTheRunQueuedForTheNextRunner(t *testing.T) {
	entered := make(chan struct{})
	r := newRig(t, func(o *triggers.Options) {
		o.Start = func(c context.Context, _ triggers.Trigger, _ triggers.Run) (string, error) {
			close(entered)
			<-c.Done()
			return "", c.Err() // a host that gives up its start when the Runner stops
		}
	})
	r.put(schedule("t1", "daily 08:00"))
	r.clock.Set(t0.Add(time.Hour + time.Second))
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { r.runner.Run(runCtx); close(done) }()
	<-entered
	cancel()
	<-done
	got := r.latest("t1")
	if got.Status != triggers.StatusQueued || !got.Ended.IsZero() || got.Reason != "" {
		t.Fatalf("a run that shutdown interrupted: %+v", got)
	}
	// The next Runner starts it from the queue, with no second run for the due time.
	r.opts.Start = r.host.start
	r.runner = r.newRunner()
	r.sync()
	runs := r.runs("t1")
	if len(runs) != 1 || runs[0].ID != got.ID || runs[0].Status != triggers.StatusRunning || r.host.startCount() != 1 {
		t.Fatalf("after the restart: %s, %d starts", statuses(runs), r.host.startCount())
	}
	// It is still the one run of its trigger: a later due time is skipped.
	r.at(t0.Add(25*time.Hour + time.Second))
	if got := r.latest("t1"); got.Status != triggers.StatusSkipped || got.Reason != triggers.ReasonStillGoing {
		t.Fatalf("the next day: %+v", got)
	}
}

func TestARequeuedRunStillWaitsForTheRunningRunOfItsTrigger(t *testing.T) {
	// A start error while stopping must not break "one run at a time": the
	// requeued run starts only after the running one of the trigger has ended.
	r := newRig(t)
	r.put(schedule("t1", "daily 08:00"))
	r.at(t0.Add(time.Hour + time.Second))
	first := r.latest("t1")
	if first.Status != triggers.StatusRunning {
		t.Fatalf("setup: %+v", first)
	}
	queued := triggers.Run{ID: "q1", TriggerID: "t1", DueKey: "manual@q1", Due: t0, Status: triggers.StatusQueued}
	if _, err := r.store.Claim(ctx, queued); err != nil {
		t.Fatal(err)
	}
	r.after(time.Second)
	if got := statuses(r.runs("t1")); got != "queued,running" && got != "running,queued" {
		t.Fatalf("statuses %s", got)
	}
	r.finish(first, triggers.StatusDone, "")
	r.after(time.Second)
	if r.host.startCount() != 2 {
		t.Fatalf("%d starts, want 2", r.host.startCount())
	}
}

func TestAPanicInStartWhileStoppingStillFailsTheRun(t *testing.T) {
	entered := make(chan struct{})
	r := newRig(t, func(o *triggers.Options) {
		o.Start = func(c context.Context, _ triggers.Trigger, _ triggers.Run) (string, error) {
			close(entered)
			<-c.Done()
			panic("host bug")
		}
	})
	r.put(schedule("t1", "daily 08:00"))
	r.clock.Set(t0.Add(time.Hour + time.Second))
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { r.runner.Run(runCtx); close(done) }()
	<-entered
	cancel()
	<-done
	if got := r.latest("t1"); got.Status != triggers.StatusFailed || got.Reason != "could not start: host bug" {
		t.Fatalf("run = %+v", got)
	}
}

func TestADueRunWhileARequeuedRunWaitsSaysSoAndTheErrorIsLogged(t *testing.T) {
	entered := make(chan struct{})
	var mu sync.Mutex
	var logs []string
	r := newRig(t, func(o *triggers.Options) {
		o.Logf = func(f string, a ...any) { mu.Lock(); logs = append(logs, fmt.Sprintf(f, a...)); mu.Unlock() }
		o.Start = func(c context.Context, _ triggers.Trigger, _ triggers.Run) (string, error) {
			close(entered)
			<-c.Done()
			return "", errors.New("folder gone")
		}
	})
	r.put(schedule("t1", "daily 08:00"))
	r.clock.Set(t0.Add(time.Hour + time.Second))
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { r.runner.Run(runCtx); close(done) }()
	<-entered
	cancel()
	<-done
	old := r.latest("t1")
	mu.Lock()
	joined := strings.Join(logs, "\n")
	mu.Unlock()
	if !strings.Contains(joined, old.ID) || !strings.Contains(joined, "t1") || !strings.Contains(joined, "folder gone") {
		t.Fatalf("the start error was not logged with the run and trigger: %q", joined)
	}
	// Restart a day later: the due time of day 2 passes before the old run starts.
	r.opts.Start = r.host.start
	r.runner = r.newRunner()
	r.at(t0.Add(26 * time.Hour))
	runs := r.runs("t1")
	if len(runs) != 2 || runs[0].Status != triggers.StatusSkipped || runs[0].Reason != triggers.ReasonWaiting {
		t.Fatalf("the newer run: %s %+v", statuses(runs), runs[0])
	}
	if runs[1].ID != old.ID || runs[1].Status != triggers.StatusRunning {
		t.Fatalf("the requeued run: %+v", runs[1])
	}
}
