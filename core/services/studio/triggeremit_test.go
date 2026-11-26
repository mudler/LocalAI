package studio_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The fake source of these tests is "notes": an add-on with items that have
// an id, a name and an optional due time. It is not a calendar, so the tests
// show that a new add-on gets watches and reminders with no host change.

func notesDecl(lifecycle string) contract.EventDeclaration {
	return contract.EventDeclaration{Type: "notes.item." + lifecycle, Lifecycle: lifecycle, Key: "item.id", Title: "item.name",
		Anchor: &contract.EventAnchor{Time: "item.due", Zone: "item.zone", AllDay: "item.allDay"}, Link: "notes?item={key}",
		Label: contract.Localized{Text: "An item changes"}}
}

func notesPayload(id, name string, due time.Time, extra string) []byte {
	m := map[string]any{"id": id, "name": name, "zone": "UTC", "note": extra}
	if !due.IsZero() {
		m["due"] = due.Format(time.RFC3339)
	}
	raw, _ := json.Marshal(map[string]any{"item": m})
	return raw
}

var _ = Describe("ThingOf", func() {
	It("reads the key, title, time, zone and link with the declaration, and nothing else", func() {
		due := time.Date(2026, 11, 26, 14, 0, 0, 0, time.UTC)
		th, err := studio.ThingOf("notes", notesDecl("created"), notesPayload("42", "Write the report", due, "a"))
		Expect(err).NotTo(HaveOccurred())
		Expect(th.Key).To(Equal("notes:42"))
		Expect(th.Title).To(Equal("Write the report"))
		Expect(th.Anchor).To(Equal(due))
		Expect(th.Zone).To(Equal("UTC"))
		Expect(th.Link).To(Equal("/app/addons/notes/notes?item=42"))
		Expect(th.Lifecycle).To(Equal("created"))
	})

	It("reads the later occurrences of a repeating thing, in order, out of the stamp", func() {
		d := notesDecl("updated")
		d.Anchor.Following = "item.later"
		due := time.Date(2026, 11, 26, 14, 0, 0, 0, time.UTC)
		payload := func(later ...string) []byte {
			raw, _ := json.Marshal(map[string]any{"item": map[string]any{"id": "1", "name": "X", "due": due.Format(time.RFC3339), "later": later}})
			return raw
		}
		th, err := studio.ThingOf("notes", d, payload("2026-11-27T14:00:00Z", "not a time", "2026-11-25T14:00:00Z", "2026-11-28T14:00:00Z"))
		Expect(err).NotTo(HaveOccurred())
		Expect(th.Following).To(Equal([]time.Time{due.Add(24 * time.Hour), due.Add(48 * time.Hour)}), "only later times, in order")
		other, _ := studio.ThingOf("notes", d, payload("2026-11-29T14:00:00Z"))
		Expect(other.Stamp).To(Equal(th.Stamp), "the list moves with time; it is not a change of the thing")
	})

	It("gives the same stamp to payloads that differ only in time, and another for any other change", func() {
		a, _ := studio.ThingOf("notes", notesDecl("updated"), notesPayload("1", "X", time.Date(2026, 11, 26, 14, 0, 0, 0, time.UTC), "a"))
		b, _ := studio.ThingOf("notes", notesDecl("updated"), notesPayload("1", "X", time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC), "a"))
		c, _ := studio.ThingOf("notes", notesDecl("updated"), notesPayload("1", "X", time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC), "b"))
		Expect(a.Stamp).To(Equal(b.Stamp))
		Expect(c.Stamp).NotTo(Equal(b.Stamp))
	})

	It("refuses a payload with no id, and keeps a thing with no time", func() {
		_, err := studio.ThingOf("notes", notesDecl("created"), []byte(`{"item":{"name":"x"}}`))
		Expect(err).To(MatchError(studio.ErrEventNotDeclared))
		th, err := studio.ThingOf("notes", notesDecl("created"), notesPayload("1", "x", time.Time{}, ""))
		Expect(err).NotTo(HaveOccurred())
		Expect(th.Anchor.IsZero()).To(BeTrue())
	})
})

// watchRig is a trigger rig with one watch of the notes source.
type watchRig struct {
	*trigRig
	watch triggers.Trigger
}

func newWatchRig(script func() string) *watchRig {
	r := newTrigRig(saySummary)
	w := r.schedule(studio.TriggerInput{Name: "My notes"})
	w.Kind, w.Schedule, w.TZ = triggers.KindEvent, "", "UTC"
	w.Source = "notes"
	w.Watch = &triggers.WatchRule{Offset: -15 * time.Minute}
	Expect(r.sh.trg.PutTrigger(context.Background(), w)).To(Succeed())
	return &watchRig{trigRig: r, watch: w}
}

func (r *watchRig) emit(lifecycle, id, name string, due time.Time, extra, chatID, cred string) error {
	return r.host.Emit(context.Background(), studio.EmitInput{Owner: alice, Addon: "notes", Decl: notesDecl(lifecycle),
		Payload: notesPayload(id, name, due, extra), ChatID: chatID, CredentialID: cred})
}

func (r *watchRig) remind(id string, due time.Time) triggers.Trigger {
	now := r.sh.clock.Now()
	th, err := studio.ThingOf("notes", notesDecl("updated"), notesPayload(id, "Item "+id, due, ""))
	Expect(err).NotTo(HaveOccurred())
	t := triggers.Trigger{ID: "rem-" + id, Owner: alice, Name: "Item " + id, Prompt: "Remind me.", Enabled: true, Kind: triggers.KindReminder,
		Since: now, Model: "m", Reminder: &triggers.Reminder{ParentID: r.watch.ID, Key: "notes:" + id, Title: "Item " + id, Anchor: due,
			Offset: -15 * time.Minute, Task: "remind me", WakeOnChange: true, Stamp: th.Stamp}}
	Expect(r.sh.trg.PutTrigger(context.Background(), t)).To(Succeed())
	return t
}

func (r *watchRig) runsOf(id string) []triggers.Run {
	runs, err := r.sh.trg.ListRuns(context.Background(), id, 100)
	Expect(err).NotTo(HaveOccurred())
	return runs
}

var _ = Describe("events of an add-on", func() {
	ctx := context.Background()

	It("starts the watch's model run for a new thing, with the facts outside the data", func() {
		r := newWatchRig(nil)
		due := r.sh.clock.Now().Add(26 * time.Hour)
		Expect(r.emit("created", "1", "Pay the rent", due, "", "", "")).To(Succeed())
		run := r.waitEnded(r.watch.ID)
		Expect(run.Status).To(Equal(triggers.StatusDone))
		var f map[string]any
		Expect(json.Unmarshal([]byte(run.Payload), &f)).To(Succeed())
		Expect(f["by"]).To(Equal("the owner"))
		Expect(f["key"]).To(Equal("notes:1"))
		Expect(f["title"]).To(Equal("Pay the rent"))
		Expect(f["data"]).NotTo(BeNil())
	})

	It("re-times the reminder when the owner moves the thing, with no model run and one silent line", func() {
		r := newWatchRig(nil)
		due := r.sh.clock.Now().Add(26 * time.Hour)
		r.remind("1", due)
		moved := due.Add(2 * time.Hour)
		Expect(r.emit("updated", "1", "Item 1", moved, "", "", "")).To(Succeed())
		got, err := r.sh.trg.ReminderByKey(ctx, r.watch.ID, "notes:1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Reminder.Anchor).To(Equal(moved))
		runs := r.runsOf(r.watch.ID)
		Expect(runs).To(HaveLen(1))
		Expect(runs[0].Status).To(Equal(triggers.StatusHandled))
		Expect(runs[0].Reason).To(Equal(`Moved the reminder for "Item 1" to Wed 10:45.`))
		ns := r.notes()
		Expect(ns).To(HaveLen(1))
		Expect(ns[0].Title).To(Equal("My notes"))
		Expect(ns[0].Body).To(Equal(runs[0].Reason))
		Expect(ns[0].Link).To(Equal("/app/triggers/" + r.watch.ID))
	})

	It("cancels the reminder when the thing is removed", func() {
		r := newWatchRig(nil)
		r.remind("1", r.sh.clock.Now().Add(26*time.Hour))
		Expect(r.emit("deleted", "1", "Item 1", time.Time{}, "", "", "")).To(Succeed())
		_, err := r.sh.trg.ReminderByKey(ctx, r.watch.ID, "notes:1")
		Expect(err).To(MatchError(triggers.ErrNotFound))
		Expect(r.runsOf(r.watch.ID)[0].Reason).To(ContainSubstring("Cancelled the reminder"))
	})

	It("handles the same event once", func() {
		r := newWatchRig(nil)
		due := r.sh.clock.Now().Add(26 * time.Hour)
		r.remind("1", due)
		in := studio.EmitInput{Owner: alice, Addon: "notes", Decl: notesDecl("updated"), Payload: notesPayload("1", "Item 1", due.Add(time.Hour), ""), DedupeKey: "d1"}
		Expect(r.host.Emit(ctx, in)).To(Succeed())
		Expect(r.host.Emit(ctx, in)).To(Succeed())
		Expect(r.runsOf(r.watch.ID)).To(HaveLen(1))
	})

	It("wakes the reminder early when the thing changes in more than its time", func() {
		r := newWatchRig(nil)
		due := r.sh.clock.Now().Add(26 * time.Hour)
		rem := r.remind("1", due)
		Expect(r.emit("updated", "1", "Item 1", due, "now with a guest", "", "")).To(Succeed())
		run := r.waitEnded(rem.ID)
		Expect(run.DueKey).To(HavePrefix(triggers.EventPrefix))
		Expect(r.runsOf(r.watch.ID)[0].Reason).To(ContainSubstring("changed"))
	})

	It("does not wake the watch on a change made by its own run, and takes the same change from the owner", func() {
		r := newWatchRig(nil)
		due := r.sh.clock.Now().Add(26 * time.Hour)
		Expect(r.emit("created", "1", "Pay the rent", due, "", "", "")).To(Succeed())
		run := r.waitEnded(r.watch.ID)
		rec := r.chatOf(run)
		Expect(rec.Origin).To(Equal(session.OriginTrigger))
		Expect(r.host.ActorOf(ctx, alice, run.ChatID, "")).To(Equal(triggers.Actor{Kind: triggers.ActorAgent, TriggerID: r.watch.ID}))
		Expect(r.emit("created", "2", "Its own note", due, "", run.ChatID, "")).To(Succeed())
		Expect(r.runsOf(r.watch.ID)).To(HaveLen(1), "the watch's own change did not wake it")
		Expect(r.emit("created", "2", "Its own note", due, "x", "", "")).To(Succeed())
		Expect(r.runsOf(r.watch.ID)).To(HaveLen(2))
	})

	It("says who made a change from the live call only", func() {
		r := newWatchRig(nil)
		Expect(r.host.ActorOf(ctx, alice, "", "")).To(Equal(triggers.Actor{Kind: triggers.ActorOwner}))
		Expect(r.host.ActorOf(ctx, alice, "", contract.ClientCredentialPrefix+"k1")).To(Equal(triggers.Actor{Kind: triggers.ActorClient, ClientID: "k1"}))
		Expect(r.host.ActorOf(ctx, alice, "no-such-chat", "").Kind).To(Equal(triggers.ActorAgent), "an unknown chat is not the owner")
	})

	It("reaches only the owner's own watches", func() {
		r := newWatchRig(nil)
		err := r.host.Emit(ctx, studio.EmitInput{Owner: "bob", Addon: "notes", Decl: notesDecl("created"), Payload: notesPayload("1", "x", time.Time{}, "")})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.runsOf(r.watch.ID)).To(BeEmpty())
	})

	It("tells the owner on the watch's channel only when someone else made the change", func() {
		r := newWatchRig(nil)
		due := r.sh.clock.Now().Add(26 * time.Hour)
		r.remind("1", due)
		Expect(r.emit("updated", "1", "Item 1", due.Add(time.Hour), "", "", contract.ClientCredentialPrefix+"k1")).To(Succeed())
		Expect(r.runsOf(r.watch.ID)[0].Status).To(Equal(triggers.StatusHandled))
		ns := r.notes()
		Expect(ns).To(HaveLen(1), "a watch that reports to the Inbox has its one line there")
		Expect(ns[0].Body).To(Equal(`"Item 1" was moved. I'll remind you at Wed 09:45.`), "someone else's change is said as a message")
		Expect(r.runsOf(r.watch.ID)[0].Reason).To(HavePrefix("Moved the reminder"), "the history keeps the plain line")
	})

	It("pauses a watch that changes more than 30 reminders in a minute, and tells the owner", func() {
		r := newWatchRig(nil)
		due := r.sh.clock.Now().Add(26 * time.Hour)
		r.remind("1", due)
		for i := range triggers.MaxChangesPerMinute + 1 {
			Expect(r.emit("updated", "1", "Item 1", due.Add(time.Duration(i+1)*time.Minute), "", "", "")).To(Succeed())
		}
		w, err := r.sh.trg.GetTrigger(ctx, r.watch.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.Enabled).To(BeFalse())
		Expect(r.notes()[0].Title).To(Equal("My notes was switched off"))
		Expect(r.notes()[0].Body).To(ContainSubstring(fmt.Sprintf("more than %d reminders", triggers.MaxChangesPerMinute)))
	})

	It("runs a plain event trigger of the same source as the hook does", func() {
		r := newTrigRig(saySummary)
		t := r.schedule(studio.TriggerInput{})
		t.Kind, t.Schedule, t.TZ, t.Source = triggers.KindEvent, "", "", "notes"
		Expect(r.sh.trg.PutTrigger(ctx, t)).To(Succeed())
		Expect(r.host.Emit(ctx, studio.EmitInput{Owner: alice, Addon: "notes", Decl: notesDecl("created"), Payload: notesPayload("1", "x", time.Time{}, "")})).To(Succeed())
		run := r.waitEnded(t.ID)
		Expect(strings.Contains(run.Payload, `"name":"x"`)).To(BeTrue())
	})

	It("lists a missed reminder in the Inbox, under its watch", func() {
		r := newWatchRig(nil)
		due := r.sh.clock.Now().Add(30 * time.Minute)
		r.remind("1", due)
		r.sh.clock.Advance(time.Hour)
		r.pass()
		ns := r.notes()
		Expect(ns).NotTo(BeEmpty())
		Expect(ns[0].Body).To(HavePrefix("Missed: Item 1."))
		Expect(ns[0].Title).To(Equal("My notes"))
	})
})

var _ = Describe("the stamp of a calendar-shaped item", func() {
	decl := contract.EventDeclaration{Type: "notes.item.updated", Lifecycle: "updated", Key: "event.id", Title: "event.title",
		Anchor: &contract.EventAnchor{Time: "event.next", Zone: "event.zone", AllDay: "event.allDay", Moves: []string{"event.start", "event.end"}},
		Label:  contract.Localized{Text: "x"}}
	payload := func(start, end, next, attendees string) []byte {
		return []byte(fmt.Sprintf(`{"event":{"id":"1","title":"Standup","start":%q,"end":%q,"next":%q,"zone":"Europe/Rome","allDay":false,"attendees":%q}}`, start, end, next, attendees))
	}

	It("is the same after a move of the start, the end and the next time, and differs for new attendees", func() {
		a, err := studio.ThingOf("notes", decl, payload("2026-11-26T09:30", "2026-11-26T10:00", "2026-11-26T09:30:00+01:00", "Sam"))
		Expect(err).NotTo(HaveOccurred())
		b, _ := studio.ThingOf("notes", decl, payload("2026-11-26T11:30", "2026-11-26T12:00", "2026-11-26T11:30:00+01:00", "Sam"))
		c, _ := studio.ThingOf("notes", decl, payload("2026-11-26T11:30", "2026-11-26T12:00", "2026-11-26T11:30:00+01:00", "Sam, Lea"))
		Expect(a.Stamp).To(Equal(b.Stamp), "a move is not a change beyond the time")
		Expect(c.Stamp).NotTo(Equal(b.Stamp))
		Expect(b.Anchor).To(Equal(time.Date(2026, 11, 26, 10, 30, 0, 0, time.UTC)))
	})
})
