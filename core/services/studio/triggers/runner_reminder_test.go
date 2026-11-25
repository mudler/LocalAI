package triggers_test

import (
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

var _ = Describe("the reminder pass", func() {
	watchOf := func(id string) triggers.Trigger {
		return triggers.Trigger{ID: id, Enabled: true, Kind: triggers.KindEvent, Source: "notes", TZ: "UTC",
			Watch: &triggers.WatchRule{Offset: -15 * time.Minute}}
	}
	reminderOf := func(id, parent string, anchor time.Time) triggers.Trigger {
		return triggers.Trigger{ID: id, Enabled: true, Kind: triggers.KindReminder, Name: "Standup",
			Reminder: &triggers.Reminder{ParentID: parent, Key: "notes:" + id, Title: "Standup", Anchor: anchor, Offset: -15 * time.Minute}}
	}

	It("starts a reminder at its due time, once", func() {
		r := newRig()
		anchor := t0.Add(2 * time.Hour)
		r.put(watchOf("w"))
		r.put(reminderOf("r", "w", anchor))
		r.sync()
		Expect(r.runs("r")).To(BeEmpty(), "not due yet")
		r.at(anchor.Add(-16 * time.Minute))
		Expect(r.runs("r")).To(BeEmpty())
		r.at(anchor.Add(-15 * time.Minute))
		run := r.latest("r")
		Expect(run.DueKey).To(Equal(triggers.ReminderPrefix + anchor.Add(-15*time.Minute).Format(time.RFC3339)))
		Expect(run.Status).To(Equal(triggers.StatusRunning))
		Expect(run.Reason).To(BeEmpty())
		r.finish(run, triggers.StatusDone, "")
		r.after(time.Minute)
		Expect(r.runs("r")).To(HaveLen(1), "a due time fires once")
	})

	It("starts the lead before the due time", func() {
		r := newRig()
		anchor := t0.Add(3 * time.Hour)
		r.put(watchOf("w"))
		rem := reminderOf("r", "w", anchor)
		rem.Reminder.Lead = 30 * time.Minute
		r.put(rem)
		r.at(anchor.Add(-46 * time.Minute))
		Expect(r.runs("r")).To(BeEmpty())
		r.at(anchor.Add(-45 * time.Minute))
		Expect(r.runs("r")).To(HaveLen(1))
	})

	It("fires again at the new time after the thing moves", func() {
		r := newRig()
		anchor := t0.Add(time.Hour)
		r.put(watchOf("w"))
		rem := r.put(reminderOf("r", "w", anchor))
		r.at(anchor.Add(-15 * time.Minute))
		r.finish(r.latest("r"), triggers.StatusDone, "")
		rem, err := r.store.GetTrigger(ctx, rem.ID) // a write starts from the stored version
		Expect(err).NotTo(HaveOccurred())
		rem.Reminder.Anchor = anchor.Add(24 * time.Hour)
		rem.Since = r.clock.Now()
		Expect(r.store.PutTrigger(ctx, rem)).To(Succeed())
		r.at(anchor.Add(24*time.Hour - 15*time.Minute))
		Expect(r.runs("r")).To(HaveLen(2))
	})

	It("moves the reminder of a repeating thing to its next occurrence, also after a failed run", func() {
		var mu sync.Mutex
		var advanced []time.Time
		r := newRig(func(o *triggers.Options) {
			o.Advanced = func(t triggers.Trigger) { mu.Lock(); advanced = append(advanced, t.Reminder.Anchor); mu.Unlock() }
		})
		anchor := t0.Add(time.Hour)
		day := 24 * time.Hour
		r.put(watchOf("w"))
		rem := reminderOf("r", "w", anchor)
		rem.Reminder.Following = []time.Time{anchor.Add(day), anchor.Add(2 * day)}
		r.put(rem)
		r.at(anchor.Add(-15 * time.Minute))
		first := r.latest("r")
		r.at(anchor.Add(time.Minute))
		got, _ := r.store.GetTrigger(ctx, "r")
		Expect(got.Reminder.Anchor).To(Equal(anchor), "the run of this occurrence is still going")
		r.finish(first, triggers.StatusFailed, "the task failed")
		r.after(time.Minute)
		got, _ = r.store.GetTrigger(ctx, "r")
		Expect(got.Reminder.Anchor).To(Equal(anchor.Add(day)), "a failed run does not stop the series")
		Expect(got.Reminder.Following).To(Equal([]time.Time{anchor.Add(2 * day)}))
		r.at(anchor.Add(day - 15*time.Minute))
		Expect(r.runs("r")).To(HaveLen(2), "the next occurrence fires at its time")
		mu.Lock()
		defer mu.Unlock()
		Expect(advanced).To(Equal([]time.Time{anchor.Add(day)}))
	})

	It("skips the occurrences that passed while the server was down", func() {
		anchor := t0.Add(time.Hour)
		day := 24 * time.Hour
		r := newRig()
		r.put(watchOf("w"))
		rem := reminderOf("r", "w", anchor)
		rem.Reminder.Following = []time.Time{anchor.Add(day), anchor.Add(2 * day), anchor.Add(3 * day)}
		r.put(rem)
		r.at(anchor.Add(2*day + time.Hour))
		got, _ := r.store.GetTrigger(ctx, "r")
		Expect(got.Reminder.Anchor).To(Equal(anchor.Add(3 * day)))
		Expect(r.runs("r")).To(HaveLen(1), "one missed line for the occurrence it held, none for the ones in between")
	})

	It("holds the reminders of a paused watch, and starts them once it is back", func() {
		r := newRig()
		anchor := t0.Add(time.Hour)
		w := watchOf("w")
		w.Enabled = false
		r.put(w)
		r.put(reminderOf("r", "w", anchor))
		r.at(anchor.Add(-15 * time.Minute))
		Expect(r.runs("r")).To(BeEmpty(), "held while the watch is paused")
		w.Enabled = true
		r.put(w)
		r.after(time.Minute)
		Expect(r.runs("r")).To(HaveLen(1))
	})

	It("starts late with a note when the host was down past the due time but not past the thing", func() {
		r := newRig()
		anchor := t0.Add(time.Hour)
		r.put(watchOf("w"))
		r.put(reminderOf("r", "w", anchor))
		r.at(anchor.Add(-5 * time.Minute)) // the first pass after a stop
		run := r.latest("r")
		Expect(run.Reason).To(Equal(triggers.ReasonLate))
		Expect(run.Status).To(Equal(triggers.StatusRunning))
	})

	It("says missed once, and starts nothing, when the host was down past the thing", func() {
		var mu sync.Mutex
		var missed []string
		r := newRig(func(o *triggers.Options) {
			o.Missed = func(t triggers.Trigger, _ triggers.Run) { mu.Lock(); missed = append(missed, t.ID); mu.Unlock() }
		})
		anchor := t0.Add(time.Hour)
		r.put(watchOf("w"))
		r.put(reminderOf("r", "w", anchor))
		r.at(anchor.Add(10 * time.Minute))
		run := r.latest("r")
		Expect(run.Status).To(Equal(triggers.StatusSkipped))
		Expect(run.Reason).To(Equal(triggers.ReasonMissed))
		r.after(time.Minute)
		Expect(r.runs("r")).To(HaveLen(1))
		mu.Lock()
		defer mu.Unlock()
		Expect(missed).To(Equal([]string{"r"}))
		Expect(r.host.startCount()).To(Equal(0))
	})

	It("says missed, not late, for a reminder at the time of a thing that began during an outage", func() {
		var missed []string
		var mu sync.Mutex
		r := newRig(func(o *triggers.Options) {
			o.Missed = func(t triggers.Trigger, _ triggers.Run) { mu.Lock(); missed = append(missed, t.ID); mu.Unlock() }
		})
		anchor := t0.Add(time.Hour)
		r.put(watchOf("w"))
		rem := reminderOf("r", "w", anchor)
		rem.Reminder.Offset = 0
		r.put(rem)
		r.at(anchor.Add(48 * time.Hour)) // the first pass after two days down
		run := r.latest("r")
		Expect(run.Status).To(Equal(triggers.StatusSkipped))
		Expect(run.Reason).To(Equal(triggers.ReasonMissed))
		mu.Lock()
		defer mu.Unlock()
		Expect(missed).To(Equal([]string{"r"}))
	})

	It("fires a reminder at the time of its thing on time", func() {
		r := newRig()
		anchor := t0.Add(time.Hour)
		r.put(watchOf("w"))
		rem := reminderOf("r", "w", anchor)
		rem.Reminder.Offset = 0
		r.put(rem)
		r.at(anchor)
		run := r.latest("r")
		Expect(run.Status).To(Equal(triggers.StatusRunning))
		Expect(run.Reason).To(BeEmpty())
	})

	It("says nothing for a thing that had begun before its reminder was set", func() {
		r := newRig()
		anchor := t0.Add(-time.Hour)
		r.put(watchOf("w"))
		rem := reminderOf("r", "w", anchor)
		rem.Since = t0
		r.put(rem)
		r.sync()
		Expect(r.runs("r")).To(BeEmpty())
	})

	It("starts at once for a thing added inside the offset, with no late note", func() {
		r := newRig()
		anchor := t0.Add(5 * time.Minute)
		r.put(watchOf("w"))
		rem := reminderOf("r", "w", anchor)
		rem.Since = t0
		r.put(rem)
		r.sync()
		run := r.latest("r")
		Expect(run.Reason).To(BeEmpty())
		Expect(run.Status).To(Equal(triggers.StatusRunning))
	})

	It("wakes a reminder early on a change and keeps its due time", func() {
		r := newRig()
		anchor := t0.Add(3 * time.Hour)
		r.put(watchOf("w"))
		r.put(reminderOf("r", "w", anchor))
		run, err := r.runner.Wake(ctx, "r", triggers.Event{Source: "notes", Type: "notes.item.updated", DedupeKey: "c1", Payload: []byte(`{"id":"r"}`)})
		Expect(err).NotTo(HaveOccurred())
		Expect(run.DueKey).To(Equal(triggers.EventPrefix + "c1"))
		r.finish(r.latest("r"), triggers.StatusDone, "")
		again, err := r.runner.Wake(ctx, "r", triggers.Event{Source: "notes", DedupeKey: "c1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(again.ID).To(Equal(run.ID), "the same change wakes once")
		r.at(anchor.Add(-15 * time.Minute))
		Expect(r.runs("r")).To(HaveLen(2), "the early run did not use up the reminder")
		_, err = r.runner.Wake(ctx, "w", triggers.Event{Source: "notes", DedupeKey: "c2"})
		Expect(err).To(MatchError(triggers.ErrNotEnabled), "only a reminder wakes")
	})

	It("lets a watch take only the types it names", func() {
		w := watchOf("w")
		w.Watch.Types = []string{"notes.item.created"}
		Expect(w.MatchesEvent(triggers.Event{Source: "notes", Type: "notes.item.created"})).To(BeTrue())
		Expect(w.MatchesEvent(triggers.Event{Source: "notes", Type: "notes.item.deleted"})).To(BeFalse())
		w.Watch.Types = nil
		Expect(w.MatchesEvent(triggers.Event{Source: "notes", Type: "notes.item.deleted"})).To(BeTrue())
	})
})
