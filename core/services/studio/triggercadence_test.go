package studio_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func cadenceInput(schedule string) studio.TriggerInput {
	return studio.TriggerInput{Name: "Watch", Prompt: "Check it.", Kind: triggers.KindSchedule, Schedule: schedule, TZ: "UTC", Model: "m", Enabled: true}
}

var _ = Describe("the schedule floor of every trigger", func() {
	ctx := context.Background()

	It("refuses a schedule under 5 minutes on create and update, and accepts 5", func() {
		r := newTrigRig(saySummary)
		for _, bad := range []string{"* * * * *", "*/2 * * * *", "*/4 * * * *"} {
			_, err := r.host.CreateTrigger(ctx, alice, cadenceInput(bad))
			Expect(err).To(MatchError(studio.ErrBadTrigger), bad)
			Expect(err.Error()).To(ContainSubstring("at most once every 5 minutes"), bad)
		}
		tr, err := r.host.CreateTrigger(ctx, alice, cadenceInput("*/5 * * * *"))
		Expect(err).NotTo(HaveOccurred())

		_, err = r.host.UpdateTrigger(ctx, alice, tr.ID, cadenceInput("*/3 * * * *"))
		Expect(err).To(MatchError(ContainSubstring("at most once every 5 minutes")))
		got, err := r.host.UpdateTrigger(ctx, alice, tr.ID, cadenceInput("*/10 * * * *"))
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Schedule).To(Equal("*/10 * * * *"))
	})

	It("lets a trigger saved before the floor be edited, and checks it only when its schedule changes", func() {
		r := newTrigRig(saySummary)
		old := triggers.Trigger{ID: "old", Owner: alice, Name: "Fast", Prompt: "Check it.", Kind: triggers.KindSchedule,
			Schedule: "*/2 * * * *", TZ: "UTC", Model: "m", Enabled: true, Since: r.sh.clock.Now()}
		Expect(r.sh.trg.PutTrigger(ctx, old)).To(Succeed())

		in := cadenceInput("*/2 * * * *")
		in.Name = "Renamed"
		got, err := r.host.UpdateTrigger(ctx, alice, "old", in)
		Expect(err).NotTo(HaveOccurred(), "a rename must not need a new schedule")
		Expect(got.Name).To(Equal("Renamed"))
		Expect(got.Schedule).To(Equal("*/2 * * * *"))

		_, err = r.host.UpdateTrigger(ctx, alice, "old", cadenceInput("*/3 * * * *"))
		Expect(err).To(MatchError(ContainSubstring("at most once every 5 minutes")), "a new schedule is checked")
		got, err = r.host.UpdateTrigger(ctx, alice, "old", cadenceInput("*/5 * * * *"))
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Schedule).To(Equal("*/5 * * * *"))
	})
})

var _ = Describe("the daily limit by cadence", func() {
	ctx := context.Background()

	It("grows with the cadence when none is set, and a limit that is set stays", func() {
		r := newTrigRig(saySummary)
		fast, err := r.host.CreateTrigger(ctx, alice, cadenceInput("*/5 * * * *"))
		Expect(err).NotTo(HaveOccurred())
		Expect(r.host.DailyCapOf(fast)).To(Equal(288))
		Expect(r.host.MaxRunsPerDay(fast)).To(Equal(288))
		half, err := r.host.CreateTrigger(ctx, alice, cadenceInput("*/30 * * * *"))
		Expect(err).NotTo(HaveOccurred())
		Expect(r.host.DailyCapOf(half)).To(Equal(48))
		hourly, err := r.host.CreateTrigger(ctx, alice, cadenceInput("0 * * * *"))
		Expect(err).NotTo(HaveOccurred())
		Expect(r.host.DailyCapOf(hourly)).To(Equal(24))

		in := cadenceInput("*/5 * * * *")
		in.DailyCap = 10
		set, err := r.host.CreateTrigger(ctx, alice, in)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.host.DailyCapOf(set)).To(Equal(10))
		Expect(r.host.MaxRunsPerDay(set)).To(Equal(10))
	})

	It("does not turn the default into a chosen limit when a form sends it back", func() {
		r := newTrigRig(saySummary)
		fast, err := r.host.CreateTrigger(ctx, alice, cadenceInput("*/5 * * * *"))
		Expect(err).NotTo(HaveOccurred())
		in := cadenceInput("*/5 * * * *")
		in.DailyCap = r.host.DailyCapOf(fast)
		got, err := r.host.UpdateTrigger(ctx, alice, fast.ID, in)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.DailyCap).To(Equal(0), "the default stays a default")
		in.Schedule = "0 * * * *"
		got, err = r.host.UpdateTrigger(ctx, alice, fast.ID, in)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.host.DailyCapOf(got)).To(Equal(24), "a slower schedule has the smaller default")
	})
})

var _ = Describe("a schedule trigger that keeps failing", func() {
	ctx := context.Background()
	failing := func(workspace.Actor, string) []studiotest.ToolStep {
		return []studiotest.ToolStep{{Err: errors.New("the model is down")}}
	}
	round := func(r *trigRig, id string) {
		GinkgoHelper()
		r.sh.clock.Advance(6 * time.Minute)
		r.pass()
		r.waitEnded(id)
	}
	switchedOff := func(r *trigRig, id string) func() bool {
		return func() bool {
			got, _ := r.host.GetTrigger(ctx, alice, id)
			return !got.Enabled
		}
	}
	attention := func(r *trigRig) int {
		n := 0
		for _, x := range r.notes() {
			if x.Kind == notify.KindNeedsAttention {
				n++
			}
		}
		return n
	}

	It("is switched off after 3 failed runs in a row and the owner is told", func() {
		r := newTrigRig(failing)
		tr, err := r.host.CreateTrigger(ctx, alice, cadenceInput("*/5 * * * *"))
		Expect(err).NotTo(HaveOccurred())
		for range 2 {
			round(r, tr.ID)
		}
		got, _ := r.host.GetTrigger(ctx, alice, tr.ID)
		Expect(got.Enabled).To(BeTrue(), "two failures are not enough")
		Expect(attention(r)).To(Equal(0))
		round(r, tr.ID)
		until("the trigger to be switched off", switchedOff(r, tr.ID))
		Expect(attention(r)).To(Equal(1), "notifications = %+v", r.notes())
	})

	It("keeps a trigger on when a run succeeded between the failures", func() {
		var n int
		r := newTrigRig(func(workspace.Actor, string) []studiotest.ToolStep {
			n++
			if n == 2 {
				return []studiotest.ToolStep{{Say: "All caught up."}}
			}
			return []studiotest.ToolStep{{Err: errors.New("the model is down")}}
		})
		tr, err := r.host.CreateTrigger(ctx, alice, cadenceInput("*/5 * * * *"))
		Expect(err).NotTo(HaveOccurred())
		for range 4 {
			round(r, tr.ID)
		}
		got, _ := r.host.GetTrigger(ctx, alice, tr.ID)
		Expect(got.Enabled).To(BeTrue(), "the streak was broken by a good run")
	})
})
