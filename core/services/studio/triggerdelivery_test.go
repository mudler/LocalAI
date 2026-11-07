package studio_test

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A trigger that a messaging app proposed is changed from a chat in Studio:
// the delivery and the acts follow the rules of a proposal and still wait for
// the person on the card.
var _ = Describe("changing the delivery and the acts of a trigger", func() {
	ctx := context.Background()
	var (
		r    *connRig
		c    *callScript
		b    connectors.Binding
		tr   triggers.Trigger
		ref  string
		chat string
	)
	setup := func(channels []connectors.Channel, allowed ...string) {
		c = &callScript{}
		tp := studiobot.NewTransport(studiobot.Addon{ID: "chat-addon", Kind: contract.ConnectorKind{ID: "chatty", Title: "Chatty", Delivery: contract.DeliveryPull}, Runtime: &postAddon{}})
		r = newConnRigOpts(newShared(), connStore(), tp, c.script, nil, nil)
		ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Worker", Model: "m", Builtins: []string{"act.files", "act.spend"}})
		Expect(err).NotTo(HaveOccurred())
		r.agent = ag
		gate := &capSwitch{}
		r.svc.SetCapabilityGate(gate)
		gate.set(contract.CapTalkbackChannels, true)
		b = r.bind(studio.ConnectorInput{Kind: "chatty", Token: "tok", Allow: []string{"11"}, Channels: channels,
			Approval: &connectors.Approval{Acts: allowed}})
		r.trust(b.ID)
		tr = r.schedule(studio.TriggerInput{Name: "Digest", Prompt: "p", AgentID: ag.ID,
			Route: triggers.Route{Binding: b.ID, Chat: "C1", Thread: "T1", Target: triggers.TargetOrigin}})
		ref = studio.TriggerRef(tr.ID)
		chat = create(r.svc, alice)
	}
	two := []connectors.Channel{{ID: "G1", Label: "General", Approved: true}, {ID: "G2", Label: "Ops", Approved: true}}

	It("waits on the card, reports the delivery as changed, and keeps the route when applied", func() {
		setup(two)
		res := c.call(r.trigRig, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"delivery":{"target":"channel","channel":"ops","mode":"on_change"}}`, ref))
		Expect(res.Err).To(BeNil(), "%+v", res)
		Expect(res.Out).To(ContainSubstring("delivery"))
		live, _ := r.host.GetTrigger(ctx, alice, tr.ID)
		Expect(live.Route.Target).To(Equal(triggers.TargetOrigin), "the edit applied before the person answered")
		card := lastCard(r.trigRig, chat)
		v, err := r.host.ChangeStatus(ctx, alice, card.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.Fields).To(Equal([]string{"delivery"}))
		got, err := r.host.ApplyChange(ctx, alice, card.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Route.Target).To(Equal(triggers.TargetChannel))
		Expect(got.Route.Channel).To(Equal("G2"), "the label is turned into the ID")
		Expect(got.Route.Mode).To(Equal(triggers.ModeOnChange))
		Expect(got.Route.Binding).To(Equal(b.ID))
		Expect(got.Route.Chat).To(Equal("C1"))
		back, err := r.host.UndoChange(ctx, alice, card.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(back.Route.Target).To(Equal(triggers.TargetOrigin))
		Expect(back.Route.Binding).To(Equal(b.ID))
	})

	It("refuses a channel the owner did not approve, naming the approved ones", func() {
		setup([]connectors.Channel{{ID: "G1", Label: "General", Approved: true}, {ID: "G2", Label: "Ops"}})
		res := c.call(r.trigRig, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"delivery":{"target":"channel","channel":"G2"}}`, ref))
		Expect(res.Err).To(HaveOccurred())
		Expect(res.Err.Error()).To(ContainSubstring("General"))
		res = c.call(r.trigRig, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"delivery":{"target":"channel","channel":"nowhere"}}`, ref))
		Expect(res.Err).To(HaveOccurred())
	})

	It("refuses a delivery for a trigger that no messaging app proposed", func() {
		setup(two)
		plain := r.schedule(studio.TriggerInput{Name: "Plain", Prompt: "p"})
		res := c.call(r.trigRig, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"delivery":{"target":"inbox"}}`, studio.TriggerRef(plain.ID)))
		Expect(res.Err).To(MatchError(ContainSubstring("messaging app")))
	})

	It("lists the approved channels in the description of the tool", func() {
		setup(two)
		c.call(r.trigRig, chat, "list_triggers", `{}`)
		d := r.h.LastToolDescription("update_trigger")
		Expect(d).To(ContainSubstring("General (G1)"))
		Expect(d).To(ContainSubstring("Ops (G2)"))
	})

	It("widens the acts only through the card, and only within the agent and the connection", func() {
		setup(two, "act.files", "act.send")
		// act.send is allowed by the connection but the agent does not hold it.
		res := c.call(r.trigRig, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"acts":["act.files","act.send"]}`, ref))
		Expect(res.Err).To(MatchError(ContainSubstring("act.send")))
		// act.spend is held by the agent but the connection does not allow it.
		res = c.call(r.trigRig, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"acts":["act.spend"]}`, ref))
		Expect(res.Err).To(MatchError(ContainSubstring("act.spend")))
		res = c.call(r.trigRig, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"acts":["act.files"]}`, ref))
		Expect(res.Err).To(BeNil(), "%+v", res)
		live, _ := r.host.GetTrigger(ctx, alice, tr.ID)
		Expect(live.Acts).To(BeEmpty(), "the acts widened before the person pressed Apply")
		card := lastCard(r.trigRig, chat)
		v, _ := r.host.ChangeStatus(ctx, alice, card.ID)
		Expect(v.Fields).To(Equal([]string{"acts"}))
		got, err := r.host.ApplyChange(ctx, alice, card.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Acts).To(Equal([]string{"act.files"}))
		Expect(got.Route.Binding).To(Equal(b.ID))
		back, err := r.host.UndoChange(ctx, alice, card.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(back.Acts).To(BeEmpty())
	})

	It("takes acts away with an empty list", func() {
		setup(two, "act.files")
		_, err := r.host.UpdateTrigger(ctx, alice, tr.ID, studio.TriggerInput{Name: "Digest", Prompt: "p", Kind: triggers.KindSchedule,
			Schedule: "daily 08:00", TZ: "UTC", AgentID: r.agent.ID, Model: "m", Enabled: true, Acts: []string{"act.files"}})
		Expect(err).NotTo(HaveOccurred())
		res := c.call(r.trigRig, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"acts":[]}`, ref))
		Expect(res.Err).To(BeNil(), "%+v", res)
		got, err := r.host.ApplyChange(ctx, alice, lastCard(r.trigRig, chat).ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Acts).To(BeEmpty())
	})

	It("uses the one approved channel when none is named, and asks for a name when there are several", func() {
		setup([]connectors.Channel{{ID: "G1", Label: "General", Approved: true}, {ID: "G9", Label: "Quiet"}})
		res := c.call(r.trigRig, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"delivery":{"target":"channel"}}`, ref))
		Expect(res.Err).To(BeNil(), "%+v", res)
		got, err := r.host.ApplyChange(ctx, alice, lastCard(r.trigRig, chat).ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Route.Channel).To(Equal("G1"))

		setup(two)
		res = c.call(r.trigRig, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"delivery":{"target":"channel"}}`, ref))
		Expect(res.Err).To(MatchError(ContainSubstring("General (G1), Ops (G2)")))
		res = c.call(r.trigRig, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"delivery":{"target":"channel","channel":"gen"}}`, ref))
		Expect(res.Err).To(BeNil(), "a unique part of a label: %+v", res)
	})

	It("names a listed channel that is not approved in the description, and refuses it with the reason", func() {
		setup([]connectors.Channel{{ID: "G9", Label: "Quiet"}})
		c.call(r.trigRig, chat, "list_triggers", `{}`)
		d := r.h.LastToolDescription("update_trigger")
		Expect(d).To(ContainSubstring("Quiet"))
		Expect(d).To(ContainSubstring("any allowed person"))
		res := c.call(r.trigRig, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"delivery":{"target":"channel","channel":"quiet"}}`, ref))
		Expect(res.Err).To(MatchError(ContainSubstring("verified person")))
	})
})
