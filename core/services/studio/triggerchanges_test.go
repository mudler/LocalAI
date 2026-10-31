package studio_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// callScript runs the tool call that the test names before it sends "call".
type callScript struct {
	mu         sync.Mutex
	tool, args string
}

func (c *callScript) set(tool, args string) {
	c.mu.Lock()
	c.tool, c.args = tool, args
	c.mu.Unlock()
}

func (c *callScript) script(_ workspace.Actor, text string) []studiotest.ToolStep {
	// A system note may come before the word.
	if !strings.HasSuffix(strings.TrimSpace(text), "call") {
		return []studiotest.ToolStep{{Say: "ok"}}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return []studiotest.ToolStep{{Tool: c.tool, Args: c.args}, {Say: "ok"}}
}

// call runs one tool call in the chat and returns its result.
func (c *callScript) call(r *trigRig, chat, tool, args string) studiotest.ToolResult {
	c.set(tool, args)
	before := len(r.h.ResultsFor(tool))
	sent(r.svc, alice, chat, "call")
	idle(r.svc, alice, chat)
	res := r.h.ResultsFor(tool)
	Expect(res).To(HaveLen(before+1), "results of %s = %+v, sent %v, all %+v", tool, res, r.h.Sent(chat), r.h.Results())
	return res[before]
}

// lastCard is the card of the newest tool line of the chat.
func lastCard(r *trigRig, chat string) *session.ToolCard {
	tools := toolEntries(r.svc, alice, chat)
	Expect(tools).NotTo(BeEmpty())
	return tools[len(tools)-1].Card
}

var _ = Describe("trigger changes from a chat", func() {
	var (
		r    *trigRig
		c    *callScript
		ctx  context.Context
		chat string
		tr   triggers.Trigger
		ref  string
	)
	BeforeEach(func() {
		c = &callScript{}
		r = newTrigRig(c.script)
		ctx = context.Background()
		tr = r.schedule(studio.TriggerInput{Name: "Nightly", Prompt: "secret instructions", Reads: "Mail", EstTokens: 900})
		chat = create(r.svc, alice)
		ref = studio.TriggerRef(tr.ID)
	})

	It("lists a ref for each trigger, and the prompt of one trigger only when asked", func() {
		res := c.call(r, chat, "list_triggers", `{}`)
		Expect(res.Err).To(BeNil(), "%+v", res)
		Expect(res.Out).To(ContainSubstring(ref))
		Expect(res.Out).NotTo(ContainSubstring(tr.ID), "the raw id leaked")
		Expect(res.Out).NotTo(ContainSubstring("secret instructions"))

		res = c.call(r, chat, "list_triggers", fmt.Sprintf(`{"ref":%q,"include_prompt":true}`, ref))
		Expect(res.Err).To(BeNil(), "%+v", res)
		var got struct {
			Triggers []struct {
				Ref    string `json:"ref"`
				Detail struct {
					Prompt   string `json:"prompt"`
					Schedule string `json:"schedule"`
				} `json:"detail"`
			} `json:"triggers"`
		}
		Expect(json.Unmarshal([]byte(res.Out), &got)).To(Succeed())
		Expect(got.Triggers).To(HaveLen(1))
		Expect(got.Triggers[0].Detail.Prompt).To(Equal("secret instructions"))
		Expect(got.Triggers[0].Detail.Schedule).To(Equal("daily 08:00"))

		res = c.call(r, chat, "list_triggers", `{"include_prompt":true}`)
		Expect(res.Err).To(MatchError(ContainSubstring("needs the ref")))
	})

	It("an edit waits for Apply, applies, and Undo puts the trigger back", func() {
		res := c.call(r, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"name":"Nightly digest","when":"daily 07:30"}`, ref))
		Expect(res.Err).To(BeNil(), "%+v", res)
		Expect(res.Out).To(ContainSubstring("Nothing is changed yet"))
		card := lastCard(r, chat)
		Expect(card).NotTo(BeNil())
		Expect(card.Kind).To(Equal(studio.ChangeCardKind))

		live, _ := r.host.GetTrigger(ctx, alice, tr.ID)
		Expect(live.Name).To(Equal("Nightly"), "the edit applied before the person answered")
		list, _ := r.host.ListTriggers(ctx, alice)
		Expect(list).To(HaveLen(1), "a change record is listed as a trigger: %+v", list)
		props, _ := r.host.Proposals(ctx, alice)
		Expect(props).To(BeEmpty(), "a change record is listed as a proposal: %+v", props)
		Expect(r.host.HasProposal(ctx, alice, chat)).To(BeTrue(), "a waiting change keeps the chat busy")

		v, err := r.host.ChangeStatus(ctx, alice, card.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.State).To(Equal(studio.ChangePending))
		Expect(v.Op).To(Equal(triggers.ChangeUpdate))
		Expect(v.Fields).To(Equal([]string{"name", "when"}))
		Expect(v.Before.Name).To(Equal("Nightly"))
		Expect(v.After.Name).To(Equal("Nightly digest"))
		Expect(v.Stale).To(BeFalse())

		// Bob sees nothing of it.
		_, err = r.host.ChangeStatus(ctx, "bob", card.ID)
		Expect(err).To(MatchError(triggers.ErrNotFound))
		_, err = r.host.ApplyChange(ctx, "bob", card.ID)
		Expect(err).To(MatchError(triggers.ErrNotFound))
		// A change record is not a proposal.
		_, err = r.host.ConfirmProposal(ctx, alice, card.ID, "")
		Expect(err).To(MatchError(triggers.ErrNotFound))

		got, err := r.host.ApplyChange(ctx, alice, card.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ID).To(Equal(tr.ID))
		Expect(got.Name).To(Equal("Nightly digest"))
		Expect(got.Schedule).To(Equal("daily 07:30"))
		Expect(got.Prompt).To(Equal("secret instructions"), "a field the change did not name was lost")
		Expect(got.Reads).To(Equal("Mail"))
		Expect(got.Enabled).To(BeTrue())
		_, err = r.host.ApplyChange(ctx, alice, card.ID)
		Expect(err).To(MatchError(studio.ErrProposalAnswered))

		v, _ = r.host.ChangeStatus(ctx, alice, card.ID)
		Expect(v.State).To(Equal(studio.ChangeApplied))
		Expect(v.CanUndo).To(BeTrue(), "%+v", v)
		Expect(v.Before.Name).To(Equal("Nightly"))
		Expect(v.After.Name).To(Equal("Nightly digest"))

		Expect(r.svc.Triggers().ProposalNote(ctx, alice, chat)).To(ContainSubstring("applied by the user"))

		back, err := r.host.UndoChange(ctx, alice, card.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(back.Name).To(Equal("Nightly"))
		Expect(back.Schedule).To(Equal("daily 08:00"))
		v, _ = r.host.ChangeStatus(ctx, alice, card.ID)
		Expect(v.State).To(Equal(studio.ChangeUndone))
	})

	It("a dismissed edit leaves the trigger as it was", func() {
		c.call(r, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"prompt":"Other work."}`, ref))
		card := lastCard(r, chat)
		Expect(r.host.DismissChange(ctx, alice, card.ID)).To(Succeed())
		Expect(r.host.DismissChange(ctx, alice, card.ID)).To(Succeed(), "dismissing twice")
		_, err := r.host.ApplyChange(ctx, alice, card.ID)
		Expect(err).To(MatchError(studio.ErrProposalAnswered))
		live, _ := r.host.GetTrigger(ctx, alice, tr.ID)
		Expect(live.Prompt).To(Equal("secret instructions"))
		Expect(r.svc.Triggers().ProposalNote(ctx, alice, chat)).To(ContainSubstring("dismissed by the user"))
	})

	It("an edit the person overtook on the Triggers page does not apply", func() {
		c.call(r, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"name":"Renamed"}`, ref))
		card := lastCard(r, chat)
		_, err := r.host.SetTriggerEnabled(ctx, alice, tr.ID, false)
		Expect(err).NotTo(HaveOccurred())
		v, _ := r.host.ChangeStatus(ctx, alice, card.ID)
		Expect(v.Stale).To(BeTrue())
		_, err = r.host.ApplyChange(ctx, alice, card.ID)
		Expect(err).To(MatchError(studio.ErrChangeStale))
		live, _ := r.host.GetTrigger(ctx, alice, tr.ID)
		Expect(live.Name).To(Equal("Nightly"))
	})

	It("an edit is checked like a proposal", func() {
		res := c.call(r, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"when":"*/15 * * * *"}`, ref))
		Expect(res.Err).To(MatchError(ContainSubstring("runs at most once an hour")))
		res = c.call(r, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"writes":"../bob/x.json"}`, ref))
		Expect(res.Err).To(MatchError(ContainSubstring("writes is not a path in your files")))
		res = c.call(r, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"when":{"file_arrives_in":"/Nowhere"}}`, ref))
		Expect(res.Err).NotTo(BeNil(), "%+v", res)
		res = c.call(r, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"name":"Nightly"}`, ref))
		Expect(res.Err).To(MatchError(ContainSubstring("nothing changes")))
		res = c.call(r, chat, "update_trigger", `{"ref":"t00000000","name":"x"}`)
		Expect(res.Err).To(MatchError(ContainSubstring("no trigger of yours")))
		Expect(lastCard(r, chat)).To(BeNil(), "a refused change has a card")

		// A schedule the person chose on the page stays possible to rename.
		fast := r.schedule(studio.TriggerInput{Name: "Fast", Kind: triggers.KindSchedule, Schedule: "*/15 * * * *", TZ: "UTC"})
		res = c.call(r, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"name":"Fast one"}`, studio.TriggerRef(fast.ID)))
		Expect(res.Err).To(BeNil(), "%+v", res)
	})

	It("a pause applies at once with a receipt, and Undo resumes", func() {
		res := c.call(r, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"enabled":false}`, ref))
		Expect(res.Err).To(BeNil(), "%+v", res)
		Expect(res.Out).To(ContainSubstring("paused"))
		live, _ := r.host.GetTrigger(ctx, alice, tr.ID)
		Expect(live.Enabled).To(BeFalse())
		card := lastCard(r, chat)
		Expect(card.Kind).To(Equal(studio.ChangeCardKind))
		v, _ := r.host.ChangeStatus(ctx, alice, card.ID)
		Expect(v.State).To(Equal(studio.ChangeApplied))
		Expect(v.Op).To(Equal(triggers.ChangePause))
		Expect(v.Fields).To(Equal([]string{"enabled"}))
		Expect(v.CanUndo).To(BeTrue())

		_, err := r.host.UndoChange(ctx, alice, card.ID)
		Expect(err).NotTo(HaveOccurred())
		live, _ = r.host.GetTrigger(ctx, alice, tr.ID)
		Expect(live.Enabled).To(BeTrue())
		_, err = r.host.UndoChange(ctx, alice, card.ID)
		Expect(err).To(MatchError(studio.ErrProposalAnswered))
	})

	It("Undo is refused after a day, or once the trigger changed", func() {
		c.call(r, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"enabled":false}`, ref))
		pause := lastCard(r, chat)
		_, err := r.host.SetTriggerEnabled(ctx, alice, tr.ID, true)
		Expect(err).NotTo(HaveOccurred())
		v, _ := r.host.ChangeStatus(ctx, alice, pause.ID)
		Expect(v.CanUndo).To(BeFalse())
		Expect(v.UndoBlocked).To(Equal(studio.UndoEdited))
		_, err = r.host.UndoChange(ctx, alice, pause.ID)
		Expect(err).To(MatchError(studio.ErrCannotUndo))

		// By its name this time.
		c.call(r, chat, "update_trigger", `{"ref":"Nightly","enabled":false}`)
		again := lastCard(r, chat)
		r.sh.clock.Advance(studio.ChangeUndoWindow + time.Minute)
		v, _ = r.host.ChangeStatus(ctx, alice, again.ID)
		Expect(v.UndoBlocked).To(Equal(studio.UndoExpired))
		_, err = r.host.UndoChange(ctx, alice, again.ID)
		Expect(err).To(MatchError(studio.ErrCannotUndo))
	})

	It("a delete waits, deletes on Apply, and Undo restores the trigger with its id", func() {
		res := c.call(r, chat, "delete_trigger", fmt.Sprintf(`{"ref":%q}`, ref))
		Expect(res.Err).To(BeNil(), "%+v", res)
		Expect(res.Out).To(ContainSubstring("It is not deleted"))
		card := lastCard(r, chat)
		_, err := r.host.GetTrigger(ctx, alice, tr.ID)
		Expect(err).NotTo(HaveOccurred(), "deleted before the person answered")
		v, _ := r.host.ChangeStatus(ctx, alice, card.ID)
		Expect(v.Op).To(Equal(triggers.ChangeDelete))
		Expect(v.HasAfter).To(BeFalse())
		Expect(v.Before.Name).To(Equal("Nightly"))

		_, err = r.host.ApplyChange(ctx, alice, card.ID)
		Expect(err).NotTo(HaveOccurred())
		_, err = r.host.GetTrigger(ctx, alice, tr.ID)
		Expect(err).To(MatchError(triggers.ErrNotFound))
		v, _ = r.host.ChangeStatus(ctx, alice, card.ID)
		Expect(v.State).To(Equal(studio.ChangeApplied))
		Expect(v.CanUndo).To(BeTrue(), "%+v", v)
		Expect(r.svc.Triggers().ProposalNote(ctx, alice, chat)).To(ContainSubstring("deleted by the user"))

		back, err := r.host.UndoChange(ctx, alice, card.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(back.ID).To(Equal(tr.ID))
		got, err := r.host.GetTrigger(ctx, alice, tr.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Prompt).To(Equal("secret instructions"))
		Expect(got.Enabled).To(BeTrue())
		Expect(studio.TriggerRef(got.ID)).To(Equal(ref), "the ref changed")
	})

	It("Keep leaves the trigger, and the refs of another user are unknown", func() {
		c.call(r, chat, "delete_trigger", fmt.Sprintf(`{"ref":%q}`, ref))
		card := lastCard(r, chat)
		Expect(r.host.DismissChange(ctx, alice, card.ID)).To(Succeed())
		_, err := r.host.GetTrigger(ctx, alice, tr.ID)
		Expect(err).NotTo(HaveOccurred())

		_, err = r.host.ResolveTrigger(ctx, "bob", ref)
		Expect(err).To(MatchError(studio.ErrUnknownTrigger))
		_, err = r.host.ProposeDelete(ctx, "bob", "x", tr)
		Expect(err).To(MatchError(triggers.ErrNotFound))
	})

	It("finds a trigger by its name when the model gives the name", func() {
		got, err := r.host.ResolveTrigger(ctx, alice, "nightly")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ID).To(Equal(tr.ID))
	})
})

var _ = Describe("trigger changes: who gets the tools", func() {
	It("only a person's chat gets update_trigger and delete_trigger", func() {
		r := newTrigRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "go" {
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"child job","wait":true}`}, {Say: "done"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		})
		id := create(r.svc, alice)
		sent(r.svc, alice, id, "go")
		idle(r.svc, alice, id)
		Expect(r.h.ToolNames(id)).To(ContainElements("update_trigger", "delete_trigger"))
		for _, ch := range childrenOf(r.svc, alice, id) {
			Expect(r.h.ToolNames(ch.ID)).NotTo(ContainElements("update_trigger"), "a sub-agent")
			Expect(r.h.ToolNames(ch.ID)).NotTo(ContainElements("delete_trigger"), "a sub-agent")
		}
		tr := r.schedule(studio.TriggerInput{})
		r.due()
		run := r.waitEnded(tr.ID)
		Expect(r.h.ToolNames(run.ChatID)).NotTo(ContainElement("update_trigger"), "an unattended run")
		Expect(r.h.ToolNames(run.ChatID)).NotTo(ContainElement("delete_trigger"), "an unattended run")
	})
})

var _ = Describe("trigger changes on two frontends", func() {
	It("a change asked on one frontend is applied and undone on the other", func() {
		sh := newShared()
		c := &callScript{}
		a := sh.rig(c.script, studio.TriggerOptions{})
		b := sh.rig(saySummary, studio.TriggerOptions{})
		ctx := context.Background()
		tr := a.schedule(studio.TriggerInput{Name: "Nightly"})
		chat := create(a.svc, alice)
		c.call(a, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"name":"Renamed"}`, studio.TriggerRef(tr.ID)))
		card := lastCard(a, chat)

		v, err := b.host.ChangeStatus(ctx, alice, card.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.State).To(Equal(studio.ChangePending))
		_, err = b.host.ApplyChange(ctx, alice, card.ID)
		Expect(err).NotTo(HaveOccurred())
		v, _ = a.host.ChangeStatus(ctx, alice, card.ID)
		Expect(v.State).To(Equal(studio.ChangeApplied))
		_, err = a.host.UndoChange(ctx, alice, card.ID)
		Expect(err).NotTo(HaveOccurred())
		got, _ := b.host.GetTrigger(ctx, alice, tr.ID)
		Expect(got.Name).To(Equal("Nightly"))
	})
})

var _ = Describe("trigger changes of a file trigger", func() {
	It("keeps the folder when only the name changes", func() {
		c := &callScript{}
		sh := newShared()
		r := sh.rig(c.script, studio.TriggerOptions{})
		ctx := context.Background()
		fs := r.sh.files
		Expect(fs.EnsureHome(ctx, alice)).To(Succeed())
		dir, _ := fs.Abs(alice, "/Contracts")
		_, err := fs.Store().Mkdir(ctx, fs.Actor(alice), dir)
		Expect(err).NotTo(HaveOccurred())
		tr, err := r.host.CreateTrigger(ctx, alice, studio.TriggerInput{Name: "Contracts", Prompt: "p", Kind: triggers.KindFile, Folder: "/Contracts", Model: "m", Enabled: true})
		Expect(err).NotTo(HaveOccurred())
		chat := create(r.svc, alice)
		c.call(r, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"name":"Contracts in"}`, studio.TriggerRef(tr.ID)))
		card := lastCard(r, chat)
		got, err := r.host.ApplyChange(ctx, alice, card.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Folder).To(Equal(tr.Folder))
		Expect(got.Name).To(Equal("Contracts in"))
	})
})
