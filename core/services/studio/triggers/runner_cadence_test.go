package triggers_test

import (
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

var _ = Describe("Runner at a fast cadence", func() {
	// paused records the triggers that the Runner asked the host to switch off.
	type pauses struct {
		mu  sync.Mutex
		ids []string
	}
	record := func(p *pauses) func(*triggers.Options) {
		return func(o *triggers.Options) {
			o.Pause = func(t triggers.Trigger, _ string) {
				p.mu.Lock()
				p.ids = append(p.ids, t.ID)
				p.mu.Unlock()
			}
		}
	}

	It("asks to pause a sub-hourly trigger after 3 runs in a row skipped by the daily cap", func() {
		p := &pauses{}
		r := newRig(record(p))
		tr := schedule("t1", "*/10 * * * *")
		tr.DailyCap = 1
		tr.Since = t0
		r.put(tr)
		r.after(10 * time.Minute)
		r.finish(r.latest("t1"), triggers.StatusDone, "")
		for range 2 {
			r.after(10 * time.Minute)
		}
		Expect(p.ids).To(BeEmpty(), "two skips are not enough")
		r.after(10 * time.Minute)
		Expect(p.ids).To(Equal([]string{"t1"}), "statuses %s", statuses(r.runs("t1")))
	})

	It("does not pause a trigger that runs hourly or slower for hitting its cap", func() {
		p := &pauses{}
		r := newRig(record(p))
		tr := schedule("t1", "0 * * * *")
		tr.DailyCap = 1
		tr.Since = t0
		r.put(tr)
		r.after(time.Hour)
		r.finish(r.latest("t1"), triggers.StatusDone, "")
		for range 5 {
			r.after(time.Hour)
		}
		Expect(statuses(r.runs("t1"))).To(ContainSubstring("skipped,skipped,skipped"))
		Expect(p.ids).To(BeEmpty())
	})

	It("records one skipped row for a stretch of skips behind a run that is still going", func() {
		r := newRig()
		r.put(schedule("t1", "*/5 * * * *"))
		r.after(5 * time.Minute)
		first := r.latest("t1")
		for range 4 {
			r.after(5 * time.Minute)
		}
		Expect(statuses(r.runs("t1"))).To(Equal("skipped,running"))
		r.finish(first, triggers.StatusDone, "")
		r.after(5 * time.Minute)
		Expect(statuses(r.runs("t1"))).To(Equal("running,skipped,done"), "the next due time runs again")
	})

	It("holds a schedule that sets no cap to a limit that grows with its cadence", func() {
		r := newRig()
		tr := schedule("t1", "*/5 * * * *")
		tr.Since = t0
		r.put(tr)
		// 30 runs in a row are more than the old default of 24 a day.
		for range 30 {
			r.after(5 * time.Minute)
			r.finish(r.latest("t1"), triggers.StatusDone, "")
		}
		Expect(statuses(r.runs("t1"))).NotTo(ContainSubstring("skipped"))
	})
})
