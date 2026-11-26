package studio_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/notify"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// notesSource is the fake source of the flow tests: an add-on with generic
// items and no calendar.
func notesSource(context.Context) ([]studio.WatchSource, error) {
	return []studio.WatchSource{{ID: "notes", Title: contract.Localized{Text: "Notes"},
		Events: []contract.EventDeclaration{notesDecl("created"), notesDecl("updated"), notesDecl("deleted")}}}, nil
}

// flowScript is a scripted model: the watch's run sets a reminder for the
// item it is told about, and stays silent; the reminder's run says its text,
// or fails when failReminder is set.
type flowScript struct {
	failReminder bool
	watchSays    string
	watchArgs    string
	seen         []string
}

func (f *flowScript) script(_ workspace.Actor, text string) []studiotest.ToolStep {
	f.seen = append(f.seen, text)
	switch {
	case strings.Contains(text, "You are a watch on notes"):
		args := f.watchArgs
		if args == "" {
			args = `{"key":"notes:1"}`
		}
		say := f.watchSays
		if say == "" {
			say = studio.SilentMark
		}
		return []studiotest.ToolStep{{Tool: studio.SetReminderName, Args: args}, {Say: say}}
	case strings.Contains(text, "This is the reminder for an item"):
		if f.failReminder {
			return []studiotest.ToolStep{{Err: errors.New("the brief could not be written")}}
		}
		return []studiotest.ToolStep{{Say: "Pay the rent: it is due in 15 minutes."}}
	}
	return []studiotest.ToolStep{{Say: "ok"}}
}

type flowRig struct {
	*trigRig
	f     *flowScript
	watch triggers.Trigger
}

func newFlowRig(f *flowScript, rule triggers.WatchRule) *flowRig {
	r := newShared().rig(f.script, studio.TriggerOptions{Sources: notesSource})
	w, err := r.host.CreateTrigger(context.Background(), alice, studio.TriggerInput{Name: "My notes", Prompt: "Keep me on top of my notes.",
		Kind: triggers.KindEvent, Source: "notes", TZ: "UTC", Model: "m", Enabled: true, Watch: &rule})
	Expect(err).NotTo(HaveOccurred())
	return &flowRig{trigRig: r, f: f, watch: w}
}

// raceStore runs before once, just before the first write of a reminder:
// the moment another frontend may write the same reminder.
type raceStore struct {
	triggers.Store
	before func()
}

func (s *raceStore) PutTrigger(ctx context.Context, t triggers.Trigger) error {
	if f := s.before; f != nil && t.IsReminder() {
		s.before = nil
		f()
	}
	return s.Store.PutTrigger(ctx, t)
}

func (r *flowRig) emit(lifecycle, id string, due time.Time, extra, cred string) {
	Expect(r.host.Emit(context.Background(), studio.EmitInput{Owner: alice, Addon: "notes", Decl: notesDecl(lifecycle),
		Payload: notesPayload(id, "Pay the rent", due, extra), CredentialID: cred})).To(Succeed())
}

func (r *flowRig) reminder(id string) (triggers.Trigger, error) {
	return r.sh.trg.ReminderByKey(context.Background(), r.watch.ID, "notes:"+id)
}

// modelRun waits for the newest model run of the watch to end, passing over
// the rows of its history that record actions.
func (r *flowRig) modelRun() triggers.Run {
	var run triggers.Run
	until("the watch's run to end", func() bool {
		runs, _ := r.sh.trg.ListRuns(context.Background(), r.watch.ID, 50)
		for _, x := range runs {
			if x.Status != triggers.StatusHandled {
				run = x
				return x.Status.Final()
			}
		}
		return false
	})
	return run
}

func (r *flowRig) bodies() []string {
	var out []string
	for _, n := range r.notes() {
		out = append(out, n.Title+" | "+n.Body)
	}
	return out
}

var _ = Describe("a watch, end to end with a scripted model", func() {
	ctx := context.Background()

	It("refuses a watch of a source that reports no changes", func() {
		r := newShared().rig(saySummary, studio.TriggerOptions{Sources: notesSource})
		_, err := r.host.CreateTrigger(ctx, alice, studio.TriggerInput{Name: "W", Prompt: "p", Kind: triggers.KindEvent, Source: "mail",
			Model: "m", Enabled: true, Watch: &triggers.WatchRule{Offset: -15 * time.Minute}})
		Expect(err).To(MatchError(studio.ErrBadTrigger))
		_, err = r.host.CreateTrigger(ctx, alice, studio.TriggerInput{Name: "W", Prompt: "p", Kind: triggers.KindEvent, Source: "notes",
			Model: "m", Enabled: true, Watch: &triggers.WatchRule{Types: []string{"notes.item.vanished"}}})
		Expect(err).To(MatchError(studio.ErrBadTrigger))
	})

	It("creates, moves and fires: one model run, a silent move, the reminder on time", func() {
		r := newFlowRig(&flowScript{}, triggers.WatchRule{Offset: -15 * time.Minute})
		due := r.sh.clock.Now().Add(26 * time.Hour)
		r.emit("created", "1", due, "", "")
		run := r.modelRun()
		Expect(run.Status).To(Equal(triggers.StatusDone))
		rem, err := r.reminder("1")
		Expect(err).NotTo(HaveOccurred(), "the model set the reminder")
		Expect(rem.Reminder.Anchor).To(Equal(due))
		Expect(rem.Reminder.Title).To(Equal("Pay the rent"), "the host gave the facts of the item")
		Expect(rem.Reminder.Offset).To(Equal(-15*time.Minute), "the watch's default")
		Expect(rem.Reminder.SetBy).To(Equal(run.ID), "the reminder records the run that set it, for a reply to that run's message")
		Expect(r.bodies()).To(ConsistOf("My notes | Set the reminder for \"Pay the rent\": Wed 08:45."),
			"the owner made the item and the model set the usual reminder: no heads-up, one line")
		n := r.notes()[0]
		Expect(n.Code).To(Equal("watch.set"), "the interface words the line in the owner's language")
		Expect(n.Params).To(HaveKeyWithValue("title", "Pay the rent"))
		Expect(n.Params).To(HaveKeyWithValue("at", due.Add(-15*time.Minute).Format(time.RFC3339)))
		Expect(n.Params).To(HaveKeyWithValue("zone", "UTC"))

		moved := due.Add(time.Hour)
		r.emit("updated", "1", moved, "", "")
		Expect(r.f.seen).To(HaveLen(1), "a move is computed with no model call")
		Expect(r.bodies()).To(ContainElement("My notes | Moved the reminder for \"Pay the rent\" to Wed 09:45."))

		r.sh.clock.Advance(moved.Add(-15*time.Minute).Sub(r.sh.clock.Now()) + time.Second)
		r.pass()
		fired := r.waitEnded(rem.ID)
		Expect(fired.Status).To(Equal(triggers.StatusDone))
		Expect(fired.DueKey).To(Equal(triggers.ReminderPrefix + moved.Add(-15*time.Minute).Format(time.RFC3339)))
		Expect(r.bodies()).To(ContainElement("Pay the rent | Pay the rent: it is due in 15 minutes."))
		first := r.f.seen[len(r.f.seen)-1]
		Expect(first).To(ContainSubstring(`title as data: "Pay the rent"`))
		Expect(first).To(ContainSubstring("Task: remind me"))
	})

	It("creates and deletes: the reminder is gone and the owner reads one line", func() {
		r := newFlowRig(&flowScript{}, triggers.WatchRule{Offset: -15 * time.Minute})
		r.emit("created", "1", r.sh.clock.Now().Add(26*time.Hour), "", "")
		r.modelRun()
		r.emit("deleted", "1", time.Time{}, "", "")
		_, err := r.reminder("1")
		Expect(err).To(MatchError(triggers.ErrNotFound))
		Expect(r.bodies()).To(ContainElement("My notes | Cancelled the reminder for \"Pay the rent\": it was removed."))
	})

	It("sends the plain reminder on time when the prep task fails", func() {
		f := &flowScript{watchArgs: `{"key":"notes:1","task":"prepare a brief from my mail","lead":"30m"}`, watchSays: "I'll send you a brief at 08:45."}
		r := newFlowRig(f, triggers.WatchRule{Offset: -15 * time.Minute})
		due := r.sh.clock.Now().Add(26 * time.Hour)
		r.emit("created", "1", due, "", "")
		r.modelRun()
		Expect(r.bodies()).To(ContainElement("My notes finished | I'll send you a brief at 08:45."), "a planned brief is worth a heads-up")
		f.failReminder = true
		rem, err := r.reminder("1")
		Expect(err).NotTo(HaveOccurred())
		Expect(rem.Reminder.Lead).To(Equal(30 * time.Minute))
		r.sh.clock.Advance(due.Add(-45*time.Minute).Sub(r.sh.clock.Now()) + time.Second)
		r.pass()
		run := r.waitEnded(rem.ID)
		Expect(run.Status).To(Equal(triggers.StatusFailed))
		Eventually(r.bodies).Should(ContainElement(MatchRegexp(`Pay the rent \| Reminder: Pay the rent at \d{4}-\d{2}-\d{2} 09:00\. \(The task "prepare a brief from my mail" did not finish\.\)`)))
	})

	It("starts late with a note when the server was down at the reminder time", func() {
		r := newFlowRig(&flowScript{}, triggers.WatchRule{Offset: -15 * time.Minute})
		due := r.sh.clock.Now().Add(26 * time.Hour)
		r.emit("created", "1", due, "", "")
		r.modelRun()
		rem, _ := r.reminder("1")
		r.sh.clock.Advance(due.Add(-5 * time.Minute).Sub(r.sh.clock.Now()))
		r.pass()
		run := r.waitEnded(rem.ID)
		Expect(run.Reason).To(Equal(triggers.ReasonLate))
		Expect(r.f.seen[len(r.f.seen)-1]).To(ContainSubstring("starts late because the server was down"))
	})

	It("tells the owner when someone else moves the item, and stays quiet when the owner does", func() {
		r := newFlowRig(&flowScript{}, triggers.WatchRule{Offset: -15 * time.Minute})
		due := r.sh.clock.Now().Add(26 * time.Hour)
		r.emit("created", "1", due, "", "")
		r.modelRun()
		r.emit("updated", "1", due.Add(time.Hour), "", "")
		r.emit("updated", "1", due.Add(2*time.Hour), "", contract.ClientCredentialPrefix+"phone")
		Expect(r.bodies()).To(ContainElement("My notes | Moved the reminder for \"Pay the rent\" to Wed 09:45."))
		Expect(r.bodies()).To(ContainElement("My notes | \"Pay the rent\" was moved. I'll remind you at Wed 10:45."))
	})

	It("does not wake itself on a change its own run made", func() {
		r := newFlowRig(&flowScript{}, triggers.WatchRule{Offset: -15 * time.Minute})
		due := r.sh.clock.Now().Add(26 * time.Hour)
		r.emit("created", "1", due, "", "")
		run := r.modelRun()
		Expect(r.host.Emit(ctx, studio.EmitInput{Owner: alice, Addon: "notes", Decl: notesDecl("created"),
			Payload: notesPayload("9", "Made by the watch", due, ""), ChatID: run.ChatID})).To(Succeed())
		Expect(r.f.seen).To(HaveLen(1))
	})

	It("wakes the reminder early on a change beyond its time, and it still fires at its time", func() {
		r := newFlowRig(&flowScript{}, triggers.WatchRule{Offset: -15 * time.Minute})
		due := r.sh.clock.Now().Add(26 * time.Hour)
		r.emit("created", "1", due, "", "")
		r.modelRun()
		rem, _ := r.reminder("1")
		r.emit("updated", "1", due, "now with Sam", "")
		early := r.waitEnded(rem.ID)
		Expect(early.DueKey).To(HavePrefix(triggers.EventPrefix))
		Expect(r.f.seen[len(r.f.seen)-1]).To(ContainSubstring("The item changed before the reminder time"))
		Expect(r.f.seen[len(r.f.seen)-1]).To(ContainSubstring("now with Sam"), "the new details reach the run as data")
		r.sh.clock.Advance(due.Add(-15*time.Minute).Sub(r.sh.clock.Now()) + time.Second)
		r.pass()
		Eventually(func() int { runs, _ := r.sh.trg.ListRuns(ctx, rem.ID, 10); return len(runs) }).Should(Equal(2))
	})

	It("holds the reminders of a paused watch and removes them with the watch, each with a line", func() {
		r := newFlowRig(&flowScript{}, triggers.WatchRule{Offset: -15 * time.Minute})
		r.emit("created", "1", r.sh.clock.Now().Add(26*time.Hour), "", "")
		r.modelRun()
		_, err := r.host.SetTriggerEnabled(ctx, alice, r.watch.ID, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.bodies()).To(ContainElement("My notes | The reminder for \"Pay the rent\" is on hold while this watch is paused."))
		Expect(r.host.DeleteTrigger(ctx, alice, r.watch.ID)).To(Succeed())
		_, err = r.reminder("1")
		Expect(err).To(MatchError(triggers.ErrNotFound))
		Expect(r.bodies()).To(ContainElement("My notes | The reminder for \"Pay the rent\" was removed with its watch."))
	})

	It("keeps the reminders of a paused watch in step with their items, with no run", func() {
		r := newFlowRig(&flowScript{}, triggers.WatchRule{Offset: -15 * time.Minute})
		due := r.sh.clock.Now().Add(26 * time.Hour)
		r.emit("created", "1", due, "", "")
		r.modelRun()
		_, err := r.host.SetReminder(ctx, r.watch, studio.ReminderInput{Key: "notes:2", Anchor: due.Format(time.RFC3339), Title: "Call Sam"})
		Expect(err).NotTo(HaveOccurred())
		_, err = r.host.SetTriggerEnabled(ctx, alice, r.watch.ID, false)
		Expect(err).NotTo(HaveOccurred())
		seen := len(r.f.seen)
		r.emit("updated", "1", due.Add(time.Hour), "now with Sam", contract.ClientCredentialPrefix+"phone")
		rem, err := r.reminder("1")
		Expect(err).NotTo(HaveOccurred())
		Expect(rem.Reminder.Anchor).To(Equal(due.Add(time.Hour)), "a move while paused is kept")
		r.emit("deleted", "2", time.Time{}, "", "")
		_, err = r.reminder("2")
		Expect(err).To(MatchError(triggers.ErrNotFound), "a removal while paused cancels the reminder")
		r.emit("created", "3", due, "", "")
		_, err = r.reminder("3")
		Expect(err).To(MatchError(triggers.ErrNotFound))
		Expect(r.f.seen).To(HaveLen(seen), "no model run while paused")
		runs, _ := r.sh.trg.ListRuns(ctx, rem.ID, 10)
		Expect(runs).To(BeEmpty(), "no early run of a reminder on hold")

		// The limit on changes holds for a paused watch too.
		for i := range triggers.MaxChangesPerMinute + 1 {
			id := fmt.Sprintf("h%d", i)
			_, err := r.sh.trg.Claim(ctx, triggers.Run{ID: id, TriggerID: r.watch.ID, DueKey: "action@" + id, Due: r.sh.clock.Now(), Status: triggers.StatusHandled})
			Expect(err).NotTo(HaveOccurred())
		}
		r.emit("updated", "1", due.Add(3*time.Hour), "", contract.ClientCredentialPrefix+"phone")
		rem, _ = r.reminder("1")
		Expect(rem.Reminder.Anchor).To(Equal(due.Add(time.Hour)), "a paused watch over the limit takes no change")
	})

	It("starts no model run for a change of an item that began over a day ago", func() {
		r := newFlowRig(&flowScript{}, triggers.WatchRule{Offset: -15 * time.Minute})
		r.emit("updated", "9", r.sh.clock.Now().Add(-48*time.Hour), "", "")
		r.emit("updated", "8", time.Time{}, "", "") // no time left, as the calendar sends a past event
		Consistently(func() []string { return r.f.seen }, 300*time.Millisecond).Should(BeEmpty())
		_, err := r.reminder("9")
		Expect(err).To(MatchError(triggers.ErrNotFound))
	})

	It("lets the owner snooze, skip and re-time a reminder, and undo each", func() {
		r := newFlowRig(&flowScript{}, triggers.WatchRule{Offset: -15 * time.Minute})
		due := r.sh.clock.Now().Add(time.Hour)
		r.emit("created", "1", due, "", "")
		r.modelRun()
		list, err := r.host.Reminders(ctx, alice, r.watch.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		v := list[0]
		Expect(v.Due).To(Equal(due.Add(-15 * time.Minute)))
		Expect(v.Snoozable).To(BeTrue())
		got, err := r.host.PatchReminder(ctx, alice, r.watch.ID, v.ID, studio.ReminderPatch{SnoozeFor: 5 * time.Minute})
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Due).To(Equal(r.sh.clock.Now().Add(5 * time.Minute)))
		empty := ""
		got, _ = r.host.PatchReminder(ctx, alice, r.watch.ID, v.ID, studio.ReminderPatch{SnoozedTo: &empty})
		Expect(got.Due).To(Equal(v.Due), "undo of the snooze")
		yes, no := true, false
		got, _ = r.host.PatchReminder(ctx, alice, r.watch.ID, v.ID, studio.ReminderPatch{Skip: &yes})
		Expect(got.Skipped).To(BeTrue())
		r.sh.clock.Advance(50 * time.Minute)
		r.pass()
		runs, _ := r.sh.trg.ListRuns(ctx, v.ID, 10)
		Expect(runs).To(BeEmpty(), "a skipped time does not fire")
		got, _ = r.host.PatchReminder(ctx, alice, r.watch.ID, v.ID, studio.ReminderPatch{Skip: &no})
		Expect(got.Skipped).To(BeFalse())
		fiveBefore := -5 * time.Minute
		got, err = r.host.PatchReminder(ctx, alice, r.watch.ID, v.ID, studio.ReminderPatch{Offset: &fiveBefore})
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Due).To(Equal(due.Add(-5 * time.Minute)))
		Expect(r.host.DeleteReminder(ctx, alice, r.watch.ID, v.ID)).To(Succeed())
		_, err = r.host.PatchReminder(ctx, alice, r.watch.ID, v.ID, studio.ReminderPatch{Skip: &yes})
		Expect(err).To(MatchError(triggers.ErrNotFound))
		_, err = r.host.Reminders(ctx, "bob", r.watch.ID)
		Expect(err).To(MatchError(triggers.ErrNotFound), "another user's watch is a missing one")
	})

	It("lists the reminders of an add-on's items for its page, and only for that add-on", func() {
		r := newFlowRig(&flowScript{}, triggers.WatchRule{Offset: -15 * time.Minute})
		r.emit("created", "1", r.sh.clock.Now().Add(26*time.Hour), "", "")
		r.modelRun()
		list, err := r.host.SourceReminders(ctx, alice, "notes")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].ItemID).To(Equal("1"))
		Expect(list[0].WatchID).To(Equal(r.watch.ID))
		Expect(list[0].View.Due.IsZero()).To(BeFalse())
		other, _ := r.host.SourceReminders(ctx, alice, "calendar")
		Expect(other).To(BeEmpty())
		bobs, _ := r.host.SourceReminders(ctx, "bob", "notes")
		Expect(bobs).To(BeEmpty())
		_, err = r.host.SourceReminders(ctx, alice, "")
		Expect(err).To(MatchError(studio.ErrBadTrigger), "a page lists only under an add-on")

		yes := true
		_, err = r.host.PatchReminder(ctx, alice, list[0].WatchID, list[0].View.ID, studio.ReminderPatch{Skip: &yes, Source: "calendar"})
		Expect(err).To(MatchError(triggers.ErrNotFound), "another add-on's page cannot skip a reminder of this watch")
		got, err := r.host.PatchReminder(ctx, alice, list[0].WatchID, list[0].View.ID, studio.ReminderPatch{Skip: &yes, Source: "notes"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Skipped).To(BeTrue())
	})

	It("keeps reminders out of the trigger list and the trigger limit", func() {
		r := newFlowRig(&flowScript{}, triggers.WatchRule{Offset: -15 * time.Minute})
		r.emit("created", "1", r.sh.clock.Now().Add(26*time.Hour), "", "")
		r.modelRun()
		list, err := r.host.ListTriggers(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		for _, t := range list {
			Expect(t.Kind).NotTo(Equal(triggers.KindReminder))
		}
	})

	It("offers the reminder tools only in the runs of a watch and its reminders", func() {
		r := newFlowRig(&flowScript{}, triggers.WatchRule{Offset: -15 * time.Minute})
		r.emit("created", "1", r.sh.clock.Now().Add(26*time.Hour), "", "")
		r.modelRun()
		plain := r.schedule(studio.TriggerInput{Name: "Plain"})
		r.due()
		run := r.waitEnded(plain.ID)
		Expect(r.h.ToolNames(run.ChatID)).NotTo(ContainElement(studio.SetReminderName))
		watchRun := r.modelRun()
		Expect(r.h.ToolNames(watchRun.ChatID)).To(ContainElements(studio.SetReminderName, studio.CancelReminderName, studio.ListRemindersName))
	})

	It("changes the reminder that another frontend made for the same item meanwhile", func() {
		sh := newShared()
		race := &raceStore{Store: sh.trg}
		sh.trg = race
		r := sh.rig((&flowScript{}).script, studio.TriggerOptions{Sources: notesSource})
		w, err := r.host.CreateTrigger(ctx, alice, studio.TriggerInput{Name: "My notes", Prompt: "Keep me on top of my notes.",
			Kind: triggers.KindEvent, Source: "notes", TZ: "UTC", Model: "m", Enabled: true, Watch: &triggers.WatchRule{Offset: -15 * time.Minute}})
		Expect(err).NotTo(HaveOccurred())
		due := sh.clock.Now().Add(26 * time.Hour)
		race.before = func() {
			other := triggers.Trigger{ID: "other", Owner: w.Owner, Name: "Pay the rent", Prompt: "remind me", Enabled: true, Kind: triggers.KindReminder,
				TZ: "UTC", Since: sh.clock.Now(), Reminder: &triggers.Reminder{ParentID: w.ID, Key: "notes:1", Title: "Pay the rent", Anchor: due,
					Offset: -15 * time.Minute, Task: "remind me"}}
			Expect(race.Store.PutTrigger(ctx, other)).To(Succeed())
		}
		got, err := r.host.SetReminder(ctx, w, studio.ReminderInput{Key: "notes:1", Anchor: due.Format(time.RFC3339), Offset: "-30m"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ID).To(Equal("other"), "the write became a change of the reminder made meanwhile")
		Expect(got.Reminder.Offset).To(Equal(-30 * time.Minute))
		list, err := sh.trg.Reminders(ctx, w.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
	})

	It("refuses a key of another source and a time after the item began", func() {
		r := newFlowRig(&flowScript{watchArgs: `{"key":"mail:1"}`}, triggers.WatchRule{Offset: -15 * time.Minute})
		r.emit("created", "1", r.sh.clock.Now().Add(26*time.Hour), "", "")
		r.modelRun()
		_, err := r.reminder("1")
		Expect(err).To(MatchError(triggers.ErrNotFound))
		w, _ := r.sh.trg.GetTrigger(ctx, r.watch.ID)
		_, err = r.host.SetReminder(ctx, w, studio.ReminderInput{Key: "notes:2", Anchor: r.sh.clock.Now().Add(-time.Minute).Format(time.RFC3339)})
		Expect(err).To(MatchError(ContainSubstring("already started")))
	})

	It("writes no internal words in the lines the owner reads", func() {
		r := newFlowRig(&flowScript{}, triggers.WatchRule{Offset: -15 * time.Minute})
		due := r.sh.clock.Now().Add(26 * time.Hour)
		r.emit("created", "1", due, "", "")
		r.modelRun()
		r.emit("updated", "1", due.Add(time.Hour), "", contract.ClientCredentialPrefix+"phone")
		r.emit("updated", "1", due.Add(time.Hour), "changed", "")
		r.emit("deleted", "1", time.Time{}, "", "")
		var texts []string
		for _, n := range r.notes() {
			texts = append(texts, n.Title, n.Body)
		}
		runs, _ := r.sh.trg.ListRuns(ctx, r.watch.ID, 50)
		for _, run := range runs {
			texts = append(texts, run.Reason)
		}
		for _, t := range texts {
			lower := strings.ToLower(t)
			for _, word := range []string{"key", "payload", "reconcil", "actor", "trigger", "notes:", "event"} {
				Expect(lower).NotTo(ContainSubstring(word), "%q says %q", t, word)
			}
		}
		_ = notify.KindRunEnded
	})
})

var _ = Describe("a watch proposed in a chat", func() {
	ctx := context.Background()

	It("waits as a proposal with its rule, and becomes a watch once approved", func() {
		c := &callScript{}
		r := newShared().rig(c.script, studio.TriggerOptions{Sources: notesSource})
		chat := create(r.svc, alice)
		res := c.call(r, chat, "propose_trigger", `{"name":"My notes","prompt":"Keep me on top of my notes.","kind":"event","source":"notes","watch":{"offset_minutes":-30,"tasks":"prep calls"}}`)
		Expect(res.Err).To(BeNil(), "%+v", res)
		card := lastCard(r, chat)
		v, err := r.host.ProposalStatus(ctx, alice, card.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.Trigger.IsWatch()).To(BeTrue())
		Expect(v.Trigger.Watch.Offset).To(Equal(-30 * time.Minute))
		Expect(v.Trigger.Enabled).To(BeFalse(), "a proposal runs nothing")
		made, err := r.host.ConfirmProposal(ctx, alice, card.ID, "UTC")
		Expect(err).NotTo(HaveOccurred())
		Expect(made.IsWatch()).To(BeTrue())
		Expect(made.Enabled).To(BeTrue())
		res = c.call(r, chat, "propose_trigger", `{"name":"Bad","prompt":"p","when":"daily 08:00","watch":{"offset_minutes":-5}}`)
		Expect(res.Out + errText(res.Err)).To(ContainSubstring("watch is only for kind event"))
	})
})

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
