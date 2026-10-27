package triggers_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/wstest"
)

const folder = "/users/1/Contracts"

// armed makes the first pass, which reads the cursor, so that later writes
// are changes the Runner has to notice.
func armed(t *testing.T, mutate ...func(*triggers.Options)) *rig {
	t.Helper()
	r := newRig(t, mutate...)
	r.write("/users/1/home.txt", "x") // some history before the trigger
	r.put(fileTrigger("t1", folder))
	r.sync()
	return r
}

func TestAFileStartsARunAfterItsQuietPeriod(t *testing.T) {
	r := armed(t)
	r.write(folder+"/a.txt", "hello")
	r.sync()
	r.after(29 * time.Second)
	if len(r.runs("t1")) != 0 {
		t.Fatal("a run before the quiet period ended")
	}
	r.after(2 * time.Second)
	runs := r.runs("t1")
	if len(runs) != 1 {
		t.Fatalf("%d runs, want 1", len(runs))
	}
	n, _ := r.ws.Stat(ctx, wstest.AdminActor, folder+"/a.txt")
	if runs[0].File != folder+"/a.txt" || runs[0].DueKey != fmt.Sprintf("%d@%s", n.ID, n.Blob) || runs[0].Status != triggers.StatusRunning {
		t.Fatalf("run = %+v", runs[0])
	}
	if got := r.host.byRun[runs[0].ID]; got.ID != "t1" {
		t.Fatalf("Start got %+v", got)
	}
	r.after(10 * time.Minute)
	if len(r.runs("t1")) != 1 {
		t.Fatal("the same file started a second run")
	}
}

func TestATriggerWithAQuietPeriodOfItsOwn(t *testing.T) {
	r := newRig(t)
	tr := fileTrigger("t1", folder)
	tr.Quiet = 5 * time.Second
	r.put(tr)
	r.sync()
	r.write(folder+"/a.txt", "hello")
	r.sync()
	r.after(6 * time.Second)
	if len(r.runs("t1")) != 1 {
		t.Fatalf("%d runs after the quiet period of 5 s", len(r.runs("t1")))
	}
}

func TestAChangeInTheQuietPeriodRestartsItAndOnlyTheLastVersionRuns(t *testing.T) {
	r := armed(t)
	r.write(folder+"/a.txt", "v1")
	r.sync()
	r.after(20 * time.Second)
	r.write(folder+"/a.txt", "v2")
	r.sync()
	r.after(20 * time.Second) // 40 s after the first write, 20 s after the second
	if len(r.runs("t1")) != 0 {
		t.Fatal("the quiet period did not restart")
	}
	r.after(11 * time.Second)
	runs := r.runs("t1")
	if len(runs) != 1 {
		t.Fatalf("%d runs, want 1", len(runs))
	}
	n, _ := r.ws.Stat(ctx, wstest.AdminActor, folder+"/a.txt")
	if runs[0].DueKey != fmt.Sprintf("%d@%s", n.ID, n.Blob) {
		t.Fatalf("the run is for another version: %s", runs[0].DueKey)
	}
}

func TestANewVersionAfterTheRunStartsAnotherRunButTheSameContentDoesNot(t *testing.T) {
	r := armed(t)
	r.write(folder+"/a.txt", "v1")
	r.sync()
	r.after(31 * time.Second)
	r.finish(r.latest("t1"), triggers.StatusDone, "")
	r.write(folder+"/a.txt", "v1") // written again with the same content
	r.sync()
	r.after(31 * time.Second)
	if len(r.runs("t1")) != 1 {
		t.Fatalf("%d runs for the same version", len(r.runs("t1")))
	}
	r.write(folder+"/a.txt", "v2")
	r.sync()
	r.after(31 * time.Second)
	if len(r.runs("t1")) != 2 {
		t.Fatalf("%d runs after a new version, want 2", len(r.runs("t1")))
	}
}

func TestAFileDeletedOrMovedOutBeforeItSettlesStartsNoRun(t *testing.T) {
	r := armed(t)
	r.write(folder+"/gone.txt", "x")
	r.write(folder+"/moved.txt", "y")
	r.write(folder+"/kept.txt", "z")
	r.sync()
	r.after(10 * time.Second)
	r.remove(folder + "/gone.txt")
	r.move(folder+"/moved.txt", "/users/1/elsewhere/moved.txt")
	r.sync()
	r.after(40 * time.Second)
	runs := r.runs("t1")
	if len(runs) != 1 || runs[0].File != folder+"/kept.txt" {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestAFileMovedInsideTheFolderRunsAtItsNewPath(t *testing.T) {
	r := armed(t)
	r.write(folder+"/a.txt", "x")
	r.sync()
	r.after(10 * time.Second)
	r.move(folder+"/a.txt", folder+"/sub/b.txt")
	r.sync()
	r.after(40 * time.Second)
	if runs := r.runs("t1"); len(runs) != 1 || runs[0].File != folder+"/sub/b.txt" {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestAFolderMovedWhileItsFilesWaitCarriesThem(t *testing.T) {
	r := armed(t)
	r.write(folder+"/in/a.txt", "x")
	r.write(folder+"/out/b.txt", "y")
	r.sync()
	r.after(10 * time.Second)
	r.move(folder+"/in", folder+"/renamed")
	r.move(folder+"/out", "/users/1/away")
	r.sync()
	r.after(40 * time.Second)
	if runs := r.runs("t1"); len(runs) != 1 || runs[0].File != folder+"/renamed/a.txt" {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestAFileMovedIntoTheFolderIsAnArrival(t *testing.T) {
	r := armed(t)
	r.write("/users/1/Inbox/a.txt", "x")
	r.sync()
	r.after(time.Minute)
	r.move("/users/1/Inbox/a.txt", folder+"/a.txt")
	r.sync()
	r.after(40 * time.Second)
	if runs := r.runs("t1"); len(runs) != 1 || runs[0].File != folder+"/a.txt" {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestOnlyFilesBelowTheFolderCount(t *testing.T) {
	r := armed(t)
	r.write(folder+"x/a.txt", "other folder with the same start")
	r.write(folder+".txt", "a file named like the folder")
	r.write("/users/1/a.txt", "parent")
	if _, err := r.ws.Mkdir(ctx, wstest.AdminActor, folder+"/empty"); err != nil {
		t.Fatal(err)
	}
	r.write(folder+"/deep/er/still/a.txt", "deep")
	r.sync()
	r.after(time.Minute)
	if runs := r.runs("t1"); len(runs) != 1 || runs[0].File != folder+"/deep/er/still/a.txt" {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestAChangeBeforeTheTriggerWatchedStartsNoRun(t *testing.T) {
	r := newRig(t)
	r.sync() // no trigger yet: the cursor follows the feed
	r.write(folder+"/old.txt", "x")
	tr := fileTrigger("t1", folder)
	tr.Since = t0.Add(time.Minute) // armed a minute after the file arrived, before the Runner read it
	r.put(tr)
	r.after(5 * time.Minute)
	if len(r.runs("t1")) != 0 {
		t.Fatalf("a file from before the trigger ran: %+v", r.runs("t1"))
	}
	r.write(folder+"/new.txt", "y")
	r.sync()
	r.after(time.Minute)
	if len(r.runs("t1")) != 1 {
		t.Fatalf("%d runs for the file after Since, want 1", len(r.runs("t1")))
	}
}

func TestTheFirstPassOnlyWatchesWhatChangesFromNow(t *testing.T) {
	r := newRig(t)
	r.write(folder+"/old.txt", "x")
	r.put(fileTrigger("t1", folder)) // Since is an hour before t0, and the file is from t0
	r.sync()
	r.after(5 * time.Minute)
	if len(r.runs("t1")) != 0 {
		t.Fatal("history ran on the first pass")
	}
	rev, ok, _ := r.store.Cursor(ctx)
	if !ok || rev == 0 {
		t.Fatalf("cursor = %d %v", rev, ok)
	}
}

func TestADisabledTriggerForgetsItsWaitingFiles(t *testing.T) {
	r := armed(t)
	r.write(folder+"/a.txt", "x")
	r.sync()
	tr, _ := r.store.GetTrigger(ctx, "t1")
	tr.Enabled = false
	_ = r.store.PutTrigger(ctx, tr)
	r.after(time.Minute)
	tr.Enabled = true
	_ = r.store.PutTrigger(ctx, tr)
	r.after(time.Minute)
	if len(r.runs("t1")) != 0 {
		t.Fatal("a file ran after the trigger was switched off and on")
	}
}

func TestTheLoopGuardIgnoresWhatTheRunOfTheSameTriggerWrites(t *testing.T) {
	r := armed(t)
	r.write(folder+"/a.txt", "contract")
	r.sync()
	r.after(31 * time.Second)
	run := r.latest("t1")
	chat := workspace.Actor{Principal: wstest.Admin, ChatID: run.ChatID}
	r.writeAs(chat, folder+"/a.summary.txt", "summary")
	r.writeAs(chat, folder+"/a.txt", "contract, annotated")
	r.sync()
	r.after(time.Minute)
	r.finish(run, triggers.StatusDone, "")
	r.after(time.Minute)
	if len(r.runs("t1")) != 1 {
		t.Fatalf("the run started %d more runs", len(r.runs("t1"))-1)
	}
}

func TestAnotherChatOrAUserStartsTheTrigger(t *testing.T) {
	r := armed(t)
	other := workspace.Actor{Principal: wstest.Admin, ChatID: "someone-elses-chat"}
	r.writeAs(other, folder+"/by-chat.txt", "x")
	r.write(folder+"/by-user.txt", "y")
	r.sync()
	r.after(31 * time.Second)
	if len(r.runs("t1")) != 2 {
		t.Fatalf("%d runs, want 2", len(r.runs("t1")))
	}
}

func TestTheLoopGuardOnlyCoversTheTriggerOfTheRun(t *testing.T) {
	r := newRig(t)
	r.put(fileTrigger("t1", folder))
	r.put(fileTrigger("t2", folder+"/out"))
	r.sync()
	r.write(folder+"/a.txt", "x")
	r.sync()
	r.after(31 * time.Second)
	run := r.latest("t1")
	chat := workspace.Actor{Principal: wstest.Admin, ChatID: run.ChatID}
	r.writeAs(chat, folder+"/out/result.txt", "r") // below t1's folder and t2's folder
	r.sync()
	r.after(31 * time.Second)
	if len(r.runs("t1")) != 1 {
		t.Fatalf("t1 ran again: %d", len(r.runs("t1")))
	}
	if len(r.runs("t2")) != 1 {
		t.Fatalf("t2 did not run for the file of the chat of t1: %d", len(r.runs("t2")))
	}
}

func TestFileRunsOfOneTriggerWaitForEachOtherAndAreNotSkipped(t *testing.T) {
	r := armed(t)
	for _, n := range []string{"a", "b", "c"} {
		r.write(folder+"/"+n+".txt", n)
	}
	r.sync()
	r.after(31 * time.Second)
	if got := statuses(r.runs("t1")); got != "queued,queued,running" {
		t.Fatalf("statuses %s", got)
	}
	r.finish(r.runs("t1")[2], triggers.StatusDone, "")
	r.sync()
	if got := statuses(r.runs("t1")); got != "queued,running,done" {
		t.Fatalf("statuses %s", got)
	}
}

func TestTheDailyCapSkipsTheFilesBeyondIt(t *testing.T) {
	r := newRig(t)
	tr := fileTrigger("t1", folder)
	tr.DailyCap = 2
	r.put(tr)
	r.sync()
	for _, n := range []string{"a", "b", "c"} {
		r.write(folder+"/"+n+".txt", n)
	}
	r.sync()
	r.after(31 * time.Second)
	runs := r.runs("t1")
	if got := statuses(runs); got != "skipped,queued,running" {
		t.Fatalf("statuses %s", got)
	}
	if runs[0].Reason != "daily limit of 2 runs reached" {
		t.Fatalf("reason %q", runs[0].Reason)
	}
}

func TestTheDailyCapOfAFileTriggerCountsTheDayOfItsTimeZone(t *testing.T) {
	if _, err := time.LoadLocation("Pacific/Auckland"); err != nil {
		t.Skip("no tzdata")
	}
	// Auckland is UTC+13 in October: its day 23 starts at 11:00 UTC on the 22nd.
	r := newRig(t)
	r.clock.Set(time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC))
	tr := fileTrigger("t1", folder)
	tr.DailyCap = 1
	tr.TZ = "Pacific/Auckland"
	r.put(tr)
	r.sync()
	r.write(folder+"/a.txt", "a")
	r.sync()
	r.after(31 * time.Second) // 10:00:31 UTC, 23:00 in Auckland on the 22nd
	r.finish(r.latest("t1"), triggers.StatusDone, "")
	r.at(time.Date(2026, 10, 22, 11, 5, 0, 0, time.UTC)) // 00:05 on the 23rd in Auckland
	r.write(folder+"/b.txt", "b")
	r.sync()
	r.after(31 * time.Second)
	if got := r.latest("t1"); got.Status != triggers.StatusRunning {
		t.Fatalf("a new day in the trigger's zone: %s %q", got.Status, got.Reason)
	}
}

func TestTwoTriggersOnOneFolderBothRun(t *testing.T) {
	r := newRig(t, func(o *triggers.Options) { o.MaxRunning = 4 })
	r.put(fileTrigger("t1", folder))
	r.put(fileTrigger("t2", "/users/1"))
	r.sync()
	r.write(folder+"/a.txt", "x")
	r.sync()
	r.after(31 * time.Second)
	if len(r.runs("t1")) != 1 || len(r.runs("t2")) != 1 {
		t.Fatalf("t1 %d runs, t2 %d runs", len(r.runs("t1")), len(r.runs("t2")))
	}
}

func TestTheCursorStaysBelowAWaitingFileAndARestartDoesNotLoseIt(t *testing.T) {
	r := armed(t)
	r.write("/users/1/before.txt", "x")
	r.write(folder+"/a.txt", "x")
	n, _ := r.ws.Stat(ctx, wstest.AdminActor, folder+"/a.txt")
	r.write("/users/1/after.txt", "y")
	r.sync()
	rev, _, _ := r.store.Cursor(ctx)
	if rev != n.Rev-1 {
		t.Fatalf("cursor %d, want %d (just below the waiting file)", rev, n.Rev-1)
	}
	r.runner = r.newRunner() // a restart before the quiet period ended
	r.after(10 * time.Second)
	r.after(31 * time.Second)
	if len(r.runs("t1")) != 1 {
		t.Fatalf("%d runs after a restart", len(r.runs("t1")))
	}
	latest, _ := r.ws.History(ctx, wstest.AdminActor, workspace.HistoryQuery{Path: "/", Limit: 1})
	if rev, _, _ := r.store.Cursor(ctx); rev != latest[0].Rev {
		t.Fatalf("cursor %d after the file settled, want %d", rev, latest[0].Rev)
	}
	r.runner = r.newRunner() // and a restart after it ran
	r.after(time.Minute)
	if len(r.runs("t1")) != 1 {
		t.Fatalf("%d runs after the second restart", len(r.runs("t1")))
	}
}

func TestAFeedErrorAfterSomeChangesLeavesTheSavedCursorAlone(t *testing.T) {
	r := armed(t)
	before, _, _ := r.store.Cursor(ctx)
	for i := 0; i < 250; i++ { // more than one page
		r.write(fmt.Sprintf("/users/1/noise/%03d.txt", i), "n")
	}
	r.write(folder+"/a.txt", "x")
	bad := &failingFeed{Store: r.ws, failFrom: 2}
	r.opts.Workspace = bad
	r.runner = r.newRunner()
	if err := r.runner.Sync(ctx); err == nil {
		t.Fatal("Sync hid a feed error")
	}
	if bad.calls < 2 {
		t.Fatalf("the feed was read %d times: the error did not hit a later page", bad.calls)
	}
	if rev, _, _ := r.store.Cursor(ctx); rev != before {
		t.Fatalf("cursor moved to %d after an error, was %d", rev, before)
	}
}

// failingFeed fails ChangesSince from its failFrom-th call on.
type failingFeed struct {
	workspace.Store
	failFrom int
	calls    int
}

func (f *failingFeed) ChangesSince(c context.Context, a workspace.Actor, rev int64, n int) ([]workspace.ChangeSet, error) {
	f.calls++
	if f.calls >= f.failFrom {
		return nil, errBoom
	}
	return f.Store.ChangesSince(c, a, rev, n)
}

func TestAFileMovedInsideTheFolderIsNotLostByARestart(t *testing.T) {
	r := armed(t)
	r.write(folder+"/a.txt", "x")
	r.sync()
	r.after(10 * time.Second)
	r.move(folder+"/a.txt", folder+"/b.txt")
	r.sync()
	r.runner = r.newRunner() // a restart before the quiet period ended
	r.after(time.Minute)
	r.after(time.Minute)
	if runs := r.runs("t1"); len(runs) != 1 || runs[0].File != folder+"/b.txt" {
		t.Fatalf("runs = %+v", runs)
	}
}

// chatNotRecorded fails every write that records a chat for a run.
type chatNotRecorded struct{ triggers.Store }

func (s *chatNotRecorded) SetRunStatus(c context.Context, run triggers.Run) error {
	if run.Status == triggers.StatusRunning && run.ChatID != "" {
		return errBoom
	}
	return s.Store.SetRunStatus(c, run)
}

func TestTheLoopGuardHoldsWhenTheChatWasNotRecorded(t *testing.T) {
	r := newRig(t, func(o *triggers.Options) { o.Store = &chatNotRecorded{Store: o.Store} })
	r.put(fileTrigger("t1", folder))
	_ = r.runner.Sync(ctx)
	r.write(folder+"/a.txt", "contract")
	_ = r.runner.Sync(ctx)
	r.clock.Advance(31 * time.Second)
	_ = r.runner.Sync(ctx) // Start works, but the chat is not recorded
	if r.host.startCount() != 1 {
		t.Fatalf("%d starts, want 1", r.host.startCount())
	}
	chat := workspace.Actor{Principal: wstest.Admin, ChatID: "chat-1"}
	r.writeAs(chat, folder+"/a.summary.txt", "summary")
	_ = r.runner.Sync(ctx)
	r.clock.Advance(time.Minute)
	_ = r.runner.Sync(ctx)
	if r.host.startCount() != 1 || len(r.runs("t1")) != 1 {
		t.Fatalf("the writes of the run started more runs: %d starts, %d runs", r.host.startCount(), len(r.runs("t1")))
	}
}

// statFails fails Stat for one path.
type statFails struct {
	workspace.Store
	path string
}

func (s *statFails) Stat(c context.Context, a workspace.Actor, p string) (workspace.Node, error) {
	if p == s.path {
		return workspace.Node{}, errBoom
	}
	return s.Store.Stat(c, a, p)
}

func TestAFileThatCannotBeReadDoesNotBlockTheOthers(t *testing.T) {
	var sf *statFails
	r := newRig(t, func(o *triggers.Options) { sf = &statFails{Store: o.Workspace}; o.Workspace = sf })
	r.put(fileTrigger("t1", folder))
	r.sync()
	r.write(folder+"/a.txt", "a")
	r.write(folder+"/b.txt", "b")
	r.sync()
	sf.path = folder + "/a.txt"
	r.clock.Advance(31 * time.Second)
	if err := r.runner.Sync(ctx); err == nil {
		t.Fatal("Sync hid the error")
	}
	if runs := r.runs("t1"); len(runs) != 1 || runs[0].File != folder+"/b.txt" {
		t.Fatalf("runs = %+v", runs)
	}
	sf.path = "" // the error is gone: the file that waited runs
	r.after(time.Second)
	if len(r.runs("t1")) != 2 {
		t.Fatalf("%d runs, want 2", len(r.runs("t1")))
	}
}

func TestALongFeedIsReadInPages(t *testing.T) {
	r := armed(t)
	for i := 0; i < 450; i++ {
		r.write(fmt.Sprintf("/users/1/noise/%03d.txt", i), "n")
	}
	r.write(folder+"/a.txt", "x")
	r.sync()
	r.after(31 * time.Second)
	if len(r.runs("t1")) != 1 {
		t.Fatalf("%d runs", len(r.runs("t1")))
	}
}

func TestCancelForgetsTheWaitingFiles(t *testing.T) {
	r := armed(t)
	r.write(folder+"/a.txt", "x")
	r.sync()
	if err := r.runner.Cancel(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	r.after(time.Minute)
	if len(r.runs("t1")) != 0 {
		t.Fatal("a file ran after Cancel")
	}
}

func TestWithoutAWorkspaceFileTriggersAreIdle(t *testing.T) {
	r := newRig(t, func(o *triggers.Options) { o.Workspace = nil })
	r.put(fileTrigger("t1", folder))
	r.write(folder+"/a.txt", "x")
	r.after(time.Minute)
	if len(r.runs("t1")) != 0 {
		t.Fatal("ran without a workspace")
	}
}

// The chat is not recorded, the run writes its summary as its last act, and
// Finish comes before the next pass reads that write.
func TestTheLoopGuardOutlivesTheRunUntilTheFeedPasses(t *testing.T) {
	r := newRig(t, func(o *triggers.Options) { o.Store = &chatNotRecorded{Store: o.Store} })
	r.put(fileTrigger("t1", folder))
	_ = r.runner.Sync(ctx)
	r.write(folder+"/a.txt", "contract")
	_ = r.runner.Sync(ctx)
	r.clock.Advance(31 * time.Second)
	_ = r.runner.Sync(ctx)
	run := r.latest("t1")
	chat := workspace.Actor{Principal: wstest.Admin, ChatID: "chat-1"}
	r.writeAs(chat, folder+"/a.summary.txt", "summary")
	if err := r.runner.Finish(run.ID, triggers.StatusDone, ""); err != nil {
		t.Fatal(err)
	}
	_ = r.runner.Sync(ctx)
	r.clock.Advance(time.Minute)
	_ = r.runner.Sync(ctx)
	if n := r.host.startCount(); n != 1 {
		t.Fatalf("the summary of the run started its own trigger: %d starts", n)
	}
}

func TestAFileThatNeverReadsIsDroppedAndTheCursorAdvances(t *testing.T) {
	var sf *statFails
	r := newRig(t, func(o *triggers.Options) { sf = &statFails{Store: o.Workspace}; o.Workspace = sf })
	r.put(fileTrigger("t1", folder))
	r.sync()
	r.write(folder+"/a.txt", "a")
	r.sync()
	sf.path = folder + "/a.txt"
	for i := 0; i < 12; i++ {
		r.clock.Advance(10 * time.Minute) // more than an hour from the first failure
		_ = r.runner.Sync(ctx)
	}
	latest, _ := r.ws.History(ctx, wstest.AdminActor, workspace.HistoryQuery{Path: "/", Limit: 1})
	if rev, _, _ := r.store.Cursor(ctx); rev != latest[0].Rev {
		t.Fatalf("cursor %d, want %d after the file was dropped", rev, latest[0].Rev)
	}
	sf.path = ""
	r.after(time.Minute)
	if len(r.runs("t1")) != 0 {
		t.Fatal("a dropped file ran")
	}
}

func TestAFileThatFailsInAQuickBurstIsNotDropped(t *testing.T) {
	var sf *statFails
	r := newRig(t, func(o *triggers.Options) { sf = &statFails{Store: o.Workspace}; o.Workspace = sf })
	r.put(fileTrigger("t1", folder))
	r.sync()
	r.write(folder+"/a.txt", "a")
	r.sync()
	r.clock.Advance(31 * time.Second)
	sf.path = folder + "/a.txt"
	for i := 0; i < 10; i++ { // ten passes within one second
		_ = r.runner.Sync(ctx)
	}
	sf.path = "" // the error was transient
	r.after(time.Minute)
	if n := len(r.runs("t1")); n != 1 {
		t.Fatalf("%d runs after a transient error of under a second, want 1", n)
	}
}

func TestAFileThatFailsForADayIsDroppedAndTheCursorAdvances(t *testing.T) {
	var sf *statFails
	r := newRig(t, func(o *triggers.Options) { sf = &statFails{Store: o.Workspace}; o.Workspace = sf })
	r.put(fileTrigger("t1", folder))
	r.sync()
	r.write(folder+"/a.txt", "a")
	r.sync()
	sf.path = folder + "/a.txt"
	r.clock.Advance(31 * time.Second)
	for i := 0; i < 5; i++ { // fewer than 10 passes, 24 hours of failures
		_ = r.runner.Sync(ctx)
		r.clock.Advance(6 * time.Hour)
	}
	_ = r.runner.Sync(ctx)
	latest, _ := r.ws.History(ctx, wstest.AdminActor, workspace.HistoryQuery{Path: "/", Limit: 1})
	if rev, _, _ := r.store.Cursor(ctx); rev != latest[0].Rev {
		t.Fatalf("cursor %d, want %d", rev, latest[0].Rev)
	}
	sf.path = ""
	r.after(time.Minute)
	if len(r.runs("t1")) != 0 {
		t.Fatal("a dropped file ran")
	}
}

func TestALongQuietPeriodAndOneTransientErrorDoNotDropTheFile(t *testing.T) {
	var sf *statFails
	r := newRig(t, func(o *triggers.Options) { sf = &statFails{Store: o.Workspace}; o.Workspace = sf })
	tr := fileTrigger("t1", folder)
	tr.Quiet = 24 * time.Hour
	r.put(tr)
	r.sync()
	r.write(folder+"/a.txt", "a")
	r.sync()
	r.clock.Advance(24*time.Hour + time.Second)
	sf.path = folder + "/a.txt"
	_ = r.runner.Sync(ctx) // one failed read
	sf.path = ""
	r.after(time.Minute)
	if n := len(r.runs("t1")); n != 1 {
		t.Fatalf("%d runs after one transient error, want 1", n)
	}
}

func TestARulesFileIsNeverAnArrival(t *testing.T) {
	r := armed(t)
	r.write(folder+"/AGENTS.md", "rules")
	r.write(folder+"/sub/AGENTS.md", "more rules")
	r.write(folder+"/a.txt", "x")
	r.write(folder+"/b.txt", "y")
	r.move(folder+"/b.txt", folder+"/c/AGENTS.md") // a waiting file renamed to the rules file drops its wait
	r.sync()
	r.after(time.Minute)
	runs := r.runs("t1")
	if len(runs) != 1 || runs[0].File != folder+"/a.txt" {
		t.Fatalf("runs = %+v", runs)
	}
	r.write(folder+"/AGENTS.md", "edited rules")
	r.sync()
	r.after(time.Minute)
	if len(r.runs("t1")) != 1 {
		t.Fatal("an edit of the rules file started a run")
	}
}
