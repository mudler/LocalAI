package studio_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const actsProposal = `{"name":"Report","prompt":"Write the report.","when":"daily 09:00","time_zone":"UTC","acts":["act.files"]}`

var _ = Describe("the acts of a proposed trigger in a Studio chat", func() {
	ctx := context.Background()
	chatOf := func(r *trigRig, grants ...string) string {
		ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Worker", Model: "m", Builtins: grants})
		Expect(err).NotTo(HaveOccurred())
		rec, err := r.svc.Create(ctx, alice, studio.CreateInput{AgentID: ag.ID})
		Expect(err).NotTo(HaveOccurred())
		return rec.ID
	}
	propose := func(r *trigRig, id string) {
		sent(r.svc, alice, id, "propose")
		idle(r.svc, alice, id)
	}

	It("stores the acts on the proposal and confirming grants exactly those", func() {
		r := newTrigRig(proposeScript(actsProposal))
		id := chatOf(r, "act.files", "act.spend")
		propose(r, id)
		props, err := r.host.Proposals(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		Expect(props).To(HaveLen(1))
		Expect(props[0].Acts).To(Equal([]string{"act.files"}))
		Expect(props[0].Enabled).To(BeFalse())
		got, err := r.host.ConfirmProposal(ctx, alice, props[0].ID, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Acts).To(Equal([]string{"act.files"}), "the agent also holds act.spend; the trigger must not")
		v := r.host.ActsView(ctx, got)
		Expect(v.ReadOnly).To(BeFalse())
		Expect(v.Held).To(Equal([]string{"act.files"}))
	})

	It("refuses an act the agent does not hold and stores nothing", func() {
		r := newTrigRig(proposeScript(actsProposal))
		id := chatOf(r, "act.send")
		propose(r, id)
		res := r.h.ResultsFor("propose_trigger")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).To(MatchError(ContainSubstring("does not hold act.files")))
		props, _ := r.host.Proposals(ctx, alice)
		Expect(props).To(BeEmpty())
	})

	It("keeps the acts when the person edits the proposal and leaves them out", func() {
		r := newTrigRig(proposeScript(actsProposal))
		id := chatOf(r, "act.files")
		propose(r, id)
		props, _ := r.host.Proposals(ctx, alice)
		p := props[0]
		edited, err := r.host.EditProposal(ctx, alice, p.ID, studio.TriggerInput{Name: "Renamed", Prompt: p.Prompt, Kind: p.Kind,
			Schedule: p.Schedule, TZ: p.TZ, Model: p.Model, AgentID: p.AgentID})
		Expect(err).NotTo(HaveOccurred())
		Expect(edited.Name).To(Equal("Renamed"))
		Expect(edited.Acts).To(Equal([]string{"act.files"}))
	})

	It("refuses a name that is not an act", func() {
		r := newTrigRig(proposeScript(`{"name":"R","prompt":"p","when":"daily 09:00","acts":["act.root"]}`))
		id := chatOf(r, "act.files")
		propose(r, id)
		res := r.h.ResultsFor("propose_trigger")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).To(HaveOccurred())
		props, _ := r.host.Proposals(ctx, alice)
		Expect(props).To(BeEmpty())
	})

	It("is read only without acts, and says why", func() {
		r := newTrigRig(proposeScript(goodProposal))
		id := chatOf(r, "act.files")
		propose(r, id)
		props, _ := r.host.Proposals(ctx, alice)
		Expect(props).To(HaveLen(1))
		Expect(props[0].Acts).To(BeEmpty())
		v := r.host.ActsView(ctx, props[0])
		Expect(v.ReadOnly).To(BeTrue())
		Expect(v.Why).To(Equal(studio.ReadOnlyNoActs))
	})

	It("says the agent is the reason when it holds none of the acts of the trigger", func() {
		r := newTrigRig(proposeScript(actsProposal))
		id := chatOf(r, "act.files")
		propose(r, id)
		props, _ := r.host.Proposals(ctx, alice)
		Expect(props).To(HaveLen(1))
		_, err := r.svc.SaveAgent(ctx, alice, agents.Agent{ID: props[0].AgentID, Name: "Worker", Model: "m", Builtins: []string{}})
		Expect(err).NotTo(HaveOccurred())
		v := r.host.ActsView(ctx, props[0])
		Expect(v.ReadOnly).To(BeTrue())
		Expect(v.Why).To(Equal(studio.ReadOnlyAgent))
	})

	It("keeps two proposals that differ only in their acts apart", func() {
		r := newTrigRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			switch text {
			case "propose":
				return []studiotest.ToolStep{{Tool: "propose_trigger", Args: goodProposal}, {Say: "ok"}}
			case "wider":
				return []studiotest.ToolStep{{Tool: "propose_trigger", Args: `{"name":"Morning digest","prompt":"Summarise yesterday.","when":"weekdays 09:00","time_zone":"Europe/Rome","acts":["act.files"]}`}, {Say: "ok"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		})
		id := chatOf(r, "act.files")
		propose(r, id)
		sent(r.svc, alice, id, "wider")
		idle(r.svc, alice, id)
		props, _ := r.host.Proposals(ctx, alice)
		Expect(props).To(HaveLen(2), "a wider proposal must not be folded into the narrower one that waits")
	})
})

var _ = Describe("a model cannot change the acts of a trigger", func() {
	It("ignores acts in update_trigger and keeps them through an applied change", func() {
		ctx := context.Background()
		c := &callScript{}
		r := newTrigRig(c.script)
		ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Worker", Model: "m", Builtins: []string{"act.files", "act.spend"}})
		Expect(err).NotTo(HaveOccurred())
		tr := r.schedule(studio.TriggerInput{Name: "Nightly", Prompt: "p", AgentID: ag.ID, Acts: []string{"act.files"}})
		rec, err := r.svc.Create(ctx, alice, studio.CreateInput{AgentID: ag.ID})
		Expect(err).NotTo(HaveOccurred())
		ref := studio.TriggerRef(tr.ID)
		res := c.call(r, rec.ID, "update_trigger", fmt.Sprintf(`{"ref":%q,"name":"Renamed","acts":["act.files","act.spend","act.send"]}`, ref))
		Expect(res.Err).To(BeNil(), "%+v", res)
		card := lastCard(r, rec.ID)
		Expect(card).NotTo(BeNil())
		got, err := r.host.ApplyChange(ctx, alice, card.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Name).To(Equal("Renamed"))
		Expect(got.Acts).To(Equal([]string{"act.files"}))
	})
})

var _ = Describe("the acts of a trigger proposed in a connector chat", func() {
	ctx := context.Background()
	const (
		chatID = "C1"
		thread = "T1"
	)
	var (
		fake *postAddon
		gate *capSwitch
		r    *connRig
		b    connectors.Binding
		args string
		seq  int
	)
	script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
		if text == "propose" {
			return []studiotest.ToolStep{{Tool: "propose_trigger", Args: args}, {Say: "I proposed it."}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
	var approval *connectors.Approval
	setup := func(grants ...string) {
		fake = &postAddon{}
		gate = &capSwitch{}
		tr := studiobot.NewTransport(studiobot.Addon{ID: "chat-addon", Kind: contract.ConnectorKind{ID: "chatty", Title: "Chatty", Delivery: contract.DeliveryPull}, Runtime: fake})
		r = newConnRigOpts(newShared(), connStore(), tr, script, nil, func(o *studio.ConnectorOptions) {
			o.OutboxPoll = 10 * time.Millisecond
			o.RatePerMinute = 100
		})
		ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Worker", Model: "m", Builtins: append([]string{studio.CapTriggersPropose}, grants...)})
		Expect(err).NotTo(HaveOccurred())
		r.agent = ag
		r.svc.SetCapabilityGate(gate)
		gate.set(studio.CapTriggersPropose, true)
		gate.set(studio.CapProposalsConfirm, true)
		b = r.bind(studio.ConnectorInput{Kind: "chatty", Token: "tok", Allow: []string{"11"}, Approval: approval})
		seq = 0
	}
	say := func(text string) {
		seq++
		Expect(r.conns.Ingest(ctx, "chat-addon", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: contract.ConnectorMessage{
			Chat: chatID, Thread: thread, Mention: true, MessageID: "m" + string(rune('a'+seq)), SenderID: "11", SenderName: "Cy", Text: text}})).To(Succeed())
	}
	answered := func(n int) {
		waitFor("the answer", func() bool { return len(fake.texts(chatID, thread)) >= n })
	}
	proposals := func() []triggers.Trigger {
		ps, err := r.host.Proposals(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		return ps
	}
	propose := func(a string) triggers.Trigger {
		args = a
		before := len(fake.texts(chatID, thread))
		say("propose")
		answered(before + 2)
		ps := proposals()
		Expect(ps).NotTo(BeEmpty())
		return ps[len(ps)-1]
	}
	const base = `"name":"Report","prompt":"RUN write it","when":"daily 08:00","time_zone":"UTC","delivery":{"target":"origin"}`

	It("lists the acts in the text of the proposal, and a yes grants those", func() {
		setup("act.files", "act.spend")
		p := propose(`{` + base + `,"acts":["act.files"]}`)
		Expect(p.Acts).To(Equal([]string{"act.files"}))
		text := fake.texts(chatID, thread)[0]
		Expect(text).To(ContainSubstring("Permissions:"))
		Expect(text).To(ContainSubstring("write files in the agent's folders"))
		Expect(text).To(ContainSubstring("(sensitive)"))
		Expect(text).NotTo(ContainSubstring("paid or external"), "an act the proposal does not carry is not listed")
		before := len(fake.texts(chatID, thread))
		say("yes")
		answered(before + 1)
		list, err := r.host.ListTriggers(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Acts).To(Equal([]string{"act.files"}))
		Expect(strings.Join(fake.texts(chatID, thread), "\n")).To(ContainSubstring("Confirmed."))
	})

	It("lists the acts when the person answers with the code", func() {
		setup("act.spend")
		p := propose(`{` + base + `,"acts":["act.spend"]}`)
		before := len(fake.texts(chatID, thread))
		say("confirm " + p.Route.Code)
		answered(before + 1)
		list, _ := r.host.ListTriggers(ctx, alice)
		Expect(list).To(HaveLen(1))
		Expect(list[0].Acts).To(Equal([]string{"act.spend"}))
		last := fake.texts(chatID, thread)
		Expect(last[len(last)-1]).To(ContainSubstring("paid or external tools"))
	})

	It("says read only in the text when the proposal has no acts", func() {
		setup("act.files")
		propose(`{` + base + `}`)
		Expect(fake.texts(chatID, thread)[0]).To(ContainSubstring("Permissions: read only"))
	})

	It("never sets up a trigger with acts without a yes, even in the automatic mode", func() {
		approval = &connectors.Approval{Mode: connectors.ApprovalAuto}
		DeferCleanup(func() { approval = nil })
		setup("act.files")
		propose(`{` + base + `,"acts":["act.files"]}`)
		list, _ := r.host.ListTriggers(ctx, alice)
		Expect(list).To(BeEmpty())
		Expect(proposals()).To(HaveLen(1))
	})

	It("refuses acts the agent does not hold", func() {
		setup("act.files")
		args = `{` + base + `,"acts":["act.spend"]}`
		before := len(fake.texts(chatID, thread))
		say("propose")
		answered(before + 1)
		Expect(proposals()).To(BeEmpty())
		res := r.h.ResultsFor("propose_trigger")
		Expect(res[len(res)-1].Err).To(MatchError(ContainSubstring("does not hold act.spend")))
	})

	It("calls a proposal that must only read read only because it said so", func() {
		setup("act.files")
		p := propose(`{` + base + `,"access":"read_only"}`)
		v := r.host.ActsView(ctx, p)
		Expect(v.ReadOnly).To(BeTrue())
		Expect(v.Why).To(Equal(studio.ReadOnlyDeclared))
		Expect(fake.texts(chatID, thread)[0]).To(ContainSubstring("Permissions: read only"))
	})

	It("refuses acts on a run that must only read", func() {
		setup("act.files")
		args = `{` + base + `,"access":"read_only","acts":["act.files"]}`
		before := len(fake.texts(chatID, thread))
		say("propose")
		answered(before + 1)
		Expect(proposals()).To(BeEmpty())
	})
})
