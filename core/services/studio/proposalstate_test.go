package studio_test

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// proposed runs one chat that proposes the good proposal and returns the chat
// and the proposal.
func proposed(r *trigRig) (string, triggers.Trigger) {
	ctx := context.Background()
	id := create(r.svc, alice)
	sent(r.svc, alice, id, "propose")
	idle(r.svc, alice, id)
	props, err := r.host.Proposals(ctx, alice)
	Expect(err).NotTo(HaveOccurred())
	Expect(props).To(HaveLen(1), "proposals = %+v, results = %+v", props, r.h.ResultsFor("propose_trigger"))
	return id, props[0]
}

var _ = Describe("proposal answers are kept", func() {
	It("a confirmed proposal says it was accepted, and says so again from the store", func() {
		r := newTrigRig(proposeScript(goodProposal))
		ctx := context.Background()
		_, p := proposed(r)
		v, err := r.host.ProposalStatus(ctx, alice, p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.State).To(Equal(studio.ProposalPending), "view = %+v", v)

		_, err = r.host.ConfirmProposal(ctx, alice, p.ID, "")
		Expect(err).NotTo(HaveOccurred())
		v, err = r.host.ProposalStatus(ctx, alice, p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.State).To(Equal(studio.ProposalAccepted), "view = %+v", v)
		Expect(v.Trigger.Enabled).To(BeTrue(), "view = %+v", v)
		Expect(v.CanUndo).To(BeTrue(), "view = %+v", v)
		Expect(v.At).NotTo(BeZero(), "no answer time: %+v", v)

		// The answer is in the shared store, not in this process.
		raw, err := r.sh.trg.GetTrigger(ctx, p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(raw.Outcome).To(Equal("accepted"), "stored = %+v", raw)

		_, err = r.host.ProposalStatus(ctx, "bob", p.ID)
		Expect(err).To(MatchError(triggers.ErrNotFound), "bob read alice's proposal: %v", err)
	})

	It("a dismissed proposal stays as a declined line, can be restored, and frees its place", func() {
		r := newTrigRig(proposeScript(goodProposal))
		ctx := context.Background()
		_, p := proposed(r)
		Expect(r.host.DismissProposal(ctx, alice, p.ID)).To(Succeed())
		v, err := r.host.ProposalStatus(ctx, alice, p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.State).To(Equal(studio.ProposalDeclined), "view = %+v", v)
		props, _ := r.host.Proposals(ctx, alice)
		Expect(props).To(BeEmpty(), "a declined proposal still waits: %+v", props)
		Expect(r.host.HasProposal(ctx, alice, p.SourceChatID)).To(BeFalse())
		list, _ := r.host.ListTriggers(ctx, alice)
		Expect(list).To(BeEmpty(), "%+v", list)
		_, err = r.host.ConfirmProposal(ctx, alice, p.ID, "")
		Expect(err).To(MatchError(studio.ErrProposalAnswered), "a declined proposal was confirmed: %v", err)

		// The model proposing the same thing again gets a new proposal that waits.
		again, err := r.host.ProposeTrigger(ctx, alice, studio.TriggerInput{Name: p.Name, Prompt: p.Prompt, Kind: p.Kind, Schedule: p.Schedule, TZ: p.TZ, Model: p.Model, SourceChatID: p.SourceChatID})
		Expect(err).NotTo(HaveOccurred())
		Expect(again.ID).NotTo(Equal(p.ID), "the declined proposal came back")

		By("restoring the declined one")
		back, err := r.host.ReopenProposal(ctx, alice, p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(back.Outcome).To(BeEmpty())
		v, _ = r.host.ProposalStatus(ctx, alice, p.ID)
		Expect(v.State).To(Equal(studio.ProposalPending), "view = %+v", v)
	})

	It("undo removes the trigger, keeps the receipt, and the proposal can be scheduled again", func() {
		r := newTrigRig(proposeScript(goodProposal))
		ctx := context.Background()
		_, p := proposed(r)
		_, err := r.host.ConfirmProposal(ctx, alice, p.ID, "")
		Expect(err).NotTo(HaveOccurred())

		undone, err := r.host.UndoProposal(ctx, alice, p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(undone.Enabled).To(BeFalse(), "undone = %+v", undone)
		v, _ := r.host.ProposalStatus(ctx, alice, p.ID)
		Expect(v.State).To(Equal(studio.ProposalUndone), "view = %+v", v)
		list, _ := r.host.ListTriggers(ctx, alice)
		Expect(list).To(BeEmpty(), "the trigger is still listed: %+v", list)
		props, _ := r.host.Proposals(ctx, alice)
		Expect(props).To(BeEmpty(), "%+v", props)
		_, err = r.host.GetTrigger(ctx, alice, p.ID)
		Expect(err).To(MatchError(triggers.ErrNotFound))
		r.sh.clock.Advance(48 * time.Hour)
		r.pass()
		runs, _ := r.sh.trg.ListRuns(ctx, p.ID, 0)
		Expect(runs).To(BeEmpty(), "an undone trigger ran: %+v", runs)
		_, err = r.host.UndoProposal(ctx, alice, p.ID)
		Expect(err).To(MatchError(studio.ErrProposalAnswered), "undone twice: %v", err)

		By("scheduling it again")
		back, err := r.host.ReopenProposal(ctx, alice, p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(back.Enabled).To(BeFalse())
		props, _ = r.host.Proposals(ctx, alice)
		Expect(props).To(HaveLen(1), "%+v", props)
		_, err = r.host.ConfirmProposal(ctx, alice, p.ID, "")
		Expect(err).NotTo(HaveOccurred())
		list, _ = r.host.ListTriggers(ctx, alice)
		Expect(list).To(HaveLen(1), "%+v", list)
	})

	It("undo is refused once the trigger ran", func() {
		r := newTrigRig(proposeScript(`{"name":"Digest","prompt":"p","when":"daily 08:00","time_zone":"UTC"}`))
		ctx := context.Background()
		_, p := proposed(r)
		_, err := r.host.ConfirmProposal(ctx, alice, p.ID, "")
		Expect(err).NotTo(HaveOccurred())
		r.due()
		r.waitEnded(p.ID)

		v, _ := r.host.ProposalStatus(ctx, alice, p.ID)
		Expect(v.CanUndo).To(BeFalse(), "view = %+v", v)
		Expect(v.UndoBlocked).To(Equal(studio.UndoRan), "view = %+v", v)
		_, err = r.host.UndoProposal(ctx, alice, p.ID)
		Expect(err).To(MatchError(studio.ErrCannotUndo))
		Expect(err.Error()).To(ContainSubstring("already ran"))
		got, err := r.host.GetTrigger(ctx, alice, p.ID)
		Expect(err).NotTo(HaveOccurred(), "the refused undo removed the trigger")
		Expect(got.Enabled).To(BeTrue())
	})

	It("undo is refused once the person edited or switched the trigger", func() {
		r := newTrigRig(proposeScript(goodProposal))
		ctx := context.Background()
		_, p := proposed(r)
		_, err := r.host.ConfirmProposal(ctx, alice, p.ID, "")
		Expect(err).NotTo(HaveOccurred())
		_, err = r.host.UpdateTrigger(ctx, alice, p.ID, studio.TriggerInput{Name: "Mine", Prompt: p.Prompt, Kind: p.Kind, Schedule: "daily 10:00", TZ: "Europe/Rome", Model: p.Model, Enabled: true})
		Expect(err).NotTo(HaveOccurred())
		v, _ := r.host.ProposalStatus(ctx, alice, p.ID)
		Expect(v.State).To(Equal(studio.ProposalAccepted), "an edit lost the receipt: %+v", v)
		Expect(v.CanUndo).To(BeFalse(), "view = %+v", v)
		Expect(v.UndoBlocked).To(Equal(studio.UndoEdited), "view = %+v", v)
		_, err = r.host.UndoProposal(ctx, alice, p.ID)
		Expect(err).To(MatchError(studio.ErrCannotUndo))

		_, p2 := proposedAgain(r)
		_, err = r.host.ConfirmProposal(ctx, alice, p2.ID, "")
		Expect(err).NotTo(HaveOccurred())
		_, err = r.host.SetTriggerEnabled(ctx, alice, p2.ID, false)
		Expect(err).NotTo(HaveOccurred())
		_, err = r.host.UndoProposal(ctx, alice, p2.ID)
		Expect(err).To(MatchError(studio.ErrCannotUndo), "a switched trigger was undone")
	})

	It("a trigger removed on the Triggers page leaves no answer", func() {
		r := newTrigRig(proposeScript(goodProposal))
		ctx := context.Background()
		_, p := proposed(r)
		_, err := r.host.ConfirmProposal(ctx, alice, p.ID, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.host.DeleteTrigger(ctx, alice, p.ID)).To(Succeed())
		_, err = r.host.ProposalStatus(ctx, alice, p.ID)
		Expect(err).To(MatchError(triggers.ErrNotFound), "%v", err)
		_, err = r.host.UndoProposal(ctx, alice, p.ID)
		Expect(err).To(MatchError(triggers.ErrNotFound), "%v", err)
	})

	It("answers do not count against the limits", func() {
		r := newTrigRig(proposeScript(goodProposal))
		ctx := context.Background()
		_, p := proposed(r)
		for i := range 12 {
			t, err := r.host.ProposeTrigger(ctx, alice, studio.TriggerInput{Name: "Other", Prompt: string(rune('a' + i)), Kind: p.Kind, Schedule: p.Schedule, TZ: p.TZ, Model: p.Model, SourceChatID: p.SourceChatID})
			Expect(err).NotTo(HaveOccurred(), "proposal %d: %v", i, err)
			Expect(r.host.DismissProposal(ctx, alice, t.ID)).To(Succeed())
		}
	})
})

var _ = Describe("the model learns what happened to its proposal", func() {
	It("the next turn names the accepted, declined and undone proposals", func() {
		r := newTrigRig(proposeScript(goodProposal))
		ctx := context.Background()
		chat, p := proposed(r)
		turn := func() string {
			sent(r.svc, alice, chat, "hello")
			idle(r.svc, alice, chat)
			s := r.h.Sent(chat)
			return s[len(s)-1]
		}
		Expect(turn()).NotTo(ContainSubstring("Morning digest"), "nothing was answered yet")

		_, err := r.host.ConfirmProposal(ctx, alice, p.ID, "")
		Expect(err).NotTo(HaveOccurred())
		got := turn()
		Expect(got).To(ContainSubstring(`"Morning digest"`))
		Expect(got).To(ContainSubstring("accepted"))

		_, err = r.host.UndoProposal(ctx, alice, p.ID)
		Expect(err).NotTo(HaveOccurred())
		got = turn()
		Expect(got).To(ContainSubstring("undone"), "turn = %q", got)
		Expect(got).NotTo(ContainSubstring("accepted"), "turn = %q", got)

		Expect(r.host.DismissProposal(ctx, alice, p.ID)).To(MatchError(studio.ErrProposalAnswered), "an undone proposal was dismissed")
		back, err := r.host.ReopenProposal(ctx, alice, p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.host.DismissProposal(ctx, alice, back.ID)).To(Succeed())
		got = turn()
		Expect(got).To(ContainSubstring("declined"), "turn = %q", got)
	})
})

// proposedAgain makes a second proposal in a new chat.
func proposedAgain(r *trigRig) (string, triggers.Trigger) {
	t, err := r.host.ProposeTrigger(context.Background(), alice, studio.TriggerInput{Name: "Second", Prompt: "other", Kind: triggers.KindSchedule, Schedule: "daily 09:00", TZ: "UTC", Model: "src-model"})
	Expect(err).NotTo(HaveOccurred())
	return "", t
}
