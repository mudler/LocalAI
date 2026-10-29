package triggers_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

type errorString string

func (e errorString) Error() string { return string(e) }

func waitFor(what string, ok func() bool) {
	Eventually(ok, 5*time.Second, 5*time.Millisecond).Should(BeTrue(), "timed out waiting for %s", what)
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

// hangingGetRun stands for a database that does not answer: GetRun waits for
// its context.
type hangingGetRun struct{ triggers.Store }

func (hangingGetRun) GetRun(ctx context.Context, _ string) (triggers.Run, error) {
	<-ctx.Done()
	return triggers.Run{}, ctx.Err()
}

var _ = Describe("Runner", func() {
	It("a due schedule starts one run with its due key", func() {
		r := newRig()
		tr := r.put(schedule("t1", "daily 08:00"))
		r.at(t0.Add(59 * time.Minute)) // 07:59
		Expect(r.host.startCount()).To(Equal(0), "a run started before the due time")
		r.at(t0.Add(time.Hour + 30*time.Second)) // 08:00:30
		Expect(r.host.startCount()).To(Equal(1))
		run := r.latest("t1")
		Expect(run.Status).To(Equal(triggers.StatusRunning), "run = %+v", run)
		Expect(run.DueKey).To(Equal("2026-10-22T08:00:00Z"), "run = %+v", run)
		Expect(run.Due.Equal(t0.Add(time.Hour))).To(BeTrue(), "run = %+v", run)
		Expect(run.ChatID).To(Equal("chat-1"), "run = %+v", run)
		Expect(run.Reason).To(Equal(""), "run = %+v", run)
		Expect(run.Started.Equal(r.clock.Now())).To(BeTrue(), "run = %+v", run)
		got := r.host.byRun[run.ID]
		Expect(got.ID).To(Equal(tr.ID), "Start got trigger %+v", got)
		r.after(time.Minute)
		Expect(r.host.startCount()).To(Equal(1), "the same due time started a second run")
		r.finish(run, triggers.StatusDone, "")
		finished := r.latest("t1")
		Expect(finished.Status).To(Equal(triggers.StatusDone), "finished run = %+v", finished)
		Expect(finished.Ended.IsZero()).To(BeFalse(), "finished run = %+v", finished)
		r.at(t0.Add(25*time.Hour + time.Minute)) // next day 08:01
		Expect(r.host.startCount()).To(Equal(2), "next day: %d starts, %+v", r.host.startCount(), r.latest("t1"))
		Expect(r.latest("t1").DueKey).To(Equal("2026-10-23T08:00:00Z"), "next day: %d starts, %+v", r.host.startCount(), r.latest("t1"))
	})

	It("a trigger that never watched starts to watch now", func() {
		r := newRig()
		tr := schedule("t1", "daily 08:00")
		tr.Since = time.Time{}
		Expect(r.store.PutTrigger(ctx, tr)).To(Succeed())
		tr.Owner = "1"
		r.at(t0.Add(5 * time.Hour)) // 12:00: 08:00 has passed
		Expect(r.host.startCount()).To(Equal(0), "a never armed trigger ran for the past")
		d, _ := r.store.LastDue(ctx, "t1")
		Expect(d.Equal(t0.Add(5*time.Hour))).To(BeTrue(), "last due = %v", d)
		r.at(t0.Add(26 * time.Hour)) // next day 09:00
		Expect(r.host.startCount()).To(Equal(1))
	})

	It("missed runs catch up once", func() {
		r := newRig()
		tr := schedule("t1", "daily 08:00")
		tr.Since = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
		r.put(tr)
		r.at(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)) // 19th, 20th, 21st and 22nd at 08:00 are past
		Expect(r.host.startCount()).To(Equal(1))
		run := r.latest("t1")
		Expect(run.DueKey).To(Equal("2026-10-22T08:00:00Z"), "run = %+v", run)
		Expect(run.Reason).To(Equal(triggers.ReasonCaughtUp), "run = %+v", run)
		Expect(r.runs("t1")).To(HaveLen(1))
	})

	It("a long gap leaves no catch up runs for the next passes", func() {
		r := newRig()
		tr := schedule("t1", "* * * * *")
		tr.Since = t0.Add(-72 * 24 * time.Hour) // more missed minutes (103680) than one pass counts
		r.put(tr)
		r.sync()
		run := r.latest("t1")
		Expect(run.DueKey).To(Equal(t0.Format(time.RFC3339)), "run = %+v", run)
		Expect(run.Reason).To(Equal(triggers.ReasonCaughtUp), "run = %+v", run)
		d, _ := r.store.LastDue(ctx, "t1")
		Expect(d.Equal(t0)).To(BeTrue())
		r.finish(run, triggers.StatusDone, "")
		r.after(10 * time.Second)
		Expect(r.runs("t1")).To(HaveLen(1))
	})

	It("a caught up reason survives an empty finish", func() {
		r := newRig()
		tr := schedule("t1", "daily 08:00")
		tr.Since = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
		r.put(tr)
		r.at(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
		r.finish(r.latest("t1"), triggers.StatusDone, "")
		got := r.latest("t1")
		Expect(got.Reason).To(Equal(triggers.ReasonCaughtUp), "run = %+v", got)
		Expect(got.Status).To(Equal(triggers.StatusDone), "run = %+v", got)
	})

	It("a due time that is claimed again after a crash starts no second run", func() {
		r := newRig()
		r.put(schedule("t1", "daily 08:00"))
		r.at(t0.Add(time.Hour + time.Second))
		run := r.latest("t1")
		r.finish(run, triggers.StatusDone, "")
		// The crash happened after Claim and before SetLastDue.
		Expect(r.store.SetLastDue(ctx, "t1", t0)).To(Succeed())
		r.runner = r.newRunner()
		r.after(time.Minute)
		Expect(r.runs("t1")).To(HaveLen(1))
		Expect(r.host.startCount()).To(Equal(1))
		d, _ := r.store.LastDue(ctx, "t1")
		Expect(d.Equal(t0.Add(time.Hour))).To(BeTrue(), "last due after the replay = %v", d)
	})

	It("an edited schedule does not run for the time before the edit", func() {
		r := newRig()
		first := schedule("t1", "daily 06:00")
		first.Since = t0.Add(-2 * time.Hour) // 05:00
		old := r.put(first)
		r.at(t0.Add(30 * time.Minute)) // 07:30, the 06:00 run starts
		r.finish(r.latest("t1"), triggers.StatusDone, "")
		edited := old
		edited.Schedule = "daily 07:10" // earlier than now, later than the last due time
		edited = triggers.Arm(&old, edited, r.clock.Now())
		Expect(r.store.PutTrigger(ctx, edited)).To(Succeed())
		r.after(time.Minute)
		Expect(r.runs("t1")).To(HaveLen(1))
		r.at(t0.Add(24*time.Hour + 20*time.Minute)) // next day 07:20
		Expect(r.runs("t1")).To(HaveLen(2), "runs = %+v", r.runs("t1"))
		Expect(r.latest("t1").DueKey).To(Equal("2026-10-23T07:10:00Z"), "runs = %+v", r.runs("t1"))
	})

	It("once fires once and never again", func() {
		r := newRig()
		r.put(schedule("t1", "once 2026-10-22 09:00"))
		r.at(t0.Add(2*time.Hour + time.Second))
		r.finish(r.latest("t1"), triggers.StatusDone, "")
		r.at(t0.Add(72 * time.Hour))
		Expect(r.runs("t1")).To(HaveLen(1))
	})

	It("a once time missed while down runs once", func() {
		r := newRig()
		r.put(schedule("t1", "once 2026-10-22 09:00"))
		r.at(t0.Add(48 * time.Hour))
		Expect(r.runs("t1")).To(HaveLen(1), "runs = %+v", r.runs("t1"))
		Expect(r.latest("t1").Status).To(Equal(triggers.StatusRunning), "runs = %+v", r.runs("t1"))
	})

	It("a schedule is read in its time zone", func() {
		r := newRig()
		tr := schedule("t1", "daily 08:00")
		tr.TZ = "Europe/Rome"
		r.put(tr)
		r.at(t0.Add(30 * time.Minute)) // 07:30 UTC is 09:30 in Rome: 08:00 Rome (06:00 UTC) is past, but before Since
		Expect(r.host.startCount()).To(Equal(0), "ran for a time before the trigger was armed")
		r.at(t0.Add(23 * time.Hour)) // next day 06:00 UTC = 08:00 Rome
		Expect(r.host.startCount()).To(Equal(1), "starts %d, run %+v", r.host.startCount(), r.runs("t1"))
		Expect(r.latest("t1").DueKey).To(Equal("2026-10-23T06:00:00Z"), "starts %d, run %+v", r.host.startCount(), r.runs("t1"))
	})

	It("a day in the spring gap still runs once", func() {
		start := time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)
		r := newRig()
		r.clock.Set(start)
		tr := schedule("t1", "daily 02:30")
		tr.TZ = "America/New_York"
		tr.Since = start
		r.put(tr)
		r.at(time.Date(2026, 3, 8, 6, 59, 0, 0, time.UTC)) // before the gap ends
		Expect(r.host.startCount()).To(Equal(0), "started in the gap")
		r.at(time.Date(2026, 3, 8, 7, 1, 0, 0, time.UTC)) // 03:01 EDT
		Expect(r.host.startCount()).To(Equal(1), "starts %d, runs %+v", r.host.startCount(), r.runs("t1"))
		Expect(r.latest("t1").DueKey).To(Equal("2026-03-08T07:00:00Z"), "starts %d, runs %+v", r.host.startCount(), r.runs("t1"))
	})

	It("a repeated hour runs once", func() {
		start := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
		r := newRig()
		r.clock.Set(start)
		tr := schedule("t1", "daily 01:30")
		tr.TZ = "America/New_York"
		tr.Since = start
		r.put(tr)
		r.at(time.Date(2026, 11, 1, 5, 31, 0, 0, time.UTC)) // 01:31 EDT, the first 01:30
		r.finish(r.latest("t1"), triggers.StatusDone, "")
		r.at(time.Date(2026, 11, 1, 6, 31, 0, 0, time.UTC)) // 01:31 EST, the second 01:30
		Expect(r.runs("t1")).To(HaveLen(1))
	})

	It("a run is skipped while the previous one is going", func() {
		r := newRig()
		r.put(schedule("t1", "* * * * *"))
		r.after(time.Minute)
		first := r.latest("t1")
		r.after(time.Minute)
		got := statuses(r.runs("t1"))
		Expect(got).To(Equal("skipped,running"), "statuses %s", got)
		skipped := r.latest("t1")
		Expect(skipped.Reason).To(Equal(triggers.ReasonStillGoing), "skipped run = %+v", skipped)
		Expect(skipped.ChatID).To(Equal(""), "skipped run = %+v", skipped)
		r.finish(first, triggers.StatusDone, "")
		r.after(time.Minute)
		got = statuses(r.runs("t1"))
		Expect(got).To(Equal("running,skipped,done"), "statuses %s", got)
	})

	It("the daily cap holds and resets with the day", func() {
		r := newRig()
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
		got := statuses(r.runs("t1"))
		Expect(got).To(Equal("skipped,skipped,done,done"), "statuses %s", got)
		got = r.latest("t1").Reason
		Expect(got).To(Equal("daily limit of 2 runs reached"), "reason %q", got)
		r.at(time.Date(2026, 10, 23, 0, 10, 0, 0, time.UTC))
		next := r.latest("t1")
		Expect(next.Status).To(Equal(triggers.StatusRunning), "the next day: %+v", next)
	})

	It("the daily cap counts the day of the triggers time zone", func() {
		// Auckland is UTC+13 in October: its day 23 starts at 11:00 UTC on the 22nd.
		start := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
		r := newRig()
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
		got := r.latest("t1")
		Expect(got.Status).To(Equal(triggers.StatusRunning), "a new day in the trigger's zone: %s %q", got.Status, got.Reason)
	})

	It("a run due before midnight counts for the day it is due in", func() {
		r := newRig()
		tr := schedule("t1", "0,59 23 * * *")
		tr.DailyCap = 1
		tr.Since = t0
		r.put(tr)
		r.at(time.Date(2026, 10, 22, 23, 0, 30, 0, time.UTC))
		got := r.latest("t1")
		Expect(got.Status).To(Equal(triggers.StatusRunning), "first run of the day: %+v", got)
		r.finish(r.latest("t1"), triggers.StatusDone, "")
		r.at(time.Date(2026, 10, 23, 0, 1, 0, 0, time.UTC)) // the 23:59 run is claimed on the new day
		got = r.latest("t1")
		Expect(got.DueKey).To(Equal("2026-10-22T23:59:00Z"), "a run due on the 22nd was admitted against the 23rd: %+v", got)
		Expect(got.Status).To(Equal(triggers.StatusSkipped), "a run due on the 22nd was admitted against the 23rd: %+v", got)
		Expect(got.Reason).To(Equal("daily limit of 1 runs reached"), "a run due on the 22nd was admitted against the 23rd: %+v", got)
	})

	It("only max running runs at once the rest wait in claim order", func() {
		r := newRig()
		for _, id := range []string{"t1", "t2", "t3"} {
			r.put(schedule(id, "daily 08:00"))
		}
		r.at(t0.Add(time.Hour + time.Second))
		Expect(r.host.startCount()).To(Equal(2))
		got := r.latest("t3")
		Expect(got.Status).To(Equal(triggers.StatusQueued), "third run: %+v", got)
		r.finish(r.latest("t1"), triggers.StatusDone, "")
		r.sync() // Finish asks for a pass; the test makes it
		got = r.latest("t3")
		Expect(got.Status).To(Equal(triggers.StatusRunning), "after a slot was free: %+v, %d starts", got, r.host.startCount())
		Expect(r.host.startCount()).To(Equal(3), "after a slot was free: %+v, %d starts", got, r.host.startCount())
	})

	It("a start error fails the run with the error cut at200 characters", func() {
		r := newRig()
		r.put(schedule("t1", "daily 08:00"))
		r.host.err = errorString(strings.Repeat("é", 300))
		r.at(t0.Add(time.Hour + time.Second))
		got := r.latest("t1")
		Expect(got.Status).To(Equal(triggers.StatusFailed), "run = %q %d runes", got.Status, len([]rune(got.Reason)))
		Expect([]rune(got.Reason)).To(HaveLen(200), "run = %q %d runes", got.Status, len([]rune(got.Reason)))
		Expect(got.Ended.IsZero()).To(BeFalse(), "run = %q %d runes", got.Status, len([]rune(got.Reason)))
	})

	It("a skip error skips the run", func() {
		r := newRig()
		r.put(schedule("t1", "daily 08:00"))
		r.host.err = &triggers.SkipError{Reason: triggers.ReasonNotReadable}
		r.at(t0.Add(time.Hour + time.Second))
		got := r.latest("t1")
		Expect(got.Status).To(Equal(triggers.StatusSkipped), "run = %+v", got)
		Expect(got.Reason).To(Equal("folder not readable"), "run = %+v", got)
	})

	It("a failed start does not stop other triggers", func() {
		r := newRig(func(o *triggers.Options) {
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
		Expect(r.latest("t1").Status).To(Equal(triggers.StatusFailed), "t1 %s, t2 %s", r.latest("t1").Status, r.latest("t2").Status)
		Expect(r.latest("t2").Status).To(Equal(triggers.StatusRunning), "t1 %s, t2 %s", r.latest("t1").Status, r.latest("t2").Status)
	})

	It("restart fails runs that no longer have work", func() {
		r := newRig()
		r.put(schedule("t1", "daily 08:00"))
		r.put(schedule("t2", "daily 08:00"))
		r.at(t0.Add(time.Hour + time.Second))
		live := r.latest("t2")
		r.host.live[live.ChatID] = true
		r.runner = r.newRunner() // a restart: the new Runner has not made a pass
		r.sync()
		got := r.latest("t1")
		Expect(got.Status).To(Equal(triggers.StatusFailed), "t1 = %+v", got)
		Expect(got.Reason).To(Equal("interrupted by a restart"), "t1 = %+v", got)
		Expect(got.Ended.IsZero()).To(BeFalse(), "t1 = %+v", got)
		got = r.latest("t2")
		Expect(got.Status).To(Equal(triggers.StatusRunning), "a live run was failed: %+v", got)
	})

	It("restart starts queued runs", func() {
		r := newRig(func(o *triggers.Options) { o.MaxRunning = 1 })
		r.put(schedule("t1", "daily 08:00"))
		r.put(schedule("t2", "daily 08:00"))
		r.at(t0.Add(time.Hour + time.Second))
		Expect(r.latest("t2").Status).To(Equal(triggers.StatusQueued), "setup: the second run is not queued")
		r.runner = r.newRunner()
		r.sync() // the first run lost its work, the queued one starts
		Expect(r.latest("t1").Status).To(Equal(triggers.StatusFailed), "t1 %s, t2 %s", r.latest("t1").Status, r.latest("t2").Status)
		Expect(r.latest("t2").Status).To(Equal(triggers.StatusRunning), "t1 %s, t2 %s", r.latest("t1").Status, r.latest("t2").Status)
	})

	It("switched off and deleted triggers drop their queued runs", func() {
		r := newRig(func(o *triggers.Options) { o.MaxRunning = 1 })
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
		got := r.latest("t2")
		Expect(got.Status).To(Equal(triggers.StatusSkipped), "t2 = %+v", got)
		Expect(got.Reason).To(Equal(triggers.ReasonSwitchedOff), "t2 = %+v", got)
		deleted, err := r.store.ListRuns(ctx, "t3", 0)
		Expect(err).NotTo(HaveOccurred(), "runs of the deleted trigger")
		Expect(deleted).To(BeEmpty(), "runs of the deleted trigger")
		Expect(r.host.startCount()).To(Equal(1))
	})

	It("run now starts a disabled trigger at once and ignores the cap", func() {
		r := newRig()
		tr := schedule("t1", "daily 08:00")
		tr.Enabled, tr.DailyCap = false, 1
		r.put(tr)
		first, err := r.runner.RunNow(ctx, "t1")
		Expect(err).NotTo(HaveOccurred(), "RunNow = %+v, %v", first, err)
		Expect(first.Status).To(Equal(triggers.StatusRunning), "RunNow = %+v, %v", first, err)
		Expect(first.ChatID).To(Equal("chat-1"), "RunNow = %+v, %v", first, err)
		Expect(strings.HasPrefix(first.DueKey, "manual@")).To(BeTrue(), "due key %q", first.DueKey)
		again, err := r.runner.RunNow(ctx, "t1")
		Expect(err).NotTo(HaveOccurred(), "second RunNow = %+v, %v", again, err)
		Expect(again.Status).To(Equal(triggers.StatusSkipped), "second RunNow = %+v, %v", again, err)
		Expect(again.Reason).To(Equal(triggers.ReasonStillGoing), "second RunNow = %+v, %v", again, err)
		r.finish(first, triggers.StatusDone, "")
		r.clock.Advance(time.Second)
		third, err := r.runner.RunNow(ctx, "t1")
		Expect(err).NotTo(HaveOccurred(), "third RunNow = %+v, %v", third, err)
		Expect(third.Status).To(Equal(triggers.StatusRunning), "third RunNow = %+v, %v", third, err)
		_, err = r.runner.RunNow(ctx, "nope")
		Expect(err).To(HaveOccurred(), "RunNow of an unknown trigger did not fail")
	})

	It("cancel drops queued runs and stops the running one", func() {
		r := newRig(func(o *triggers.Options) { o.MaxRunning = 1 })
		r.put(schedule("t1", "daily 08:00"))
		r.at(t0.Add(time.Hour + time.Second))
		running := r.latest("t1")
		r.put(schedule("t2", "daily 08:00"))
		Expect(r.store.SetLastDue(ctx, "t2", t0)).To(Succeed())
		r.after(time.Second) // t2 is due and waits
		Expect(r.runner.Cancel(ctx, "t2")).To(Succeed())
		got := r.latest("t2")
		Expect(got.Status).To(Equal(triggers.StatusSkipped), "t2 = %+v", got)
		Expect(got.Reason).To(Equal(triggers.ReasonDropped), "t2 = %+v", got)
		Expect(r.runner.Cancel(ctx, "t1")).To(Succeed())
		Expect(r.host.stopped).To(HaveLen(1))
		Expect(r.host.stopped[0]).To(Equal(running.ChatID))
	})

	It("an edit or a switch off during a run lets it finish and later runs use the new settings", func() {
		r := newRig()
		old := r.put(schedule("t1", "daily 08:00"))
		r.at(t0.Add(time.Hour + time.Second))
		first := r.latest("t1")
		edited := old
		edited.Enabled, edited.Prompt = false, "The new prompt."
		Expect(r.store.PutTrigger(ctx, triggers.Arm(&old, edited, r.clock.Now()))).To(Succeed())
		r.after(time.Minute)
		got := r.latest("t1")
		Expect(got.ID).To(Equal(first.ID), "the run was disturbed by the switch-off: %+v", got)
		Expect(got.Status).To(Equal(triggers.StatusRunning), "the run was disturbed by the switch-off: %+v", got)
		r.finish(first, triggers.StatusDone, "")
		got = r.latest("t1")
		Expect(got.Status).To(Equal(triggers.StatusDone), "the run could not finish: %+v", got)
		again := edited
		again.Enabled = true
		Expect(r.store.PutTrigger(ctx, triggers.Arm(&edited, again, r.clock.Now()))).To(Succeed())
		r.at(t0.Add(25*time.Hour + time.Minute)) // the next day, 08:01
		runs := r.runs("t1")
		Expect(runs).To(HaveLen(2))
		prompt := r.host.byRun[runs[0].ID].Prompt
		Expect(prompt).To(Equal("The new prompt."), "the second run got the wrong prompt")
	})

	It("finish", func() {
		r := newRig()
		r.put(schedule("t1", "daily 08:00"))
		r.at(t0.Add(time.Hour + time.Second))
		run := r.latest("t1")
		Expect(r.runner.Finish(ctx, run.ID, triggers.StatusRunning, "")).NotTo(Succeed(), "Finish accepted a status that is not an end")
		Expect(r.runner.Finish(ctx, run.ID, triggers.StatusFailed, strings.Repeat("x", 500))).To(Succeed())
		got := r.latest("t1")
		Expect(got.Status).To(Equal(triggers.StatusFailed), "run = %q %d", got.Status, len(got.Reason))
		Expect(got.Reason).To(HaveLen(200), "run = %q %d", got.Status, len(got.Reason))
		Expect(r.runner.Finish(ctx, run.ID, triggers.StatusDone, "late")).To(Succeed())
		Expect(r.latest("t1").Status).To(Equal(triggers.StatusFailed), "a second Finish changed the run")
		Expect(r.latest("t1").Reason).NotTo(Equal("late"), "a second Finish changed the run")
		err := r.runner.Finish(ctx, "unknown", triggers.StatusDone, "")
		Expect(err).NotTo(HaveOccurred(), "Finish of an unknown run: %v", err)
	})

	It("a disabled trigger does not run", func() {
		r := newRig()
		tr := schedule("t1", "daily 08:00")
		tr.Enabled = false
		r.put(tr)
		r.at(t0.Add(2 * time.Hour))
		Expect(r.host.startCount()).To(Equal(0), "a disabled trigger ran")
		Expect(r.runs("t1")).To(BeEmpty(), "a disabled trigger ran")
	})

	It("a bad schedule in the store is logged and skipped", func() {
		r := newRig()
		r.put(schedule("t1", "whenever"))
		r.put(schedule("t2", "daily 08:00"))
		r.at(t0.Add(time.Hour + time.Second))
		Expect(r.runs("t1")).To(BeEmpty(), "a bad schedule stopped the others")
		Expect(r.runs("t2")).To(HaveLen(1), "a bad schedule stopped the others")
	})

	It("run drives passes on ticks and notify and stops cleanly", func() {
		r := newRig(func(o *triggers.Options) { o.Tick = time.Hour })
		r.put(schedule("t1", "daily 08:00"))
		before := runtime.NumGoroutine()
		cctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { r.runner.Run(cctx); close(done) }()
		time.Sleep(50 * time.Millisecond) // the first pass: nothing is due
		Expect(r.host.startCount()).To(Equal(0), "started early")
		r.clock.Set(t0.Add(time.Hour + time.Second))
		r.runner.Notify()
		waitFor("a run after Notify", func() bool { return r.host.startCount() == 1 })
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			Fail("Run did not return after its context ended")
		}
		r.clock.Set(t0.Add(48 * time.Hour))
		r.runner.Notify()
		time.Sleep(50 * time.Millisecond)
		Expect(r.host.startCount()).To(Equal(1), "a run started after Run returned")
		waitFor("no goroutine left", func() bool { return runtime.NumGoroutine() <= before })
	})

	It("run starts a pass on each tick", func() {
		r := newRig(func(o *triggers.Options) { o.Tick = 5 * time.Millisecond })
		r.put(schedule("t1", "daily 08:00"))
		cctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { r.runner.Run(cctx); close(done) }()
		defer func() { cancel(); <-done }()
		r.clock.Set(t0.Add(time.Hour + time.Second))
		waitFor("a run on a tick", func() bool { return r.host.startCount() == 1 })
	})

	It("notify never blocks", func() {
		r := newRig()
		for i := 0; i < 1000; i++ {
			r.runner.Notify()
		}
	})

	It("new runner needs a store and a start", func() {
		_, err := triggers.NewRunner(triggers.Options{})
		Expect(err).To(HaveOccurred(), "NewRunner accepted empty options")
	})

	It("a panic in start fails the run and frees the slot", func() {
		once := true
		r := newRig(func(o *triggers.Options) {
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
		Expect(got.Status).To(Equal(triggers.StatusFailed), "run = %+v", got)
		Expect(got.Reason).To(Equal("could not start: host bug"), "run = %+v", got)
		Expect(got.Ended.IsZero()).To(BeFalse(), "run = %+v", got)
		r.at(t0.Add(25*time.Hour + time.Second))
		got = r.latest("t1")
		Expect(got.Status).To(Equal(triggers.StatusRunning), "the next day: %+v", got)
	})

	It("run now after a restart first fails the runs of the dead process", func() {
		r := newRig()
		r.put(schedule("t1", "daily 08:00"))
		r.at(t0.Add(time.Hour + time.Second))
		old := r.latest("t1")
		r.runner = r.newRunner() // a restart; no pass yet
		got, err := r.runner.RunNow(ctx, "t1")
		Expect(err).NotTo(HaveOccurred(), "RunNow = %+v, %v", got, err)
		Expect(got.Status).To(Equal(triggers.StatusRunning), "RunNow = %+v, %v", got, err)
		o, _ := r.store.GetRun(ctx, old.ID)
		Expect(o.Status).To(Equal(triggers.StatusFailed), "old run = %+v", o)
		Expect(o.Reason).To(Equal("interrupted by a restart"), "old run = %+v", o)
		r.sync() // recovery must not touch the run that RunNow started
		now, _ := r.store.GetRun(ctx, got.ID)
		Expect(now.Status).To(Equal(triggers.StatusRunning), "the manual run was failed by recovery: %+v", now)
	})

	It("nothing starts after run has returned", func() {
		r := newRig(func(o *triggers.Options) { o.MaxRunning = 1 })
		r.put(schedule("t1", "daily 08:00"))
		r.put(schedule("t2", "daily 08:00"))
		r.put(schedule("t3", "daily 09:00"))
		r.at(t0.Add(time.Hour + time.Second))
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		r.runner.Run(cctx)
		r.finish(r.latest("t1"), triggers.StatusDone, "")
		_, err := r.runner.RunNow(ctx, "t3")
		Expect(err).To(MatchError(triggers.ErrStopped), "RunNow after shutdown: %v", err)
		err = r.runner.Sync(ctx)
		Expect(err).To(MatchError(triggers.ErrStopped), "Sync after shutdown: %v", err)
		got := r.host.startCount()
		Expect(got).To(Equal(1))
		Expect(r.latest("t2").Status).To(Equal(triggers.StatusQueued))
	})

	It("start gets a context that outlives the run now request", func() {
		var got context.Context
		r := newRig(func(o *triggers.Options) {
			inner := o.Start
			o.Start = func(c context.Context, tr triggers.Trigger, run triggers.Run) (string, error) {
				got = c
				return inner(c, tr, run)
			}
		})
		r.put(schedule("t1", "daily 08:00"))
		req, cancel := context.WithCancel(ctx)
		_, err := r.runner.RunNow(req, "t1")
		Expect(err).NotTo(HaveOccurred())
		cancel()
		Expect(got).NotTo(BeNil(), "the context of Start ended with the request")
		Expect(got.Err()).NotTo(HaveOccurred(), "the context of Start ended with the request")
	})

	It("the chat id is recorded after one failed write", func() {
		r := newRig(func(o *triggers.Options) { o.Store = &failOnceStore{Store: o.Store} })
		r.put(schedule("t1", "daily 08:00"))
		r.at(t0.Add(time.Hour + time.Second))
		got := r.latest("t1")
		Expect(got.Status).To(Equal(triggers.StatusRunning), "run = %+v", got)
		Expect(got.ChatID).To(Equal("chat-1"), "run = %+v", got)
	})

	It("finish cancel and run now race cleanly", func() {
		var rr *rig
		var mu sync.Mutex
		r := newRig(func(o *triggers.Options) {
			inner := o.Start
			o.Start = func(c context.Context, tr triggers.Trigger, run triggers.Run) (string, error) {
				id, err := inner(c, tr, run)
				go func() {
					mu.Lock()
					x := rr
					mu.Unlock()
					_ = x.runner.Finish(ctx, run.ID, triggers.StatusDone, "")
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
		waitFor("every run finished", func() bool {
			running, _ := r.store.RunningRuns(ctx)
			return len(running) == 0
		})
		r.sync()
		waitFor("every run finished", func() bool {
			running, _ := r.store.RunningRuns(ctx)
			queued, _ := r.store.QueuedRuns(ctx)
			return len(running) == 0 && len(queued) == 0
		})
	})

	It("run now right after the context ends is refused", func() {
		for i := 0; i < 200; i++ {
			var dead bool
			var sig *passSignalStore
			r := newRig(func(o *triggers.Options) {
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
			Expect(err).To(MatchError(triggers.ErrStopped), "iteration %d: err %v, dead ctx %v, %d starts", i, err, dead, r.host.startCount())
			Expect(dead).To(BeFalse(), "iteration %d: err %v, dead ctx %v, %d starts", i, err, dead, r.host.startCount())
			Expect(r.host.startCount()).To(Equal(0), "iteration %d: err %v, dead ctx %v, %d starts", i, err, dead, r.host.startCount())
		}
	})

	It("a failed start does not hold its slot for the pass", func() {
		r := newRig(func(o *triggers.Options) {
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
		Expect(r.latest("t1").Status).To(Equal(triggers.StatusFailed), "t1 %s, t2 %s", r.latest("t1").Status, r.latest("t2").Status)
		Expect(r.latest("t2").Status).To(Equal(triggers.StatusRunning), "t1 %s, t2 %s", r.latest("t1").Status, r.latest("t2").Status)
	})

	It("a start that ends with the runner leaves the run queued for the next runner", func() {
		entered := make(chan struct{})
		r := newRig(func(o *triggers.Options) {
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
		Expect(got.Status).To(Equal(triggers.StatusQueued), "a run that shutdown interrupted: %+v", got)
		Expect(got.Ended.IsZero()).To(BeTrue(), "a run that shutdown interrupted: %+v", got)
		Expect(got.Reason).To(Equal(""), "a run that shutdown interrupted: %+v", got)
		// The next Runner starts it from the queue, with no second run for the due time.
		r.opts.Start = r.host.start
		r.runner = r.newRunner()
		r.sync()
		runs := r.runs("t1")
		Expect(runs).To(HaveLen(1), "after the restart: %s, %d starts", statuses(runs), r.host.startCount())
		Expect(runs[0].ID).To(Equal(got.ID), "after the restart: %s, %d starts", statuses(runs), r.host.startCount())
		Expect(runs[0].Status).To(Equal(triggers.StatusRunning), "after the restart: %s, %d starts", statuses(runs), r.host.startCount())
		Expect(r.host.startCount()).To(Equal(1), "after the restart: %s, %d starts", statuses(runs), r.host.startCount())
		// It is still the one run of its trigger: a later due time is skipped.
		r.at(t0.Add(25*time.Hour + time.Second))
		got = r.latest("t1")
		Expect(got.Status).To(Equal(triggers.StatusSkipped), "the next day: %+v", got)
		Expect(got.Reason).To(Equal(triggers.ReasonStillGoing), "the next day: %+v", got)
	})

	It("a requeued run still waits for the running run of its trigger", func() {
		// A start error while stopping must not break "one run at a time": the
		// requeued run starts only after the running one of the trigger has ended.
		r := newRig()
		r.put(schedule("t1", "daily 08:00"))
		r.at(t0.Add(time.Hour + time.Second))
		first := r.latest("t1")
		Expect(first.Status).To(Equal(triggers.StatusRunning), "setup: %+v", first)
		queued := triggers.Run{ID: "q1", TriggerID: "t1", DueKey: "manual@q1", Due: t0, Status: triggers.StatusQueued}
		_, err := r.store.Claim(ctx, queued)
		Expect(err).NotTo(HaveOccurred())
		r.after(time.Second)
		Expect(statuses(r.runs("t1"))).To(Or(Equal("queued,running"), Equal("running,queued")))
		r.finish(first, triggers.StatusDone, "")
		r.after(time.Second)
		Expect(r.host.startCount()).To(Equal(2))
	})

	It("a panic in start while stopping still fails the run", func() {
		entered := make(chan struct{})
		r := newRig(func(o *triggers.Options) {
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
		got := r.latest("t1")
		Expect(got.Status).To(Equal(triggers.StatusFailed), "run = %+v", got)
		Expect(got.Reason).To(Equal("could not start: host bug"), "run = %+v", got)
	})

	It("a due run while a requeued run waits says so and the error is logged", func() {
		entered := make(chan struct{})
		var mu sync.Mutex
		var logs []string
		r := newRig(func(o *triggers.Options) {
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
		Expect(strings.Contains(joined, old.ID)).To(BeTrue(), "the start error was not logged with the run and trigger: %q", joined)
		Expect(strings.Contains(joined, "t1")).To(BeTrue(), "the start error was not logged with the run and trigger: %q", joined)
		Expect(strings.Contains(joined, "folder gone")).To(BeTrue(), "the start error was not logged with the run and trigger: %q", joined)
		// Restart a day later: the due time of day 2 passes before the old run starts.
		r.opts.Start = r.host.start
		r.runner = r.newRunner()
		r.at(t0.Add(26 * time.Hour))
		runs := r.runs("t1")
		Expect(runs).To(HaveLen(2), "the newer run: %s %+v", statuses(runs), runs[0])
		Expect(runs[0].Status).To(Equal(triggers.StatusSkipped), "the newer run: %s %+v", statuses(runs), runs[0])
		Expect(runs[0].Reason).To(Equal(triggers.ReasonWaiting), "the newer run: %s %+v", statuses(runs), runs[0])
		Expect(runs[1].ID).To(Equal(old.ID), "the requeued run: %+v", runs[1])
		Expect(runs[1].Status).To(Equal(triggers.StatusRunning), "the requeued run: %+v", runs[1])
	})

	It("finish gives up when its context ends instead of hanging on the store", func() {
		r := newRig(func(o *triggers.Options) { o.Store = hangingGetRun{o.Store} })
		cctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- r.runner.Finish(cctx, "run-1", triggers.StatusDone, "") }()
		select {
		case err := <-done:
			Expect(err).To(MatchError(context.DeadlineExceeded))
		case <-time.After(5 * time.Second):
			Fail("Finish hangs on a store that does not answer")
		}
	})

})
