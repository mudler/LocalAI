package triggers_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

// daily runs a "daily 08:00" trigger for days days, ending every run after a
// minute unless leave says to keep it running.
func daily(r *rig, days int, leave func(day int) bool) {
	r.put(schedule("t1", "daily 08:00"))
	for d := range days {
		r.at(t0.Add(time.Hour + time.Duration(d)*24*time.Hour + time.Minute))
		if leave != nil && leave(d) {
			continue
		}
		if l := r.latest("t1"); l.Status == triggers.StatusRunning {
			r.finish(l, triggers.StatusDone, "")
		}
	}
}

var _ = Describe("Run retention", func() {
	It("removes finished runs beyond the limit outside the last two days", func() {
		r := newRig(func(o *triggers.Options) { o.Retention = triggers.RunRetention{Keep: 3, MaxAge: -1} })
		daily(r, 10, nil)
		runs := r.runs("t1")
		// The newest three stay, and so does every run of the last 48 hours; with
		// one run a day both are the same three runs.
		Expect(runs).To(HaveLen(3), statuses(runs))
		Expect(runs[0].DueKey).To(Equal("2026-10-31T08:00:00Z"))
	})

	It("removes finished runs older than the max age", func() {
		r := newRig(func(o *triggers.Options) { o.Retention = triggers.RunRetention{Keep: -1, MaxAge: 4 * 24 * time.Hour} })
		daily(r, 10, nil)
		runs := r.runs("t1")
		// Last run is due on day 9 at 08:00; a run due before 08:01 four days earlier goes.
		Expect(runs).To(HaveLen(4), statuses(runs))
		Expect(runs[len(runs)-1].DueKey).To(Equal("2026-10-28T08:00:00Z"))
	})

	It("keeps a month of daily runs by default", func() {
		r := newRig()
		daily(r, 10, nil)
		Expect(r.runs("t1")).To(HaveLen(10))
	})

	It("keeps every run when retention is off", func() {
		r := newRig(func(o *triggers.Options) { o.Retention = triggers.RunRetention{Keep: -1, MaxAge: -1} })
		daily(r, 10, nil)
		Expect(r.runs("t1")).To(HaveLen(10))
	})

	It("does not remove a run that is still going and counts skipped runs", func() {
		r := newRig(func(o *triggers.Options) { o.Retention = triggers.RunRetention{Keep: 2, MaxAge: -1} })
		// The run of day 0 never ends, so the later days are skipped at once.
		daily(r, 8, func(day int) bool { return day == 0 })
		runs := r.runs("t1")
		Expect(runs).To(HaveLen(3), statuses(runs))
		Expect(runs[len(runs)-1].Status).To(Equal(triggers.StatusRunning), "the running run stays")
		Expect(runs[0].Status).To(Equal(triggers.StatusSkipped))
	})
})
