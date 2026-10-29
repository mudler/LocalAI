package triggers_test

import (
	"testing"
	"time"

	"github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

// daily runs a "daily 08:00" trigger for days days, ending every run after a
// minute unless leave says to keep it running.
func daily(r *rig, days int, leave func(day int) bool) {
	r.t.Helper()
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

func TestFinishedRunsBeyondTheLimitAreRemovedOutsideTheLastTwoDays(t *testing.T) {
	g := gomega.NewWithT(t)
	r := newRig(t, func(o *triggers.Options) { o.Retention = triggers.RunRetention{Keep: 3, MaxAge: -1} })
	daily(r, 10, nil)
	runs := r.runs("t1")
	// The newest three stay, and so does every run of the last 48 hours; with
	// one run a day both are the same three runs.
	g.Expect(runs).To(gomega.HaveLen(3), statuses(runs))
	g.Expect(runs[0].DueKey).To(gomega.Equal("2026-10-31T08:00:00Z"))
}

func TestFinishedRunsOlderThanTheMaxAgeAreRemoved(t *testing.T) {
	g := gomega.NewWithT(t)
	r := newRig(t, func(o *triggers.Options) { o.Retention = triggers.RunRetention{Keep: -1, MaxAge: 4 * 24 * time.Hour} })
	daily(r, 10, nil)
	runs := r.runs("t1")
	// Last run is due on day 9 at 08:00; a run due before 08:01 four days earlier goes.
	g.Expect(runs).To(gomega.HaveLen(4), statuses(runs))
	g.Expect(runs[len(runs)-1].DueKey).To(gomega.Equal("2026-10-28T08:00:00Z"))
}

func TestTheDefaultRetentionKeepsAMonthOfDailyRuns(t *testing.T) {
	g := gomega.NewWithT(t)
	r := newRig(t)
	daily(r, 10, nil)
	g.Expect(r.runs("t1")).To(gomega.HaveLen(10))
}

func TestNoRetentionKeepsEveryRun(t *testing.T) {
	g := gomega.NewWithT(t)
	r := newRig(t, func(o *triggers.Options) { o.Retention = triggers.RunRetention{Keep: -1, MaxAge: -1} })
	daily(r, 10, nil)
	g.Expect(r.runs("t1")).To(gomega.HaveLen(10))
}

func TestARunThatIsStillGoingIsNotRemovedAndSkippedRunsAreCounted(t *testing.T) {
	g := gomega.NewWithT(t)
	r := newRig(t, func(o *triggers.Options) { o.Retention = triggers.RunRetention{Keep: 2, MaxAge: -1} })
	// The run of day 0 never ends, so the later days are skipped at once.
	daily(r, 8, func(day int) bool { return day == 0 })
	runs := r.runs("t1")
	g.Expect(runs).To(gomega.HaveLen(3), statuses(runs))
	g.Expect(runs[len(runs)-1].Status).To(gomega.Equal(triggers.StatusRunning), "the running run stays")
	g.Expect(runs[0].Status).To(gomega.Equal(triggers.StatusSkipped))
}
