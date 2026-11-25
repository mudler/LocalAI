package triggers_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

// The tests of this file use a made-up source, "notes", whose things have an
// id, a title and an optional time. Nothing in them is about a calendar: the
// primitives must work for any add-on that declares its events.

func rome() *time.Location {
	loc, err := time.LoadLocation("Europe/Rome")
	Expect(err).NotTo(HaveOccurred())
	return loc
}

var _ = Describe("PlanReminder", func() {
	now := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	anchor := time.Date(2026, 11, 26, 14, 0, 0, 0, time.UTC)

	It("fires at the time of the thing plus the offset, and starts the lead before", func() {
		tm := triggers.PlanReminder(triggers.Reminder{Anchor: anchor, Offset: -15 * time.Minute, Lead: 30 * time.Minute}, nil, time.UTC, now)
		Expect(tm.Due).To(Equal(anchor.Add(-15 * time.Minute)))
		Expect(tm.Start).To(Equal(anchor.Add(-45 * time.Minute)))
		Expect(tm.Now || tm.Skip || tm.None).To(BeFalse())
		Expect(tm.Adjusted).To(BeEmpty())
	})

	It("uses an absolute time over the offset, and a snooze over both", func() {
		at := now.Add(3 * time.Hour)
		tm := triggers.PlanReminder(triggers.Reminder{Anchor: anchor, Offset: -15 * time.Minute, At: at}, nil, time.UTC, now)
		Expect(tm.Due).To(Equal(at))
		snooze := now.Add(time.Hour)
		tm = triggers.PlanReminder(triggers.Reminder{Anchor: anchor, At: at, SnoozedTo: snooze}, nil, time.UTC, now)
		Expect(tm.Due).To(Equal(snooze))
	})

	It("times a thing with no time of its own by its absolute time only", func() {
		at := now.Add(20 * time.Minute)
		tm := triggers.PlanReminder(triggers.Reminder{At: at}, nil, time.UTC, now)
		Expect(tm.Due).To(Equal(at))
		Expect(tm.Skip).To(BeFalse(), "a thing with no time never counts as started")
	})

	It("starts now when the thing starts inside the offset and the lead", func() {
		soon := now.Add(10 * time.Minute)
		tm := triggers.PlanReminder(triggers.Reminder{Anchor: soon, Offset: -15 * time.Minute, Lead: 30 * time.Minute}, nil, time.UTC, now)
		Expect(tm.Now).To(BeTrue())
		Expect(tm.Start).To(Equal(now))
		Expect(tm.Skip).To(BeFalse())
	})

	It("fires a reminder due at or after the thing's start, and one snoozed past it", func() {
		at := now.Add(-time.Minute) // the thing began a minute ago
		for _, r := range []triggers.Reminder{
			{Anchor: at},                           // at the time
			{Anchor: at, Offset: 10 * time.Minute}, // after it
			{Anchor: at, Offset: -15 * time.Minute, SnoozedTo: now.Add(30 * time.Minute)},
		} {
			tm := triggers.PlanReminder(r, nil, time.UTC, now)
			Expect(tm.Skip).To(BeFalse(), "%+v", r)
		}
		tm := triggers.PlanReminder(triggers.Reminder{Anchor: at}, nil, time.UTC, now)
		Expect(tm.Due).To(Equal(at))
		Expect(tm.Now).To(BeTrue(), "due now: it starts at once")
	})

	It("skips a thing that has already started", func() {
		tm := triggers.PlanReminder(triggers.Reminder{Anchor: now.Add(-time.Minute), Offset: -15 * time.Minute}, nil, time.UTC, now)
		Expect(tm.Skip).To(BeTrue())
	})

	It("reminds of an all-day thing at 08:00 of its day in its zone, or not at all", func() {
		day := time.Date(2026, 11, 27, 0, 0, 0, 0, rome())
		r := triggers.Reminder{Anchor: day.UTC(), Zone: "Europe/Rome", AllDay: true, Offset: -15 * time.Minute}
		tm := triggers.PlanReminder(r, &triggers.WatchRule{}, time.UTC, now)
		Expect(tm.Due).To(Equal(time.Date(2026, 11, 27, 8, 0, 0, 0, rome()).UTC()))
		Expect(tm.Adjusted).To(Equal(triggers.AdjustedAllDay))
		tm = triggers.PlanReminder(r, &triggers.WatchRule{AllDay: triggers.AllDayNone}, time.UTC, now)
		Expect(tm.None).To(BeTrue())
		during := time.Date(2026, 11, 27, 10, 0, 0, 0, rome())
		Expect(triggers.PlanReminder(r, &triggers.WatchRule{}, time.UTC, during).Skip).To(BeFalse(), "the day is not over")
		Expect(triggers.PlanReminder(r, &triggers.WatchRule{}, time.UTC, day.Add(25*time.Hour)).Skip).To(BeTrue())
	})

	It("reads the day of an all-day thing with no zone as given, west of UTC too", func() {
		ny, err := time.LoadLocation("America/New_York")
		Expect(err).NotTo(HaveOccurred())
		r := triggers.Reminder{Anchor: time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC), AllDay: true}
		tm := triggers.PlanReminder(r, &triggers.WatchRule{}, ny, now)
		Expect(tm.Due).To(Equal(time.Date(2026, 11, 27, 8, 0, 0, 0, ny).UTC()), "the morning of the 27th, not of the 26th")
		evening := time.Date(2026, 11, 27, 23, 0, 0, 0, ny)
		Expect(triggers.PlanReminder(r, &triggers.WatchRule{}, ny, evening).Skip).To(BeFalse(), "the day is not over on the owner's clock")
	})

	It("ends the day of an all-day thing at midnight on a day of 25 hours", func() {
		// 2026-10-25 lasts 25 hours in Rome: at 23:30 the day is not over,
		// though 24 hours from its midnight have passed.
		day := time.Date(2026, 10, 25, 0, 0, 0, 0, rome())
		r := triggers.Reminder{Anchor: day.UTC(), Zone: "Europe/Rome", AllDay: true}
		late := time.Date(2026, 10, 25, 23, 30, 0, 0, rome())
		Expect(late.Sub(day)).To(BeNumerically(">", 24*time.Hour))
		tm := triggers.PlanReminder(r, &triggers.WatchRule{}, rome(), late)
		Expect(tm.Skip).To(BeFalse())
		Expect(triggers.PlanReminder(r, &triggers.WatchRule{}, rome(), late.Add(time.Hour)).Skip).To(BeTrue())
	})

	It("moves a reminder out of the quiet hours to the evening before, in the owner's zone", func() {
		rule := &triggers.WatchRule{Offset: -15 * time.Minute, QuietFrom: "22:00", QuietTo: "07:00"}
		early := time.Date(2026, 11, 27, 6, 45, 0, 0, rome())
		tm := triggers.PlanReminder(triggers.Reminder{Anchor: early.Add(15 * time.Minute), Offset: -15 * time.Minute}, rule, rome(), now)
		Expect(tm.Due).To(Equal(time.Date(2026, 11, 26, 22, 0, 0, 0, rome()).UTC()))
		Expect(tm.Adjusted).To(Equal(triggers.AdjustedQuiet))
		late := time.Date(2026, 11, 26, 23, 30, 0, 0, rome())
		tm = triggers.PlanReminder(triggers.Reminder{Anchor: late.Add(15 * time.Minute), Offset: -15 * time.Minute}, rule, rome(), now)
		Expect(tm.Due).To(Equal(time.Date(2026, 11, 26, 22, 0, 0, 0, rome()).UTC()))
		day := time.Date(2026, 11, 26, 15, 0, 0, 0, rome())
		tm = triggers.PlanReminder(triggers.Reminder{Anchor: day, Offset: -15 * time.Minute}, rule, rome(), now)
		Expect(tm.Adjusted).To(BeEmpty())
		Expect(tm.Due).To(Equal(day.Add(-15 * time.Minute).UTC()))
	})

	It("keeps a time the owner chose inside the quiet hours", func() {
		rule := &triggers.WatchRule{QuietFrom: "22:00", QuietTo: "07:00"}
		at := time.Date(2026, 11, 26, 23, 0, 0, 0, rome())
		tm := triggers.PlanReminder(triggers.Reminder{At: at}, rule, rome(), now)
		Expect(tm.Due).To(Equal(at.UTC()))
		Expect(tm.Adjusted).To(BeEmpty())
	})

	It("keeps the wall time of a thing across a change of daylight saving time", func() {
		// 2026-10-25 is the end of summer time in Rome. A thing at 09:00 on the
		// day after keeps 09:00 local: the add-on gives the instant with its zone.
		start := time.Date(2026, 10, 26, 9, 0, 0, 0, rome())
		before := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
		tm := triggers.PlanReminder(triggers.Reminder{Anchor: start.UTC(), Zone: "Europe/Rome", Offset: -15 * time.Minute}, nil, rome(), before)
		Expect(tm.Due.In(rome()).Format("15:04")).To(Equal("08:45"))
		day := time.Date(2026, 10, 25, 0, 0, 0, 0, rome())
		tm = triggers.PlanReminder(triggers.Reminder{Anchor: day.UTC(), Zone: "Europe/Rome", AllDay: true}, &triggers.WatchRule{}, rome(), before)
		Expect(tm.Due.In(rome()).Format("2006-01-02 15:04")).To(Equal("2026-10-25 08:00"), "the morning of the day the clocks change")
		rule := &triggers.WatchRule{QuietFrom: "22:00", QuietTo: "07:00"}
		early := time.Date(2026, 10, 25, 6, 0, 0, 0, rome())
		tm = triggers.PlanReminder(triggers.Reminder{At: time.Time{}, Anchor: early.UTC()}, rule, rome(), before)
		Expect(tm.Due.In(rome()).Format("2006-01-02 15:04")).To(Equal("2026-10-24 22:00"))
	})
})

var _ = Describe("Reconcile", func() {
	ctx := context.Background()
	now := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	anchor := time.Date(2026, 11, 26, 14, 0, 0, 0, time.UTC)
	var st triggers.Store
	watch := triggers.Trigger{ID: "w", Owner: "u", Kind: triggers.KindEvent, Source: "notes", Watch: &triggers.WatchRule{Offset: -15 * time.Minute}}

	thing := func(lifecycle string, at time.Time, stamp string) triggers.Event {
		return triggers.Event{Source: "notes", Type: "notes.item." + lifecycle, DedupeKey: "d-" + lifecycle,
			Thing: &triggers.Thing{Key: "notes:7", Lifecycle: lifecycle, Title: "Write the report", Anchor: at, Stamp: stamp}}
	}
	BeforeEach(func() {
		var err error
		st, err = triggers.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		Expect(st.PutTrigger(ctx, triggers.Trigger{ID: "r", Owner: "u", Name: "Write the report", Prompt: "Remind me.", Enabled: true,
			Kind: triggers.KindReminder, Since: now.Add(-time.Hour), Reminder: &triggers.Reminder{ParentID: "w", Key: "notes:7", Title: "Write the report",
				Anchor: anchor, Offset: -15 * time.Minute, WakeOnChange: true, Stamp: "s1", SnoozedTo: anchor.Add(-time.Hour)}})).To(Succeed())
	})

	It("re-times the reminder from the new time of the thing, with no model, and drops a snooze", func() {
		moved := anchor.Add(2 * time.Hour)
		ev := thing(triggers.LifecycleUpdated, moved, "s1")
		ev.Thing.Following = []time.Time{moved.Add(24 * time.Hour)}
		out, err := triggers.Reconcile(ctx, st, watch, ev, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Action).To(Equal(triggers.ActionRetimed))
		Expect(out.Moved).To(BeTrue())
		got, _ := st.ReminderByKey(ctx, "w", "notes:7")
		Expect(got.Reminder.Anchor).To(Equal(moved))
		Expect(got.Reminder.Following).To(Equal([]time.Time{moved.Add(24 * time.Hour)}), "the later occurrences come with each event")
		Expect(got.Reminder.SnoozedTo.IsZero()).To(BeTrue())
		Expect(got.Since).To(Equal(now))
		tm := triggers.PlanReminder(*got.Reminder, watch.Watch, time.UTC, now)
		Expect(tm.Due).To(Equal(moved.Add(-15 * time.Minute)))
	})

	It("cancels the reminder when the thing is deleted", func() {
		out, err := triggers.Reconcile(ctx, st, watch, thing(triggers.LifecycleDeleted, time.Time{}, ""), now)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Action).To(Equal(triggers.ActionCancelled))
		Expect(out.Before.ID).To(Equal("r"))
		_, err = st.ReminderByKey(ctx, "w", "notes:7")
		Expect(err).To(MatchError(triggers.ErrNotFound))
	})

	It("wakes the reminder early for a change beyond a move, and keeps its time", func() {
		out, err := triggers.Reconcile(ctx, st, watch, thing(triggers.LifecycleUpdated, anchor, "s2"), now)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Action).To(Equal(triggers.ActionWoken))
		Expect(out.Moved).To(BeFalse())
		got, _ := st.ReminderByKey(ctx, "w", "notes:7")
		Expect(got.Reminder.Stamp).To(Equal("s2"))
		Expect(got.Reminder.SnoozedTo.IsZero()).To(BeFalse(), "a change that is not a move keeps the owner's snooze")
	})

	It("does not wake a reminder whose wake on change is off", func() {
		r, _ := st.ReminderByKey(ctx, "w", "notes:7")
		r.Reminder.WakeOnChange = false
		Expect(st.PutTrigger(ctx, r)).To(Succeed())
		out, err := triggers.Reconcile(ctx, st, watch, thing(triggers.LifecycleUpdated, anchor, "s2"), now)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Action).To(Equal(triggers.ActionUnchanged))
	})

	It("hands a thing with no reminder to the model, and ignores the removal of one", func() {
		ev := thing(triggers.LifecycleCreated, anchor, "x")
		ev.Thing.Key = "notes:8"
		out, err := triggers.Reconcile(ctx, st, watch, ev, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Action).To(Equal(triggers.ActionModel))
		ev.Thing.Lifecycle = triggers.LifecycleUpdated
		out, _ = triggers.Reconcile(ctx, st, watch, ev, now)
		Expect(out.Action).To(Equal(triggers.ActionModel), "an update may now need a reminder")
		ev.Thing.Lifecycle = triggers.LifecycleDeleted
		out, _ = triggers.Reconcile(ctx, st, watch, ev, now)
		Expect(out.Action).To(Equal(triggers.ActionNone))
		out, _ = triggers.Reconcile(ctx, st, watch, triggers.Event{Source: "notes"}, now)
		Expect(out.Action).To(Equal(triggers.ActionModel))
	})

	It("re-times a repeating thing to its next occurrence like any move", func() {
		next := anchor.Add(7 * 24 * time.Hour)
		out, err := triggers.Reconcile(ctx, st, watch, thing(triggers.LifecycleUpdated, next, "s1"), now)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Action).To(Equal(triggers.ActionRetimed))
		Expect(out.After.Reminder.Anchor).To(Equal(next))
	})

	It("keeps the reminders of another watch out of reach", func() {
		other := watch
		other.ID = "w2"
		out, err := triggers.Reconcile(ctx, st, other, thing(triggers.LifecycleDeleted, time.Time{}, ""), now)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Action).To(Equal(triggers.ActionNone))
		_, err = st.ReminderByKey(ctx, "w", "notes:7")
		Expect(err).NotTo(HaveOccurred())
	})
})

var _ = Describe("the loop guard", func() {
	w := triggers.Trigger{ID: "w"}
	parent := func(id string) string {
		if id == "r" {
			return "w"
		}
		return ""
	}

	It("keeps a run of the watch or of its reminders from waking the watch", func() {
		Expect(triggers.Guarded(w, triggers.Actor{Kind: triggers.ActorAgent, TriggerID: "w"}, parent)).To(BeTrue())
		Expect(triggers.Guarded(w, triggers.Actor{Kind: triggers.ActorAgent, TriggerID: "r"}, parent)).To(BeTrue())
	})

	It("lets another watch, the owner and a client wake it", func() {
		Expect(triggers.Guarded(w, triggers.Actor{Kind: triggers.ActorAgent, TriggerID: "w2"}, parent)).To(BeFalse())
		Expect(triggers.Guarded(w, triggers.Actor{Kind: triggers.ActorOwner}, parent)).To(BeFalse())
		Expect(triggers.Guarded(w, triggers.Actor{Kind: triggers.ActorClient, ClientID: "c"}, parent)).To(BeFalse())
		Expect(triggers.Guarded(w, triggers.Actor{}, nil)).To(BeFalse())
	})
})

var _ = Describe("CountHandledSince", func() {
	It("counts the handled rows of the last minute only", func() {
		ctx := context.Background()
		st, _ := triggers.NewJSON("")
		now := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
		for i, due := range []time.Time{now.Add(-2 * time.Minute), now.Add(-30 * time.Second), now} {
			_, err := st.Claim(ctx, triggers.Run{ID: string(rune('a' + i)), TriggerID: "w", DueKey: string(rune('a' + i)), Due: due, Status: triggers.StatusHandled})
			Expect(err).NotTo(HaveOccurred())
		}
		// The newest rows are model runs: they do not hide the changes
		// before them.
		for i := range 40 {
			id := fmt.Sprintf("m%d", i)
			_, _ = st.Claim(ctx, triggers.Run{ID: id, TriggerID: "w", DueKey: id, Due: now, Status: triggers.StatusDone})
		}
		n, err := st.CountHandledSince(ctx, "w", now.Add(-time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(2))
	})
})
