package updatenotify_test

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/notify"
	. "github.com/mudler/LocalAI/core/services/updatenotify"
	"github.com/mudler/LocalAI/core/services/upgraderun"
)

// fakeClock is a clock whose timers fire only when the test says so.
type fakeClock struct {
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	f       func()
	stopped bool
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) AfterFunc(_ time.Duration, f func()) func() {
	t := &fakeTimer{f: f}
	c.timers = append(c.timers, t)
	return func() { t.stopped = true }
}

// fire runs the timers that are still armed.
func (c *fakeClock) fire() {
	for _, t := range c.timers {
		if !t.stopped {
			t.stopped = true
			t.f()
		}
	}
}

func runOf(id string, source upgraderun.Source, state upgraderun.RunState, items ...upgraderun.Item) upgraderun.Run {
	return upgraderun.Run{ID: id, Source: source, State: state, Items: items}
}

func skippedWith(name, key string) upgraderun.Item {
	it := runItem(name, upgraderun.PhaseSkipped, 0)
	it.ErrorKey = key
	return it
}

func runItem(name string, phase upgraderun.Phase, progress int) upgraderun.Item {
	return upgraderun.Item{Key: upgraderun.Key(upgraderun.KindGallery, name), Kind: upgraderun.KindGallery, Name: name, Phase: phase, Progress: progress}
}

var _ = Describe("the notifications of an upgrade run", func() {
	var (
		f   *frontend
		clk *fakeClock
		rn  *RunNotifier
	)
	rows := func(who string) (live, end []notify.Notification) {
		for _, n := range list(f, who) {
			switch n.Code {
			case CodeRun:
				live = append(live, n)
			case CodeRunEnd:
				end = append(end, n)
			}
		}
		return live, end
	}
	build := func(admins ...string) {
		f = newFrontend(memStore(), nil, admins...)
		clk = &fakeClock{now: time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)}
		rn = &RunNotifier{Emitter: f.em, Now: clk.Now, AfterFunc: clk.AfterFunc}
	}
	local := func(r upgraderun.Run) { rn.RunChanged(upgraderun.Change{Run: r, Local: true}) }

	It("keeps one live row for the run and edits it in place", func() {
		build("admin")
		r := runOf("r1", upgraderun.SourceManual, upgraderun.RunRunning,
			runItem("a", upgraderun.PhaseQueued, 0), runItem("b", upgraderun.PhaseQueued, 0))
		local(r)
		live, _ := rows("admin")
		Expect(live).To(HaveLen(1))
		Expect(live[0].Link).To(Equal("/app/updates?run=r1"))
		Expect(live[0].Params).To(Equal(map[string]any{
			"run": "r1", "progress": 0, "done": 0, "total": 2, "source": "manual", "updated": 0, "running": 0, "queued": 2,
		}))
		Expect(f.pushed).To(BeEmpty(), "the start of a run is not pushed: only its end is")

		clk.now = clk.now.Add(2 * time.Second)
		r.Items[0].Phase, r.Items[0].Progress = upgraderun.PhaseRunning, 50
		local(r)
		live, _ = rows("admin")
		Expect(live).To(HaveLen(1), "the same row")
		Expect(live[0].Params).To(HaveKeyWithValue("running", BeNumerically("==", 1)))
		Expect(live[0].Params).To(HaveKeyWithValue("progress", BeNumerically("==", 25)))
		Expect(f.pushed).To(BeEmpty(), "an edit in place is quiet")
	})

	It("writes at most once for each interval and the last change when it ends", func() {
		build("admin")
		r := runOf("r1", upgraderun.SourceManual, upgraderun.RunRunning, runItem("a", upgraderun.PhaseRunning, 0))
		local(r)
		for p := 10; p <= 50; p += 10 {
			clk.now = clk.now.Add(200 * time.Millisecond)
			r.Items[0].Progress = p
			local(r)
		}
		live, _ := rows("admin")
		Expect(live[0].Params).To(HaveKeyWithValue("progress", BeNumerically("==", 0)), "ticks inside the interval are held back")
		clk.now = clk.now.Add(time.Second)
		clk.fire()
		live, _ = rows("admin")
		Expect(live[0].Params).To(HaveKeyWithValue("progress", BeNumerically("==", 50)), "the last held tick is written")
		clk.fire()
		clk.now = clk.now.Add(time.Minute)
		r.Items[0].Progress = 60
		local(r)
		live, _ = rows("admin")
		Expect(live[0].Params).To(HaveKeyWithValue("progress", BeNumerically("==", 60)))
	})

	It("replaces the live row with the end row, so the run is one item, and posts the end once", func() {
		build("admin")
		r := runOf("r1", upgraderun.SourceAuto, upgraderun.RunRunning,
			runItem("a", upgraderun.PhaseRunning, 30), runItem("b", upgraderun.PhaseQueued, 0))
		local(r)
		r.State = upgraderun.RunFinished
		r.Items[0].Phase, r.Items[1].Phase = upgraderun.PhaseUpdated, upgraderun.PhaseUpdated
		local(r)
		local(r)
		live, end := rows("admin")
		Expect(live).To(BeEmpty(), "the live row is removed: one item for each run")
		Expect(end).To(HaveLen(1), "a replayed end is silent")
		Expect(end[0].Params).To(Equal(map[string]any{"run": "r1", "total": 2, "updated": 2, "failed": 0, "source": "auto"}))
		Expect(end[0].Read).To(BeFalse())
		Expect(end[0].Kind).To(Equal(notify.KindRunEnded))
		Expect(end[0].Status).To(Equal(notify.StatusOK))
		Expect(end[0].Link).To(Equal("/app/updates?run=r1"))
		Expect(f.pushed).To(HaveLen(1), "only the end")
		clk.fire()
		live, _ = rows("admin")
		Expect(live).To(BeEmpty(), "no timer brings the live row back after the end")
		Expect(list(f, "admin")).To(HaveLen(1))
	})

	It("counts every item that needs attention and names the first one", func() {
		build("admin")
		bad := runItem("llama-cpp", upgraderun.PhaseFailed, 0)
		bad.From = "1.0"
		bad.Error = "download failed for https://user:hunter2@example.org/x with token=abc123 at /var/lib/app/backends/x.tar"
		r := runOf("r2", upgraderun.SourceManual, upgraderun.RunFinished,
			runItem("a", upgraderun.PhaseUpdated, 100), bad,
			runItem("c", upgraderun.PhaseRolledBack, 0), runItem("d", upgraderun.PhaseUnconfirmed, 0),
			skippedWith("e", upgraderun.ErrKeyHeld), runItem("f", upgraderun.PhaseCancelled, 0),
			runItem("g", upgraderun.PhaseSkipped, 0))
		local(r)
		_, end := rows("admin")
		Expect(end).To(HaveLen(1))
		// The same items the Updates page counts: failed, rolled back,
		// unconfirmed and skipped for a reason; not cancelled, not a skip
		// with no reason.
		Expect(end[0].Params).To(HaveKeyWithValue("failed", BeNumerically("==", 4)))
		Expect(end[0].Params).To(HaveKeyWithValue("updated", BeNumerically("==", 1)))
		Expect(end[0].Params).To(HaveKeyWithValue("first_name", "llama-cpp"))
		Expect(end[0].Params).To(HaveKeyWithValue("first_from", "1.0"))
		Expect(end[0].Kind).To(Equal(notify.KindNeedsAttention))
		Expect(end[0].Status).To(Equal(notify.StatusFailed))
		text := end[0].Title + end[0].Body + end[0].Params["first_error"].(string)
		for _, secret := range []string{"hunter2", "abc123", "/var/lib/app/backends"} {
			Expect(text).NotTo(ContainSubstring(secret))
		}
		Expect(end[0].Params["first_error"]).To(ContainSubstring("x.tar"))
	})

	It("says that a run was cancelled, with the items that did not run", func() {
		build("admin")
		r := runOf("rc", upgraderun.SourceManual, upgraderun.RunCancelled,
			runItem("a", upgraderun.PhaseUpdated, 100), runItem("b", upgraderun.PhaseCancelled, 0), runItem("c", upgraderun.PhaseCancelled, 0))
		local(r)
		_, end := rows("admin")
		Expect(end).To(HaveLen(1))
		Expect(end[0].Params).To(Equal(map[string]any{
			"run": "rc", "total": 3, "updated": 1, "failed": 0, "source": "manual", "state": "cancelled", "cancelled": 2,
		}))
		Expect(end[0].Title).To(Equal("Update run cancelled: 1 of 3 updated"))
	})

	It("gives the error key of the first item that needs attention", func() {
		build("admin")
		bad := runItem("e2e-bad", upgraderun.PhaseRolledBack, 0)
		bad.From, bad.ErrorKey, bad.Error = "1.0.0", upgraderun.ErrKeyStart, "the backend process exited (exit code 3)"
		local(runOf("rk", upgraderun.SourceManual, upgraderun.RunFinished, bad))
		_, end := rows("admin")
		Expect(end[0].Params).To(HaveKeyWithValue("first_key", "start"))
	})

	It("puts the name of the first item in the body on one line, without escapes", func() {
		build("admin")
		odd := upgraderun.Item{Key: "addon:odd", Kind: upgraderun.KindAddon, Name: "odd", Title: "Odd\x1b[31m\nname\u202e",
			From: "1", Phase: upgraderun.PhaseFailed, ErrorKey: "unknown"}
		local(runOf("r10", upgraderun.SourceManual, upgraderun.RunFinished, odd))
		_, end := rows("admin")
		Expect(end).To(HaveLen(1))
		Expect(end[0].Body).To(HavePrefix("Odd name"))
		Expect(end[0].Body).NotTo(ContainSubstring("\x1b"))
		Expect(end[0].Body).NotTo(ContainSubstring("\n"))
		Expect(end[0].Body).NotTo(ContainSubstring("\u202e"))
	})

	It("names an add-on by its title and its version, never its id and digest", func() {
		build("admin")
		notes := upgraderun.Item{Key: "addon:notes", Kind: upgraderun.KindAddon, Name: "notes", Title: "Notes sync",
			From: "0.8.1", To: "0.8.2", ExpectedTo: "sha256:bb", Phase: upgraderun.PhaseRolledBack, ErrorKey: "start"}
		local(runOf("r9", upgraderun.SourceManual, upgraderun.RunFinished, notes))
		_, end := rows("admin")
		Expect(end).To(HaveLen(1))
		Expect(end[0].Params).To(HaveKeyWithValue("first_name", "Notes sync"))
		Expect(end[0].Params).To(HaveKeyWithValue("first_from", "0.8.1"))
		Expect(end[0].Body).To(HavePrefix("Notes sync"))
		Expect(end[0].Body + end[0].Title).NotTo(ContainSubstring("sha256"))
	})

	It("bounds the error text", func() {
		build("admin")
		bad := runItem("a", upgraderun.PhaseFailed, 0)
		bad.Error = strings.Repeat("boom ", 200)
		local(runOf("r3", upgraderun.SourceManual, upgraderun.RunFinished, bad))
		_, end := rows("admin")
		Expect(len([]rune(end[0].Params["first_error"].(string)))).To(BeNumerically("<=", 161))
	})

	It("writes only on the frontend that runs the run", func() {
		build("admin")
		r := runOf("r1", upgraderun.SourceManual, upgraderun.RunRunning, runItem("a", upgraderun.PhaseRunning, 10))
		rn.RunChanged(upgraderun.Change{Run: r, Local: false})
		r.State = upgraderun.RunFinished
		rn.RunChanged(upgraderun.Change{Run: r, Local: false})
		Expect(list(f, "admin")).To(BeEmpty())
	})

	It("tells each administrator and nobody else", func() {
		build("admin1", "admin2")
		r := runOf("r1", upgraderun.SourceManual, upgraderun.RunRunning, runItem("a", upgraderun.PhaseRunning, 10))
		local(r)
		for _, who := range []string{"admin1", "admin2"} {
			live, end := rows(who)
			Expect(live).To(HaveLen(1))
			Expect(end).To(BeEmpty())
		}
		r.State, r.Items[0].Phase = upgraderun.RunFinished, upgraderun.PhaseUpdated
		local(r)
		for _, who := range []string{"admin1", "admin2"} {
			live, end := rows(who)
			Expect(live).To(BeEmpty())
			Expect(end).To(HaveLen(1))
		}
		Expect(list(f, "guest")).To(BeEmpty())
	})

	It("keeps the rows of two runs apart", func() {
		build("admin")
		local(runOf("r1", upgraderun.SourceManual, upgraderun.RunRunning, runItem("a", upgraderun.PhaseRunning, 10)))
		local(runOf("r2", upgraderun.SourceAuto, upgraderun.RunRunning, runItem("b", upgraderun.PhaseRunning, 10)))
		live, end := rows("admin")
		Expect(live).To(HaveLen(2))
		Expect(end).To(BeEmpty())
		// The end of one run removes its own live row only.
		local(runOf("r1", upgraderun.SourceManual, upgraderun.RunFinished, runItem("a", upgraderun.PhaseUpdated, 100)))
		live, end = rows("admin")
		Expect(live).To(HaveLen(1))
		Expect(live[0].Params).To(HaveKeyWithValue("run", "r2"))
		Expect(end).To(HaveLen(1))
		Expect(end[0].Params).To(HaveKeyWithValue("run", "r1"))
	})

	It("does nothing without a notification center", func() {
		rn = &RunNotifier{Emitter: &Emitter{}}
		Expect(func() {
			rn.RunChanged(upgraderun.Change{Run: runOf("r1", upgraderun.SourceManual, upgraderun.RunFinished), Local: true})
		}).NotTo(Panic())
	})
})
