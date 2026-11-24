package studio_test

import (
	"context"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const eventProposal = `{"name":"Review PRs","prompt":"Review the pull request.","kind":"event","source":"github","event_type":"pull_request",` +
	`"matches":["action=opened"],"condition":"the PR touches core/","acts":["act.files"]}`

var _ = Describe("ProposeTrigger of kind event", func() {
	ctx := context.Background()
	chatOf := func(r *trigRig, grants ...string) string {
		ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Worker", Model: "m", Builtins: grants})
		Expect(err).NotTo(HaveOccurred())
		rec, err := r.svc.Create(ctx, alice, studio.CreateInput{AgentID: ag.ID})
		Expect(err).NotTo(HaveOccurred())
		return rec.ID
	}
	run := func(r *trigRig, id string) {
		sent(r.svc, alice, id, "propose")
		idle(r.svc, alice, id)
	}

	It("stores a proposal with the condition in the prompt, and confirming returns the secret once", func() {
		r := newTrigRig(proposeScript(eventProposal))
		id := chatOf(r, "act.files")
		run(r, id)
		res := r.h.ResultsFor("propose_trigger")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).To(BeNil(), "%+v", res)
		Expect(res[0].Out).To(ContainSubstring("not active"))
		Expect(res[0].Out).To(ContainSubstring("secret"))
		props, _ := r.host.Proposals(ctx, alice)
		Expect(props).To(HaveLen(1))
		p := props[0]
		Expect(p.Kind).To(Equal(triggers.KindEvent))
		Expect(p.Proposed).To(BeTrue())
		Expect(p.Enabled).To(BeFalse())
		Expect(p.Secret).To(BeEmpty(), "a proposal has no secret")
		Expect(p.Source).To(Equal("github"))
		Expect(p.EventType).To(Equal("pull_request"))
		Expect(p.Matches).To(Equal([]string{"action=opened"}))
		Expect(p.Prompt).To(Equal("Review the pull request.\nAct only if: the PR touches core/"))
		Expect(p.Acts).To(Equal([]string{"act.files"}))

		v, err := r.host.ProposalStatus(ctx, alice, p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.Trigger.Secret).To(BeEmpty())

		got, err := r.host.ConfirmProposal(ctx, alice, p.ID, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Enabled).To(BeTrue())
		Expect(got.Secret).NotTo(BeEmpty(), "the confirm response carries the secret")
		Expect(len(got.Secret)).To(BeNumerically(">=", 43))

		// The secret is in the trigger row only: not in the receipt, not in the chat.
		v, err = r.host.ProposalStatus(ctx, alice, p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.State).To(Equal(studio.ProposalAccepted))
		Expect(v.Trigger.Secret).To(BeEmpty(), "the receipt shows the secret")
		stored, err := r.host.GetTrigger(ctx, alice, p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.Secret).To(Equal(got.Secret))
		for _, e := range toolEntries(r.svc, alice, id) {
			Expect(fmt.Sprintf("%+v", e)).NotTo(ContainSubstring(got.Secret))
		}
		for _, m := range r.h.Sent(id) {
			Expect(m).NotTo(ContainSubstring(got.Secret))
		}
		_, err = r.host.ConfirmProposal(ctx, alice, p.ID, "")
		Expect(err).To(HaveOccurred(), "a second confirm must not make a second secret")
		again, _ := r.host.GetTrigger(ctx, alice, p.ID)
		Expect(again.Secret).To(Equal(got.Secret))
	})

	It("proposes without a condition and leaves the prompt as it is", func() {
		r := newTrigRig(proposeScript(`{"name":"Hook","prompt":"Check the build.","kind":"event","source":"ci"}`))
		run(r, chatOf(r))
		props, _ := r.host.Proposals(ctx, alice)
		Expect(props).To(HaveLen(1))
		Expect(props[0].Prompt).To(Equal("Check the build."))
		Expect(props[0].Acts).To(BeEmpty(), "an event proposal with no acts is read only")
	})

	It("refuses an invalid match with the validation text and stores nothing", func() {
		r := newTrigRig(proposeScript(`{"name":"Hook","prompt":"p","kind":"event","source":"ci","matches":["no-equals-sign"]}`))
		run(r, chatOf(r))
		res := r.h.ResultsFor("propose_trigger")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).NotTo(BeNil())
		Expect(res[0].Err.Error()).To(ContainSubstring("match"))
		Expect(res[0].Err.Error()).NotTo(ContainSubstring("studio:"))
		props, _ := r.host.Proposals(ctx, alice)
		Expect(props).To(BeEmpty())
	})

	It("refuses a missing source, a long condition and a condition on another kind", func() {
		for name, args := range map[string]string{
			"source":    `{"name":"Hook","prompt":"p","kind":"event"}`,
			"condition": fmt.Sprintf(`{"name":"Hook","prompt":"p","kind":"event","source":"ci","condition":%q}`, strings.Repeat("x", 301)),
			"line":      `{"name":"Hook","prompt":"p","kind":"event","source":"ci","condition":"a\nb"}`,
			"kind":      `{"name":"Hook","prompt":"p","when":"daily 08:00","condition":"x"}`,
			"when":      `{"name":"Hook","prompt":"p","kind":"event","source":"ci","when":"daily 08:00"}`,
		} {
			r := newTrigRig(proposeScript(args))
			run(r, chatOf(r))
			res := r.h.ResultsFor("propose_trigger")
			Expect(res).To(HaveLen(1), name)
			Expect(res[0].Err).NotTo(BeNil(), name)
			props, _ := r.host.Proposals(ctx, alice)
			Expect(props).To(BeEmpty(), name)
		}
	})

	It("does not let the model name more acts than its agent holds", func() {
		r := newTrigRig(proposeScript(eventProposal))
		run(r, chatOf(r))
		res := r.h.ResultsFor("propose_trigger")
		Expect(res[0].Err).To(MatchError(ContainSubstring("does not hold act.files")))
	})

	It("keeps a waiting event proposal editable", func() {
		r := newTrigRig(proposeScript(eventProposal))
		run(r, chatOf(r, "act.files"))
		props, _ := r.host.Proposals(ctx, alice)
		p := props[0]
		edited, err := r.host.EditProposal(ctx, alice, p.ID, studio.TriggerInput{Name: p.Name, Prompt: p.Prompt, Kind: triggers.KindEvent,
			Source: "gitlab", EventType: "merge_request", Matches: []string{"action=open", "project.name=app"}, Model: p.Model, AgentID: p.AgentID})
		Expect(err).NotTo(HaveOccurred())
		Expect(edited.Proposed).To(BeTrue())
		Expect(edited.Secret).To(BeEmpty())
		Expect(edited.Source).To(Equal("gitlab"))
		Expect(edited.Matches).To(Equal([]string{"action=open", "project.name=app"}))
		Expect(edited.Acts).To(Equal([]string{"act.files"}))
		got, err := r.host.ConfirmProposal(ctx, alice, p.ID, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Secret).NotTo(BeEmpty())
	})

	It("refuses an event trigger that a chat of a messaging app would make", func() {
		r := newTrigRig(proposeScript(eventProposal))
		route := triggers.Route{Binding: "b1", Chat: "c1", Code: "AB12"}
		_, err := r.host.ProposeTrigger(ctx, alice, studio.TriggerInput{Name: "Hook", Prompt: "p", Kind: triggers.KindEvent, Source: "ci", Model: "m", Route: route})
		Expect(err).To(MatchError(ContainSubstring("create it in Studio")), "propose")
		props, _ := r.host.Proposals(ctx, alice)
		Expect(props).To(BeEmpty())

		// A stored proposal with a chat route, as an older version could keep.
		t := r.schedule(studio.TriggerInput{Name: "Hook", Prompt: "p", Kind: triggers.KindEvent, Source: "ci"})
		t.Proposed, t.Enabled, t.Route, t.Secret = true, false, route, ""
		Expect(r.sh.trg.PutTrigger(ctx, t)).To(Succeed())
		_, err = r.host.ConfirmProposal(ctx, alice, t.ID, "")
		Expect(err).To(MatchError(ContainSubstring("create it in Studio")), "confirm, the path of an answer in a chat and of automatic setup")
		got, err := r.sh.trg.GetTrigger(ctx, t.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Secret).To(BeEmpty())
		Expect(got.Enabled).To(BeFalse())
	})

	It("keeps two proposals that differ only in the match apart", func() {
		r := newTrigRig(proposeScript(`{"name":"Hook","prompt":"p","kind":"event","source":"ci","matches":["a=1"]}`))
		id := chatOf(r)
		run(r, id)
		r2 := `{"name":"Hook","prompt":"p","kind":"event","source":"ci","matches":["a=2"]}`
		_, err := r.host.ProposeTrigger(ctx, alice, studio.TriggerInput{Name: "Hook", Prompt: "p", Kind: triggers.KindEvent, Source: "ci", Matches: []string{"a=2"}, Model: "m"})
		Expect(err).NotTo(HaveOccurred(), r2)
		props, _ := r.host.Proposals(ctx, alice)
		Expect(props).To(HaveLen(2))
	})

	It("tells the model when to use it, to name the acts and that the owner pastes a URL and a secret", func() {
		r := newTrigRig(proposeScript(eventProposal))
		id := chatOf(r, "act.files")
		run(r, id)
		d := r.h.ToolDescription(id, "propose_trigger")
		Expect(d).To(ContainSubstring("kind event"))
		Expect(d).To(ContainSubstring("outside system calls"))
		Expect(d).To(ContainSubstring("URL and a secret"))
		Expect(d).To(ContainSubstring("paste into the sender"))
		Expect(d).To(ContainSubstring("Name in acts what the run needs"))
		Expect(r.h.ToolSchema(id, "propose_trigger")).To(ContainSubstring(`"event_type"`))
	})

	It("lets update_trigger change an event trigger and keep its secret, and never offers a secret field", func() {
		c := &callScript{}
		r := newTrigRig(c.script)
		tr := r.schedule(studio.TriggerInput{Name: "Hook", Prompt: "Old.", Kind: triggers.KindEvent, Source: "ci", EventType: "push"})
		Expect(tr.Secret).NotTo(BeEmpty())
		chat := create(r.svc, alice)
		res := c.call(r, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"prompt":"New.","secret":"attacker"}`, studio.TriggerRef(tr.ID)))
		Expect(res.Err).To(BeNil(), "%+v", res)
		card := lastCard(r, chat)
		Expect(card).NotTo(BeNil())
		_, err := r.host.ApplyChange(ctx, alice, card.ID)
		Expect(err).NotTo(HaveOccurred())
		live, err := r.host.GetTrigger(ctx, alice, tr.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(live.Prompt).To(Equal("New."))
		Expect(live.Secret).To(Equal(tr.Secret))
		Expect(r.h.ToolSchema(chat, "update_trigger")).NotTo(ContainSubstring("secret"))
		out := c.call(r, chat, "list_triggers", fmt.Sprintf(`{"ref":%q,"include_prompt":true}`, studio.TriggerRef(tr.ID)))
		Expect(out.Out).To(ContainSubstring(`"source":"ci"`))
		Expect(out.Out).NotTo(ContainSubstring(tr.Secret))
	})
})
