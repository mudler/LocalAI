package triggers_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/wstest"
)

const folder = "/users/1/Contracts"

// armed makes the first pass, which reads the cursor, so that later writes
// are changes the Runner has to notice.
func armed(mutate ...func(*triggers.Options)) *rig {
	r := newRig(mutate...)
	r.write("/users/1/home.txt", "x") // some history before the trigger
	r.put(fileTrigger("t1", folder))
	r.sync()
	return r
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

// chatNotRecorded fails every write that records a chat for a run.
type chatNotRecorded struct{ triggers.Store }

func (s *chatNotRecorded) SetRunStatus(c context.Context, run triggers.Run) error {
	if run.Status == triggers.StatusRunning && run.ChatID != "" {
		return errBoom
	}
	return s.Store.SetRunStatus(c, run)
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

var _ = Describe("Folder triggers", func() {
	It("a file starts a run after its quiet period", func() {
		r := armed()
		r.write(folder+"/a.txt", "hello")
		r.sync()
		r.after(29 * time.Second)
		Expect(r.runs("t1")).To(BeEmpty(), "a run before the quiet period ended")
		r.after(2 * time.Second)
		runs := r.runs("t1")
		Expect(runs).To(HaveLen(1))
		n, _ := r.ws.Stat(ctx, wstest.AdminActor, folder+"/a.txt")
		Expect(runs[0].File).To(Equal(folder+"/a.txt"), "run = %+v", runs[0])
		Expect(runs[0].DueKey).To(Equal(fmt.Sprintf("%d@%s", n.ID, n.Blob)), "run = %+v", runs[0])
		Expect(runs[0].Status).To(Equal(triggers.StatusRunning), "run = %+v", runs[0])
		got := r.host.byRun[runs[0].ID]
		Expect(got.ID).To(Equal("t1"), "Start got %+v", got)
		r.after(10 * time.Minute)
		Expect(r.runs("t1")).To(HaveLen(1), "the same file started a second run")
	})

	It("a trigger with a quiet period of its own", func() {
		r := newRig()
		tr := fileTrigger("t1", folder)
		tr.Quiet = 5 * time.Second
		r.put(tr)
		r.sync()
		r.write(folder+"/a.txt", "hello")
		r.sync()
		r.after(6 * time.Second)
		Expect(r.runs("t1")).To(HaveLen(1))
	})

	It("a change in the quiet period restarts it and only the last version runs", func() {
		r := armed()
		r.write(folder+"/a.txt", "v1")
		r.sync()
		r.after(20 * time.Second)
		r.write(folder+"/a.txt", "v2")
		r.sync()
		r.after(20 * time.Second) // 40 s after the first write, 20 s after the second
		Expect(r.runs("t1")).To(BeEmpty(), "the quiet period did not restart")
		r.after(11 * time.Second)
		runs := r.runs("t1")
		Expect(runs).To(HaveLen(1))
		n, _ := r.ws.Stat(ctx, wstest.AdminActor, folder+"/a.txt")
		Expect(runs[0].DueKey).To(Equal(fmt.Sprintf("%d@%s", n.ID, n.Blob)), "the run is for another version: %s", runs[0].DueKey)
	})

	It("a new version after the run starts another run but the same content does not", func() {
		r := armed()
		r.write(folder+"/a.txt", "v1")
		r.sync()
		r.after(31 * time.Second)
		r.finish(r.latest("t1"), triggers.StatusDone, "")
		r.write(folder+"/a.txt", "v1") // written again with the same content
		r.sync()
		r.after(31 * time.Second)
		Expect(r.runs("t1")).To(HaveLen(1))
		r.write(folder+"/a.txt", "v2")
		r.sync()
		r.after(31 * time.Second)
		Expect(r.runs("t1")).To(HaveLen(2))
	})

	It("a file deleted or moved out before it settles starts no run", func() {
		r := armed()
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
		Expect(runs).To(HaveLen(1), "runs = %+v", runs)
		Expect(runs[0].File).To(Equal(folder+"/kept.txt"), "runs = %+v", runs)
	})

	It("a file moved inside the folder runs at its new path", func() {
		r := armed()
		r.write(folder+"/a.txt", "x")
		r.sync()
		r.after(10 * time.Second)
		r.move(folder+"/a.txt", folder+"/sub/b.txt")
		r.sync()
		r.after(40 * time.Second)
		runs := r.runs("t1")
		Expect(runs).To(HaveLen(1), "runs = %+v", runs)
		Expect(runs[0].File).To(Equal(folder+"/sub/b.txt"), "runs = %+v", runs)
	})

	It("a folder moved while its files wait carries them", func() {
		r := armed()
		r.write(folder+"/in/a.txt", "x")
		r.write(folder+"/out/b.txt", "y")
		r.sync()
		r.after(10 * time.Second)
		r.move(folder+"/in", folder+"/renamed")
		r.move(folder+"/out", "/users/1/away")
		r.sync()
		r.after(40 * time.Second)
		runs := r.runs("t1")
		Expect(runs).To(HaveLen(1), "runs = %+v", runs)
		Expect(runs[0].File).To(Equal(folder+"/renamed/a.txt"), "runs = %+v", runs)
	})

	It("a file moved into the folder is an arrival", func() {
		r := armed()
		r.write("/users/1/Inbox/a.txt", "x")
		r.sync()
		r.after(time.Minute)
		r.move("/users/1/Inbox/a.txt", folder+"/a.txt")
		r.sync()
		r.after(40 * time.Second)
		runs := r.runs("t1")
		Expect(runs).To(HaveLen(1), "runs = %+v", runs)
		Expect(runs[0].File).To(Equal(folder+"/a.txt"), "runs = %+v", runs)
	})

	It("only files below the folder count", func() {
		r := armed()
		r.write(folder+"x/a.txt", "other folder with the same start")
		r.write(folder+".txt", "a file named like the folder")
		r.write("/users/1/a.txt", "parent")
		_, err := r.ws.Mkdir(ctx, wstest.AdminActor, folder+"/empty")
		Expect(err).NotTo(HaveOccurred())
		r.write(folder+"/deep/er/still/a.txt", "deep")
		r.sync()
		r.after(time.Minute)
		runs := r.runs("t1")
		Expect(runs).To(HaveLen(1), "runs = %+v", runs)
		Expect(runs[0].File).To(Equal(folder+"/deep/er/still/a.txt"), "runs = %+v", runs)
	})

	It("a change before the trigger watched starts no run", func() {
		r := newRig()
		r.sync() // no trigger yet: the cursor follows the feed
		r.write(folder+"/old.txt", "x")
		tr := fileTrigger("t1", folder)
		tr.Since = t0.Add(time.Minute) // armed a minute after the file arrived, before the Runner read it
		r.put(tr)
		r.after(5 * time.Minute)
		Expect(r.runs("t1")).To(BeEmpty(), "a file from before the trigger ran: %+v", r.runs("t1"))
		r.write(folder+"/new.txt", "y")
		r.sync()
		r.after(time.Minute)
		Expect(r.runs("t1")).To(HaveLen(1))
	})

	It("the first pass only watches what changes from now", func() {
		r := newRig()
		r.write(folder+"/old.txt", "x")
		r.put(fileTrigger("t1", folder)) // Since is an hour before t0, and the file is from t0
		r.sync()
		r.after(5 * time.Minute)
		Expect(r.runs("t1")).To(BeEmpty(), "history ran on the first pass")
		rev, ok, _ := r.store.Cursor(ctx)
		Expect(ok).To(BeTrue(), "cursor = %d %v", rev, ok)
		Expect(rev).NotTo(Equal(0), "cursor = %d %v", rev, ok)
	})

	It("a disabled trigger forgets its waiting files", func() {
		r := armed()
		r.write(folder+"/a.txt", "x")
		r.sync()
		tr, _ := r.store.GetTrigger(ctx, "t1")
		tr.Enabled = false
		_ = r.store.PutTrigger(ctx, tr)
		r.after(time.Minute)
		tr.Enabled = true
		_ = r.store.PutTrigger(ctx, tr)
		r.after(time.Minute)
		Expect(r.runs("t1")).To(BeEmpty(), "a file ran after the trigger was switched off and on")
	})

	It("the loop guard ignores what the run of the same trigger writes", func() {
		r := armed()
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
		Expect(r.runs("t1")).To(HaveLen(1), "the run started %d more runs", len(r.runs("t1"))-1)
	})

	It("another chat or a user starts the trigger", func() {
		r := armed()
		other := workspace.Actor{Principal: wstest.Admin, ChatID: "someone-elses-chat"}
		r.writeAs(other, folder+"/by-chat.txt", "x")
		r.write(folder+"/by-user.txt", "y")
		r.sync()
		r.after(31 * time.Second)
		Expect(r.runs("t1")).To(HaveLen(2))
	})

	It("the loop guard only covers the trigger of the run", func() {
		r := newRig()
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
		Expect(r.runs("t1")).To(HaveLen(1), "t1 ran again: %d", len(r.runs("t1")))
		Expect(r.runs("t2")).To(HaveLen(1), "t2 did not run for the file of the chat of t1: %d", len(r.runs("t2")))
	})

	It("file runs of one trigger wait for each other and are not skipped", func() {
		r := armed()
		for _, n := range []string{"a", "b", "c"} {
			r.write(folder+"/"+n+".txt", n)
		}
		r.sync()
		r.after(31 * time.Second)
		got := statuses(r.runs("t1"))
		Expect(got).To(Equal("queued,queued,running"), "statuses %s", got)
		r.finish(r.runs("t1")[2], triggers.StatusDone, "")
		r.sync()
		got = statuses(r.runs("t1"))
		Expect(got).To(Equal("queued,running,done"), "statuses %s", got)
	})

	It("the daily cap skips the files beyond it", func() {
		r := newRig()
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
		got := statuses(runs)
		Expect(got).To(Equal("skipped,queued,running"), "statuses %s", got)
		Expect(runs[0].Reason).To(Equal("daily limit of 2 runs reached"), "reason %q", runs[0].Reason)
	})

	It("the daily cap of a file trigger counts the day of its time zone", func() {
		// Auckland is UTC+13 in October: its day 23 starts at 11:00 UTC on the 22nd.
		r := newRig()
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
		got := r.latest("t1")
		Expect(got.Status).To(Equal(triggers.StatusRunning), "a new day in the trigger's zone: %s %q", got.Status, got.Reason)
	})

	It("two triggers on one folder both run", func() {
		r := newRig(func(o *triggers.Options) { o.MaxRunning = 4 })
		r.put(fileTrigger("t1", folder))
		r.put(fileTrigger("t2", "/users/1"))
		r.sync()
		r.write(folder+"/a.txt", "x")
		r.sync()
		r.after(31 * time.Second)
		Expect(r.runs("t1")).To(HaveLen(1), "t1 %d runs, t2 %d runs", len(r.runs("t1")), len(r.runs("t2")))
		Expect(r.runs("t2")).To(HaveLen(1), "t1 %d runs, t2 %d runs", len(r.runs("t1")), len(r.runs("t2")))
	})

	It("the cursor stays below a waiting file and a restart does not lose it", func() {
		r := armed()
		r.write("/users/1/before.txt", "x")
		r.write(folder+"/a.txt", "x")
		n, _ := r.ws.Stat(ctx, wstest.AdminActor, folder+"/a.txt")
		r.write("/users/1/after.txt", "y")
		r.sync()
		rev, _, _ := r.store.Cursor(ctx)
		Expect(rev).To(Equal(n.Rev - 1))
		r.runner = r.newRunner() // a restart before the quiet period ended
		r.after(10 * time.Second)
		r.after(31 * time.Second)
		Expect(r.runs("t1")).To(HaveLen(1))
		latest, _ := r.ws.History(ctx, wstest.AdminActor, workspace.HistoryQuery{Path: "/", Limit: 1})
		rev, _, _ = r.store.Cursor(ctx)
		Expect(rev).To(Equal(latest[0].Rev))
		r.runner = r.newRunner() // and a restart after it ran
		r.after(time.Minute)
		Expect(r.runs("t1")).To(HaveLen(1))
	})

	It("a feed error after some changes leaves the saved cursor alone", func() {
		r := armed()
		before, _, _ := r.store.Cursor(ctx)
		for i := 0; i < 250; i++ { // more than one page
			r.write(fmt.Sprintf("/users/1/noise/%03d.txt", i), "n")
		}
		r.write(folder+"/a.txt", "x")
		bad := &failingFeed{Store: r.ws, failFrom: 2}
		r.opts.Workspace = bad
		r.runner = r.newRunner()
		Expect(r.runner.Sync(ctx)).NotTo(Succeed(), "Sync hid a feed error")
		Expect(bad.calls).To(BeNumerically(">=", 2), "the feed was read %d times: the error did not hit a later page", bad.calls)
		rev, _, _ := r.store.Cursor(ctx)
		Expect(rev).To(Equal(before), "cursor moved to %d after an error, was %d", rev, before)
	})

	It("a file moved inside the folder is not lost by a restart", func() {
		r := armed()
		r.write(folder+"/a.txt", "x")
		r.sync()
		r.after(10 * time.Second)
		r.move(folder+"/a.txt", folder+"/b.txt")
		r.sync()
		r.runner = r.newRunner() // a restart before the quiet period ended
		r.after(time.Minute)
		r.after(time.Minute)
		runs := r.runs("t1")
		Expect(runs).To(HaveLen(1), "runs = %+v", runs)
		Expect(runs[0].File).To(Equal(folder+"/b.txt"), "runs = %+v", runs)
	})

	It("the loop guard holds when the chat was not recorded", func() {
		r := newRig(func(o *triggers.Options) { o.Store = &chatNotRecorded{Store: o.Store} })
		r.put(fileTrigger("t1", folder))
		_ = r.runner.Sync(ctx)
		r.write(folder+"/a.txt", "contract")
		_ = r.runner.Sync(ctx)
		r.clock.Advance(31 * time.Second)
		_ = r.runner.Sync(ctx) // Start works, but the chat is not recorded
		Expect(r.host.startCount()).To(Equal(1))
		chat := workspace.Actor{Principal: wstest.Admin, ChatID: "chat-1"}
		r.writeAs(chat, folder+"/a.summary.txt", "summary")
		_ = r.runner.Sync(ctx)
		r.clock.Advance(time.Minute)
		_ = r.runner.Sync(ctx)
		Expect(r.host.startCount()).To(Equal(1), "the writes of the run started more runs: %d starts, %d runs", r.host.startCount(), len(r.runs("t1")))
		Expect(r.runs("t1")).To(HaveLen(1), "the writes of the run started more runs: %d starts, %d runs", r.host.startCount(), len(r.runs("t1")))
	})

	It("a file that cannot be read does not block the others", func() {
		var sf *statFails
		r := newRig(func(o *triggers.Options) { sf = &statFails{Store: o.Workspace}; o.Workspace = sf })
		r.put(fileTrigger("t1", folder))
		r.sync()
		r.write(folder+"/a.txt", "a")
		r.write(folder+"/b.txt", "b")
		r.sync()
		sf.path = folder + "/a.txt"
		r.clock.Advance(31 * time.Second)
		Expect(r.runner.Sync(ctx)).NotTo(Succeed(), "Sync hid the error")
		runs := r.runs("t1")
		Expect(runs).To(HaveLen(1), "runs = %+v", runs)
		Expect(runs[0].File).To(Equal(folder+"/b.txt"), "runs = %+v", runs)
		sf.path = "" // the error is gone: the file that waited runs
		r.after(time.Second)
		Expect(r.runs("t1")).To(HaveLen(2))
	})

	It("a long feed is read in pages", func() {
		r := armed()
		for i := 0; i < 450; i++ {
			r.write(fmt.Sprintf("/users/1/noise/%03d.txt", i), "n")
		}
		r.write(folder+"/a.txt", "x")
		r.sync()
		r.after(31 * time.Second)
		Expect(r.runs("t1")).To(HaveLen(1))
	})

	It("cancel forgets the waiting files", func() {
		r := armed()
		r.write(folder+"/a.txt", "x")
		r.sync()
		Expect(r.runner.Cancel(ctx, "t1")).To(Succeed())
		r.after(time.Minute)
		Expect(r.runs("t1")).To(BeEmpty(), "a file ran after Cancel")
	})

	It("without a workspace file triggers are idle", func() {
		r := newRig(func(o *triggers.Options) { o.Workspace = nil })
		r.put(fileTrigger("t1", folder))
		r.write(folder+"/a.txt", "x")
		r.after(time.Minute)
		Expect(r.runs("t1")).To(BeEmpty(), "ran without a workspace")
	})

	// The chat is not recorded, the run writes its summary as its last act, and
	// Finish comes before the next pass reads that write.
	It("the loop guard outlives the run until the feed passes", func() {
		r := newRig(func(o *triggers.Options) { o.Store = &chatNotRecorded{Store: o.Store} })
		r.put(fileTrigger("t1", folder))
		_ = r.runner.Sync(ctx)
		r.write(folder+"/a.txt", "contract")
		_ = r.runner.Sync(ctx)
		r.clock.Advance(31 * time.Second)
		_ = r.runner.Sync(ctx)
		run := r.latest("t1")
		chat := workspace.Actor{Principal: wstest.Admin, ChatID: "chat-1"}
		r.writeAs(chat, folder+"/a.summary.txt", "summary")
		Expect(r.runner.Finish(ctx, run.ID, triggers.StatusDone, "")).To(Succeed())
		_ = r.runner.Sync(ctx)
		r.clock.Advance(time.Minute)
		_ = r.runner.Sync(ctx)
		n := r.host.startCount()
		Expect(n).To(Equal(1), "the summary of the run started its own trigger: %d starts", n)
	})

	It("a file that never reads is dropped and the cursor advances", func() {
		var sf *statFails
		r := newRig(func(o *triggers.Options) { sf = &statFails{Store: o.Workspace}; o.Workspace = sf })
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
		rev, _, _ := r.store.Cursor(ctx)
		Expect(rev).To(Equal(latest[0].Rev))
		sf.path = ""
		r.after(time.Minute)
		Expect(r.runs("t1")).To(BeEmpty(), "a dropped file ran")
	})

	It("a file that fails in a quick burst is not dropped", func() {
		var sf *statFails
		r := newRig(func(o *triggers.Options) { sf = &statFails{Store: o.Workspace}; o.Workspace = sf })
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
		n := len(r.runs("t1"))
		Expect(n).To(Equal(1))
	})

	It("a file that fails for a day is dropped and the cursor advances", func() {
		var sf *statFails
		r := newRig(func(o *triggers.Options) { sf = &statFails{Store: o.Workspace}; o.Workspace = sf })
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
		rev, _, _ := r.store.Cursor(ctx)
		Expect(rev).To(Equal(latest[0].Rev))
		sf.path = ""
		r.after(time.Minute)
		Expect(r.runs("t1")).To(BeEmpty(), "a dropped file ran")
	})

	It("a long quiet period and one transient error do not drop the file", func() {
		var sf *statFails
		r := newRig(func(o *triggers.Options) { sf = &statFails{Store: o.Workspace}; o.Workspace = sf })
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
		n := len(r.runs("t1"))
		Expect(n).To(Equal(1))
	})

	It("a rules file is never an arrival", func() {
		r := armed()
		r.write(folder+"/AGENTS.md", "rules")
		r.write(folder+"/sub/AGENTS.md", "more rules")
		r.write(folder+"/a.txt", "x")
		r.write(folder+"/b.txt", "y")
		r.move(folder+"/b.txt", folder+"/c/AGENTS.md") // a waiting file renamed to the rules file drops its wait
		r.sync()
		r.after(time.Minute)
		runs := r.runs("t1")
		Expect(runs).To(HaveLen(1), "runs = %+v", runs)
		Expect(runs[0].File).To(Equal(folder+"/a.txt"), "runs = %+v", runs)
		r.write(folder+"/AGENTS.md", "edited rules")
		r.sync()
		r.after(time.Minute)
		Expect(r.runs("t1")).To(HaveLen(1), "an edit of the rules file started a run")
	})

})

var _ = Describe("Folder triggers: the time between runs", func() {
	It("a file that settles soon after a run waits for the gap, and its rewrites start one run", func() {
		r := armed()
		r.write(folder+"/data.json", "v1")
		r.sync()
		r.after(31 * time.Second)
		Expect(r.runs("t1")).To(HaveLen(1))
		r.finish(r.latest("t1"), triggers.StatusDone, "")
		// A page that saves after each change it sees writes the file again
		// and again: the writes settle before the gap of one minute is over.
		for _, v := range []string{"v2", "v3", "v4"} {
			r.write(folder+"/data.json", v)
			r.sync()
			r.after(10 * time.Second)
		}
		r.after(21 * time.Second) // 61 s after the first run: the quiet period is over, the gap is not
		Expect(r.runs("t1")).To(HaveLen(1), "a run started inside the gap")
		r.after(10 * time.Second)
		Expect(r.runs("t1")).To(HaveLen(2), "the waiting file never ran")
		n, _ := r.ws.Stat(ctx, wstest.AdminActor, folder+"/data.json")
		Expect(r.latest("t1").DueKey).To(Equal(fmt.Sprintf("%d@%s", n.ID, n.Blob)), "the run is not for the last version")
	})

	It("a trigger with a longer gap of its own", func() {
		r := newRig()
		tr := fileTrigger("t1", folder)
		tr.MinGap = 10 * time.Minute
		r.put(tr)
		r.sync()
		r.write(folder+"/a.txt", "a")
		r.sync()
		r.after(31 * time.Second)
		r.finish(r.latest("t1"), triggers.StatusDone, "")
		r.write(folder+"/b.txt", "b")
		r.sync()
		r.after(5 * time.Minute)
		Expect(r.runs("t1")).To(HaveLen(1), "the second file did not wait for the trigger's gap")
		r.after(5 * time.Minute)
		Expect(r.runs("t1")).To(HaveLen(2))
	})

	It("files that settle in one pass all run", func() {
		r := armed()
		for _, n := range []string{"a", "b"} {
			r.write(folder+"/"+n+".txt", n)
		}
		r.sync()
		r.after(31 * time.Second)
		Expect(r.runs("t1")).To(HaveLen(2))
	})
})
