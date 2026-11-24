package triggers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

func eventTrigger(id string) triggers.Trigger {
	return triggers.Trigger{ID: id, Enabled: true, Kind: triggers.KindEvent, Source: "example", EventType: "ping"}
}

func ev(key, payload string) triggers.Event {
	return triggers.Event{Source: "example", Type: "ping", DedupeKey: key, Payload: []byte(payload)}
}

var _ = Describe("Runner.Fire", func() {
	var rg *rig
	fire := func(id string, e triggers.Event) triggers.Run {
		run, err := rg.runner.Fire(ctx, id, e)
		Expect(err).NotTo(HaveOccurred())
		return run
	}
	BeforeEach(func() {
		rg = newRig(func(o *triggers.Options) { o.MaxRunning = 5 })
		rg.put(eventTrigger("e"))
	})

	It("starts a run for an event and takes one run for one dedupe key", func() {
		a := fire("e", ev("k1", `{"n":1}`))
		Expect(a.Status).To(Equal(triggers.StatusRunning))
		Expect(a.Payload).To(Equal(`{"n":1}`))
		b := fire("e", ev("k1", `{"n":1}`))
		Expect(b.ID).To(Equal(a.ID))
		Expect(rg.runs("e")).To(HaveLen(1))
		Expect(rg.host.startCount()).To(Equal(1))
	})

	It("answers the sentinels and claims nothing", func() {
		_, err := rg.runner.Fire(ctx, "nope", ev("k", `{}`))
		Expect(err).To(MatchError(triggers.ErrNotFound))
		_, err = rg.runner.Fire(ctx, "e", triggers.Event{Source: "other", Type: "ping", DedupeKey: "k"})
		Expect(err).To(MatchError(triggers.ErrNoMatch))
		_, err = rg.runner.Fire(ctx, "e", ev("", `{}`))
		Expect(err).To(HaveOccurred())
		rg.put(schedule("s", "daily 08:00"))
		_, err = rg.runner.Fire(ctx, "s", ev("k", `{}`))
		Expect(err).To(MatchError(triggers.ErrNotEnabled))
		off := eventTrigger("off")
		off.Enabled = false
		rg.put(off)
		_, err = rg.runner.Fire(ctx, "off", ev("k", `{}`))
		Expect(err).To(MatchError(triggers.ErrNotEnabled))
		Expect(rg.runs("e")).To(BeEmpty())
		Expect(rg.runs("off")).To(BeEmpty())
	})

	It("merges events into a queued run", func() {
		rg.finish(fire("e", ev("first", `{"n":0}`)), triggers.StatusDone, "")
		// The gap holds the next run, so it stays queued.
		q := fire("e", ev("k0", `{"n":0}`))
		Expect(q.Status).To(Equal(triggers.StatusQueued))
		for i := 1; i <= 10; i++ {
			got := fire("e", ev(fmt.Sprintf("k%d", i), fmt.Sprintf(`{"n":%d}`, i)))
			Expect(got.ID).To(Equal(q.ID))
		}
		fire("e", ev("k3", `{"n":3}`)) // a repeat adds nothing
		var items []json.RawMessage
		merged, err := rg.store.GetRun(ctx, q.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(json.Unmarshal([]byte(merged.Payload), &items)).To(Succeed())
		Expect(items).To(HaveLen(9))
		Expect(string(items[0])).To(Equal(`{"dropped":3}`))
		Expect(string(items[8])).To(Equal(`{"n":10}`))
		queued := 0
		for _, r := range rg.runs("e") {
			if r.Status == triggers.StatusQueued {
				queued++
			}
		}
		Expect(queued).To(Equal(1))
	})

	It("makes a queued run while one is running", func() {
		first := fire("e", ev("a", `{}`))
		Expect(first.Status).To(Equal(triggers.StatusRunning))
		q := fire("e", ev("b", `{"n":0}`))
		Expect(q.ID).NotTo(Equal(first.ID))
		Expect(q.Status).To(Equal(triggers.StatusQueued))
		for i := 1; i <= 10; i++ {
			Expect(fire("e", ev(fmt.Sprintf("m%d", i), `{}`)).ID).To(Equal(q.ID))
		}
		got, _ := rg.store.GetRun(ctx, first.ID)
		Expect(got.Payload).To(Equal(`{}`), "a running run takes no event")
	})

	It("keeps plain text and cuts long payloads to the limit", func() {
		rg.finish(fire("e", ev("first", `{}`)), triggers.StatusDone, "")
		fire("e", ev("t1", "plain <b>text</b>"))
		got := fire("e", ev("t2", `[1,2]`))
		Expect(got.Payload).To(Equal(`[{"dropped":0},"plain <b>text</b>",[1,2]]`))
		for i := range 8 {
			long := fmt.Sprintf(`{"v":"%s"}`, string(rune('a'+i))+fmt.Sprintf("%07999d", i))
			got = fire("e", ev(fmt.Sprintf("l%d", i), long))
		}
		Expect(len(got.Payload)).To(BeNumerically("<=", triggers.MaxPayloadBytes))
		Expect(json.Valid([]byte(got.Payload))).To(BeTrue())
	})

	It("holds a run until the gap since the previous start has passed", func() {
		a := fire("e", ev("a", `{}`))
		rg.finish(a, triggers.StatusDone, "")
		b := fire("e", ev("b", `{}`))
		Expect(b.Status).To(Equal(triggers.StatusQueued))
		rg.after(triggers.DefaultFileGap - time.Second)
		Expect(rg.latest("e").Status).To(Equal(triggers.StatusQueued))
		rg.after(time.Second)
		Expect(rg.latest("e").Status).To(Equal(triggers.StatusRunning))
	})

	It("uses the MinGap of the trigger", func() {
		tr := eventTrigger("g")
		tr.MinGap = 10 * time.Minute
		rg.put(tr)
		a := fire("g", ev("a", `{}`))
		rg.finish(a, triggers.StatusDone, "")
		fire("g", ev("b", `{}`))
		rg.after(9 * time.Minute)
		Expect(rg.latest("g").Status).To(Equal(triggers.StatusQueued))
		rg.after(time.Minute)
		Expect(rg.latest("g").Status).To(Equal(triggers.StatusRunning))
	})

	It("skips a run past the daily limit", func() {
		tr := eventTrigger("c")
		tr.DailyCap = 2
		tr.MinGap = time.Second
		rg.put(tr)
		for i := range 2 {
			r := fire("c", ev(fmt.Sprintf("k%d", i), `{}`))
			rg.after(2 * time.Second) // past the gap: the queued run starts
			r, _ = rg.store.GetRun(ctx, r.ID)
			Expect(r.Status).To(Equal(triggers.StatusRunning))
			Expect(r.Payload).NotTo(BeEmpty(), "a run that was admitted keeps its payload")
			rg.finish(r, triggers.StatusDone, "")
		}
		r := fire("c", ev("k3", `{"big":"payload"}`))
		Expect(r.Status).To(Equal(triggers.StatusSkipped))
		Expect(r.Reason).To(ContainSubstring("limit"))
		Expect(r.Payload).To(BeEmpty(), "a run skipped by the cap keeps no payload")
		stored, err := rg.store.GetRun(ctx, r.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.Payload).To(BeEmpty())
	})

	It("skips a queued run whose trigger was switched off", func() {
		rg.finish(fire("e", ev("a", `{}`)), triggers.StatusDone, "")
		q := fire("e", ev("b", `{}`))
		Expect(q.Status).To(Equal(triggers.StatusQueued))
		off := eventTrigger("e")
		off.Enabled = false
		rg.put(off)
		rg.after(time.Hour)
		got, _ := rg.store.GetRun(ctx, q.ID)
		Expect(got.Status).To(Equal(triggers.StatusSkipped))
		Expect(got.Reason).To(Equal(triggers.ReasonSwitchedOff))
	})

	It("leaves one queued run after 200 events in one tick", func() {
		rg.finish(fire("e", ev("first", `{}`)), triggers.StatusDone, "")
		for i := range 200 {
			fire("e", ev(fmt.Sprintf("b%d", i), fmt.Sprintf(`{"i":%d}`, i)))
		}
		queued := 0
		for _, r := range rg.runs("e") {
			if r.Status == triggers.StatusQueued {
				queued++
			}
		}
		Expect(queued).To(Equal(1))
	})

	It("keeps the gap after a burst of more than 50 merged events", func() {
		rg.finish(fire("e", ev("first", `{}`)), triggers.StatusDone, "")
		for i := range 70 {
			fire("e", ev(fmt.Sprintf("b%d", i), `{}`))
		}
		rg.after(triggers.DefaultFileGap - time.Second)
		Expect(rg.host.startCount()).To(Equal(1))
		rg.after(time.Second)
		Expect(rg.host.startCount()).To(Equal(2))
	})

	It("puts an event in a new run when the queued run started before the append", func() {
		rg.finish(fire("e", ev("first", `{}`)), triggers.StatusDone, "")
		q := fire("e", ev("q", `{"n":0}`))
		Expect(q.Status).To(Equal(triggers.StatusQueued))
		racy := &startAfterSnapshot{Store: rg.store, runID: q.ID}
		rg.opts.Store = racy
		rg.runner = rg.newRunner()
		got := fire("e", ev("late", `{"n":1}`))
		Expect(got.ID).NotTo(Equal(q.ID))
		Expect(got.Payload).To(Equal(`{"n":1}`))
		Expect(got.DueKey).To(Equal("event@late"))
		again := fire("e", ev("late", `{"n":1}`))
		Expect(again.ID).To(Equal(got.ID), "the key is still unique")
		old, _ := rg.store.GetRun(ctx, q.ID)
		Expect(old.Payload).To(Equal(`{"n":0}`))
	})
})

// startAfterSnapshot starts the run right after QueuedRuns returned it, as
// another replica could.
type startAfterSnapshot struct {
	triggers.Store
	runID string
	done  bool
}

func (s *startAfterSnapshot) QueuedRuns(ctx context.Context) ([]triggers.Run, error) {
	runs, err := s.Store.QueuedRuns(ctx)
	if err == nil && !s.done {
		for _, r := range runs {
			if r.ID == s.runID {
				s.done = true
				r.Status = triggers.StatusRunning
				err = s.Store.SetRunStatus(ctx, r)
			}
		}
	}
	return runs, err
}

var _ = Describe("Runner.FireQueued", func() {
	var rg *rig
	queue := func(id string, e triggers.Event) triggers.Run {
		run, err := rg.runner.FireQueued(ctx, id, e)
		Expect(err).NotTo(HaveOccurred())
		return run
	}
	BeforeEach(func() {
		rg = newRig(func(o *triggers.Options) { o.MaxRunning = 5 })
		rg.put(eventTrigger("e"))
	})

	It("claims a queued run and starts nothing; a pass of the leader starts it", func() {
		a := queue("e", ev("k1", `{"n":1}`))
		Expect(a.Status).To(Equal(triggers.StatusQueued))
		Expect(rg.host.startCount()).To(Equal(0))
		Expect(queue("e", ev("k1", `{"n":1}`)).ID).To(Equal(a.ID), "one run for one dedupe key")
		rg.sync()
		Expect(rg.host.startCount()).To(Equal(1))
		got, _ := rg.store.GetRun(ctx, a.ID)
		Expect(got.Status).To(Equal(triggers.StatusRunning))
	})

	It("merges a burst into one queued run with a marker for each event", func() {
		q := queue("e", ev("k0", `{"n":0}`))
		for i := 1; i <= 10; i++ {
			Expect(queue("e", ev(fmt.Sprintf("k%d", i), fmt.Sprintf(`{"n":%d}`, i))).ID).To(Equal(q.ID))
		}
		merged, _ := rg.store.GetRun(ctx, q.ID)
		var items []json.RawMessage
		Expect(json.Unmarshal([]byte(merged.Payload), &items)).To(Succeed())
		Expect(string(items[0])).To(Equal(`{"dropped":3}`))
		Expect(string(items[len(items)-1])).To(Equal(`{"n":10}`))
		markers := 0
		for _, r := range rg.runs("e") {
			if r.Reason == triggers.ReasonMerged {
				markers++
			}
		}
		Expect(markers).To(Equal(10))
		Expect(rg.host.startCount()).To(Equal(0))
	})

	It("applies the daily limit and the sentinels", func() {
		tr := eventTrigger("c")
		tr.DailyCap = 1
		tr.MinGap = time.Second
		rg.put(tr)
		r, err := rg.runner.Fire(ctx, "c", ev("k0", `{}`))
		Expect(err).NotTo(HaveOccurred())
		rg.finish(r, triggers.StatusDone, "")
		over := queue("c", ev("k1", `{}`))
		Expect(over.Status).To(Equal(triggers.StatusSkipped))
		Expect(over.Reason).To(ContainSubstring("limit"))
		_, err = rg.runner.FireQueued(ctx, "nope", ev("k", `{}`))
		Expect(err).To(MatchError(triggers.ErrNotFound))
		_, err = rg.runner.FireQueued(ctx, "e", triggers.Event{Source: "other", Type: "ping", DedupeKey: "k"})
		Expect(err).To(MatchError(triggers.ErrNoMatch))
		rg.put(schedule("s", "daily 08:00"))
		_, err = rg.runner.FireQueued(ctx, "s", ev("k", `{}`))
		Expect(err).To(MatchError(triggers.ErrNotEnabled))
	})

	It("does not mark the runs that are going as interrupted", func() {
		a, err := rg.runner.Fire(ctx, "e", ev("first", `{}`))
		Expect(err).NotTo(HaveOccurred())
		other := rg.newRunner() // a replica that did not start the run
		_, err = other.FireQueued(ctx, "e", ev("second", `{}`))
		Expect(err).NotTo(HaveOccurred())
		got, _ := rg.store.GetRun(ctx, a.ID)
		Expect(got.Status).To(Equal(triggers.StatusRunning))
	})
})
