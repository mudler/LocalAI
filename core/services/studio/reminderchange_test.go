package studio_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

var _ = Describe("a reminder changed by asking in a chat", func() {
	var (
		ctx  context.Context
		c    *callScript
		r    *trigRig
		w    triggers.Trigger
		rem  triggers.Trigger
		due  time.Time
		chat string
	)

	BeforeEach(func() {
		ctx = context.Background()
		c = &callScript{}
		r = newShared().rig(c.script, studio.TriggerOptions{Sources: notesSource})
		var err error
		w, err = r.host.CreateTrigger(ctx, alice, studio.TriggerInput{Name: "My notes", Prompt: "Keep me on top of my notes.",
			Kind: triggers.KindEvent, Source: "notes", TZ: "UTC", Model: "m", Enabled: true, Watch: &triggers.WatchRule{Offset: -15 * time.Minute}})
		Expect(err).NotTo(HaveOccurred())
		due = r.sh.clock.Now().Add(26 * time.Hour)
		rem, err = r.host.SetReminder(ctx, w, studio.ReminderInput{Key: "notes:1", Title: "Pay the rent", Anchor: due.Format(time.RFC3339)})
		Expect(err).NotTo(HaveOccurred())
		chat = create(r.svc, alice)
	})

	current := func() triggers.Trigger {
		got, err := r.sh.trg.GetTrigger(ctx, rem.ID)
		Expect(err).NotTo(HaveOccurred())
		return got
	}

	It("offers the tools only to a person who has a watch", func() {
		other := newShared().rig(c.script, studio.TriggerOptions{Sources: notesSource})
		plain := create(other.svc, alice)
		c.call(other, plain, "list_triggers", `{}`)
		Expect(other.h.ToolNames(plain)).NotTo(ContainElement(studio.SetReminderName))
		c.call(r, chat, studio.ListRemindersName, `{}`)
		Expect(r.h.ToolNames(chat)).To(ContainElements(studio.SetReminderName, studio.CancelReminderName, studio.ListRemindersName))
		res := r.h.ResultsFor(studio.ListRemindersName)
		Expect(res[len(res)-1].Out).To(ContainSubstring("Pay the rent (key notes:1)"))
		Expect(res[len(res)-1].Out).To(ContainSubstring(studio.TriggerRef(w.ID)))
	})

	It("proposes a new time, applies it on Approve and undoes it", func() {
		res := c.call(r, chat, studio.SetReminderName, `{"key":"notes:1","offset":"-30m"}`)
		Expect(res.Err).To(BeNil())
		Expect(res.Out).To(ContainSubstring("Nothing changes until the user presses Approve"))
		Expect(current().Reminder.Offset).To(Equal(-15*time.Minute), "nothing changes before the answer")
		card := lastCard(r, chat)
		Expect(card.Kind).To(Equal(studio.ChangeCardKind))

		v, err := r.host.ChangeStatus(ctx, alice, card.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.State).To(Equal(studio.ChangePending))
		Expect(v.Op).To(Equal(triggers.ChangeUpdate))
		Expect(v.Reminder).NotTo(BeNil())
		Expect(v.Reminder.WatchID).To(Equal(w.ID))
		Expect(v.Reminder.Before.Due).To(Equal(due.Add(-15 * time.Minute)))
		Expect(v.Reminder.After.Due).To(Equal(due.Add(-30 * time.Minute)))
		Expect(v.Stale).To(BeFalse())

		list, _ := r.host.Reminders(ctx, alice, w.ID)
		Expect(list).To(HaveLen(1), "the waiting change is not a reminder of the watch")

		_, err = r.host.ApplyChange(ctx, alice, card.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(current().Reminder.Offset).To(Equal(-30 * time.Minute))
		v, _ = r.host.ChangeStatus(ctx, alice, card.ID)
		Expect(v.State).To(Equal(studio.ChangeApplied))
		Expect(v.CanUndo).To(BeTrue())
		Expect(v.Reminder.Before.Due).To(Equal(due.Add(-15 * time.Minute)))
		Expect(v.Reminder.After.Due).To(Equal(due.Add(-30 * time.Minute)))

		_, err = r.host.UndoChange(ctx, alice, card.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(current().Reminder.Offset).To(Equal(-15 * time.Minute))
		v, _ = r.host.ChangeStatus(ctx, alice, card.ID)
		Expect(v.State).To(Equal(studio.ChangeUndone))
		Expect(r.host.ProposalNote(ctx, alice, chat)).To(ContainSubstring(`reminder for "Pay the rent" undone by the user`))
	})

	It("leaves the reminder as it is on Not now", func() {
		res := c.call(r, chat, studio.CancelReminderName, `{"key":"notes:1"}`)
		Expect(res.Err).To(BeNil())
		card := lastCard(r, chat)
		Expect(r.host.DismissChange(ctx, alice, card.ID)).To(Succeed())
		Expect(current().Reminder.Key).To(Equal("notes:1"))
		v, _ := r.host.ChangeStatus(ctx, alice, card.ID)
		Expect(v.State).To(Equal(studio.ChangeDismissed))
		_, err := r.host.ApplyChange(ctx, alice, card.ID)
		Expect(err).To(MatchError(studio.ErrProposalAnswered))
	})

	It("cancels on Approve, and Undo sets the same reminder again", func() {
		c.call(r, chat, studio.CancelReminderName, `{"key":"notes:1"}`)
		card := lastCard(r, chat)
		v, _ := r.host.ChangeStatus(ctx, alice, card.ID)
		Expect(v.Op).To(Equal(triggers.ChangeDelete))
		Expect(v.Reminder.After).To(BeNil())
		_, err := r.host.ApplyChange(ctx, alice, card.ID)
		Expect(err).NotTo(HaveOccurred())
		_, err = r.sh.trg.ReminderByKey(ctx, w.ID, "notes:1")
		Expect(err).To(MatchError(triggers.ErrNotFound))
		_, err = r.host.UndoChange(ctx, alice, card.ID)
		Expect(err).NotTo(HaveOccurred())
		back, err := r.sh.trg.ReminderByKey(ctx, w.ID, "notes:1")
		Expect(err).NotTo(HaveOccurred())
		Expect(back.ID).To(Equal(rem.ID))
		Expect(back.Reminder.Anchor).To(Equal(due))
	})

	It("sets a reminder for an item that has none, and Undo cancels it", func() {
		later := due.Add(48 * time.Hour)
		res := c.call(r, chat, studio.SetReminderName, `{"key":"notes:2","title":"Call the bank","anchor":"`+later.Format(time.RFC3339)+`","offset":"-1h","task":"prepare a short brief"}`)
		Expect(res.Err).To(BeNil())
		card := lastCard(r, chat)
		v, _ := r.host.ChangeStatus(ctx, alice, card.ID)
		Expect(v.Op).To(Equal(triggers.ChangeCreate))
		Expect(v.Reminder.Before).To(BeNil())
		Expect(v.Reminder.After.Task).To(Equal("prepare a short brief"))
		_, err := r.sh.trg.ReminderByKey(ctx, w.ID, "notes:2")
		Expect(err).To(MatchError(triggers.ErrNotFound), "a proposal sets nothing")
		_, err = r.host.ApplyChange(ctx, alice, card.ID)
		Expect(err).NotTo(HaveOccurred())
		made, err := r.sh.trg.ReminderByKey(ctx, w.ID, "notes:2")
		Expect(err).NotTo(HaveOccurred())
		Expect(made.Reminder.Offset).To(Equal(-time.Hour))
		Expect(made.Enabled).To(BeTrue())
		_, err = r.host.UndoChange(ctx, alice, card.ID)
		Expect(err).NotTo(HaveOccurred())
		_, err = r.sh.trg.ReminderByKey(ctx, w.ID, "notes:2")
		Expect(err).To(MatchError(triggers.ErrNotFound))
	})

	It("refuses a change whose reminder was changed meanwhile", func() {
		c.call(r, chat, studio.SetReminderName, `{"key":"notes:1","offset":"-30m"}`)
		card := lastCard(r, chat)
		yes := true
		_, err := r.host.PatchReminder(ctx, alice, w.ID, rem.ID, studio.ReminderPatch{Skip: &yes})
		Expect(err).NotTo(HaveOccurred())
		v, _ := r.host.ChangeStatus(ctx, alice, card.ID)
		Expect(v.Stale).To(BeTrue())
		_, err = r.host.ApplyChange(ctx, alice, card.ID)
		Expect(err).To(MatchError(studio.ErrChangeStale))
		Expect(current().Reminder.Offset).To(Equal(-15 * time.Minute))
	})

	It("asks for the watch when the person has several", func() {
		_, err := r.host.CreateTrigger(ctx, alice, studio.TriggerInput{Name: "Other notes", Prompt: "p",
			Kind: triggers.KindEvent, Source: "notes", TZ: "UTC", Model: "m", Enabled: true, Watch: &triggers.WatchRule{Offset: -5 * time.Minute}})
		Expect(err).NotTo(HaveOccurred())
		res := c.call(r, chat, studio.SetReminderName, `{"key":"notes:1","offset":"-30m"}`)
		Expect(errText(res.Err) + res.Out).To(ContainSubstring("give watch"))
		res = c.call(r, chat, studio.SetReminderName, `{"watch":"My notes","key":"notes:1","offset":"-30m"}`)
		Expect(res.Err).To(BeNil())
		Expect(res.Out).To(ContainSubstring("Asked to set the reminder"))
	})
})
