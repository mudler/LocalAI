package studio_test

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A chat in Studio can report a trigger into a connection that the owner set
// up. The kinds in these specs are made-up platforms: one receives messages,
// one is a service that agents use only as a tool.
var _ = Describe("a trigger from a chat in Studio that reports to a connection", func() {
	ctx := context.Background()
	const bob = "bob"
	var (
		r     *connRig
		c     *callScript
		gate  *capSwitch
		fake  *postAddon
		chat  string
		chatB connectors.Binding // a connection that can receive reports
		svcB  connectors.Binding // a connection of a service that is only a tool
		other connectors.Binding // a connection of another owner
	)
	channels := []connectors.Channel{{ID: "G1", Label: "General", Approved: true}, {ID: "G2", Label: "Ops"}}
	setup := func() {
		c = &callScript{}
		fake = &postAddon{}
		tp := studiobot.NewTransport(
			studiobot.Addon{ID: "chat-addon", Kind: contract.ConnectorKind{ID: "chatty", Title: "Chatty", Delivery: contract.DeliveryPull}, Runtime: fake},
			studiobot.Addon{ID: "service-addon", Kind: contract.ConnectorKind{ID: "servicey", Title: "Servicey", Delivery: contract.DeliveryPull, ToolOnly: true}, Runtime: &postAddon{}},
		)
		r = newConnRigOpts(newShared(), connStore(), tp, c.script, nil, nil)
		gate = &capSwitch{}
		r.svc.SetCapabilityGate(gate)
		gate.set(contract.CapTalkbackChannels, true)
		chatB = r.bind(studio.ConnectorInput{Kind: "chatty", Name: "Team chat", Token: "secret-token-1", Allow: []string{"11"}, Channels: channels})
		sb, err := r.conns.CreateConnector(ctx, alice, studio.ConnectorInput{Kind: "servicey", Name: "Code host", Token: "secret-token-2",
			ConfirmDuplicate: studio.DuplicateConfirmation})
		Expect(err).NotTo(HaveOccurred())
		svcB = sb.Binding
		agB, err := r.svc.SaveAgent(ctx, bob, agents.Agent{Name: "Other", Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		ob, err := r.conns.CreateConnector(ctx, bob, studio.ConnectorInput{Kind: "chatty", Name: "Not yours", Token: "secret-token-3", AgentID: agB.ID, Enabled: true,
			Allow: []string{"11"}, Channels: []connectors.Channel{{ID: "B1", Label: "Bob room", Approved: true}}, ConfirmDuplicate: studio.DuplicateConfirmation, Reply: connectors.ReplyOff})
		Expect(err).NotTo(HaveOccurred())
		r.trust(ob.ID)
		other = ob.Binding
		chat = create(r.svc, alice)
	}
	propose := func(delivery string) studiotest.ToolResult {
		return c.call(r.trigRig, chat, "propose_trigger", fmt.Sprintf(`{"name":"Digest","prompt":"RUN go","when":"daily 08:00","time_zone":"UTC","delivery":%s}`, delivery))
	}
	waiting := func() []triggers.Trigger {
		p, err := r.host.Proposals(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		return p
	}

	Describe("list_connections", func() {
		It("lists the connections of the owner with their approved channels, and says which can receive reports", func() {
			setup()
			res := c.call(r.trigRig, chat, "list_connections", `{}`)
			Expect(res.Err).To(BeNil(), "%+v", res)
			var out struct {
				Connections []struct {
					ID, Kind, Name  string
					Enabled         bool
					CanReceive      bool `json:"can_receive_reports"`
					TalkbackAllowed bool `json:"talkback_allowed"`
					Channels        []map[string]string
					Why             string
				}
				Kinds   []string `json:"kinds_that_can_receive_reports"`
				Service []string `json:"service_only_kinds"`
			}
			Expect(json.Unmarshal([]byte(res.Out), &out)).To(Succeed(), res.Out)
			Expect(out.Connections).To(HaveLen(2), "the other owner's connection must not show: %s", res.Out)
			byID := map[string]int{}
			for i, x := range out.Connections {
				byID[x.ID] = i
			}
			chatty := out.Connections[byID[chatB.ID]]
			Expect(chatty.Name).To(Equal("Team chat"))
			Expect(chatty.Enabled).To(BeTrue())
			Expect(chatty.CanReceive).To(BeTrue())
			Expect(chatty.TalkbackAllowed).To(BeTrue())
			Expect(chatty.Channels).To(Equal([]map[string]string{{"id": "G1", "label": "General"}}), "only the approved channel is offered")
			service := out.Connections[byID[svcB.ID]]
			Expect(service.CanReceive).To(BeFalse())
			Expect(service.Channels).To(BeEmpty())
			Expect(service.Why).To(ContainSubstring("tool"))
			Expect(out.Kinds).To(Equal([]string{"chatty"}))
			Expect(out.Service).To(Equal([]string{"servicey"}))
		})

		It("shows no token, sender, pairing data or other owner's connection", func() {
			setup()
			res := c.call(r.trigRig, chat, "list_connections", `{}`)
			Expect(res.Err).To(BeNil())
			for _, secret := range []string{"secret-token", "Allow", "allow_list", "Paired", "paired", "\"11\"", other.ID, "Not yours", "Bob room", "B1", "Ops", "G2"} {
				Expect(res.Out).NotTo(ContainSubstring(secret), "the result must not carry %q: %s", secret, res.Out)
			}
		})

		It("says talk-back is not allowed when the server switch is off", func() {
			setup()
			gate.set(contract.CapTalkbackChannels, false)
			res := c.call(r.trigRig, chat, "list_connections", `{}`)
			Expect(res.Err).To(BeNil())
			Expect(res.Out).To(ContainSubstring(`"talkback_allowed":false`))
			Expect(res.Out).NotTo(ContainSubstring("General"), "no channel is offered while talk-back is off")
		})

		It("names the kinds that can and cannot be a destination in the description of propose_trigger", func() {
			setup()
			c.call(r.trigRig, chat, "list_triggers", `{}`)
			d := r.h.LastToolDescription("propose_trigger")
			Expect(d).To(ContainSubstring("list_connections"))
			Expect(d).To(ContainSubstring("can receive a report: chatty"))
			Expect(d).To(ContainSubstring("servicey"))
			Expect(d).To(ContainSubstring("refused as a destination"))
			Expect(d).NotTo(ContainSubstring("Team chat"), "no connection name in a description that is cached")
		})
	})

	Describe("propose_trigger with a destination", func() {
		It("keeps the Inbox when there is no delivery", func() {
			setup()
			res := c.call(r.trigRig, chat, "propose_trigger", `{"name":"Digest","prompt":"RUN go","when":"daily 08:00","time_zone":"UTC"}`)
			Expect(res.Err).To(BeNil(), "%+v", res)
			p := waiting()
			Expect(p).To(HaveLen(1))
			Expect(p[0].Route.TargetOrDefault()).To(Equal(triggers.TargetInbox))
			Expect(p[0].Route.Connection).To(BeEmpty())
		})

		It("does not hand back the Inbox proposal when the same trigger is proposed again with a channel", func() {
			setup()
			Expect(c.call(r.trigRig, chat, "propose_trigger", `{"name":"Digest","prompt":"RUN go","when":"daily 08:00","time_zone":"UTC"}`).Err).To(BeNil())
			Expect(propose(fmt.Sprintf(`{"target":"channel","connection":%q,"channel":"G1"}`, chatB.ID)).Err).To(BeNil())
			p := waiting()
			Expect(p).To(HaveLen(2))
			var channel int
			for _, x := range p {
				if x.Route.Target == triggers.TargetChannel {
					channel++
				}
			}
			Expect(channel).To(Equal(1))
		})

		It("stores the connection, the channel as an ID and the mode, and waits for the person", func() {
			setup()
			res := propose(fmt.Sprintf(`{"target":"channel","connection":%q,"channel":"#general","mode":"on_change"}`, chatB.ID))
			Expect(res.Err).To(BeNil(), "%+v", res)
			p := waiting()
			Expect(p).To(HaveLen(1))
			Expect(p[0].Enabled).To(BeFalse(), "a proposal does not run")
			Expect(p[0].Route.Target).To(Equal(triggers.TargetChannel))
			Expect(p[0].Route.Connection).To(Equal(chatB.ID))
			Expect(p[0].Route.Channel).To(Equal("G1"), "the label is turned into the ID")
			Expect(p[0].Route.Mode).To(Equal(triggers.ModeOnChange))
			Expect(p[0].Route.Binding).To(BeEmpty(), "it is not a connector trigger")
			Expect(p[0].Route.Code).To(BeEmpty(), "no code to confirm by text")
			d := r.host.DeliveryOf(ctx, p[0])
			Expect(d.Target).To(Equal(triggers.TargetChannel))
			Expect(d.ChannelLabel).To(Equal("General"))
			Expect(d.ConnectionName).To(Equal("Team chat"))
		})

		It("stores the route when the person confirms, and the runs report in the channel", func() {
			setup()
			Expect(propose(fmt.Sprintf(`{"target":"channel","connection":%q,"channel":"G1"}`, chatB.ID)).Err).To(BeNil())
			p := waiting()
			Expect(p).To(HaveLen(1))
			t, err := r.host.ConfirmProposal(ctx, alice, p[0].ID, "UTC")
			Expect(err).NotTo(HaveOccurred())
			Expect(t.Route.Connection).To(Equal(chatB.ID))
			Expect(t.Route.Channel).To(Equal("G1"))
			Expect(t.Route.Target).To(Equal(triggers.TargetChannel))
			live, err := r.host.GetTrigger(ctx, alice, t.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(live.Route).To(Equal(t.Route))
			r.due()
			r.waitEnded(t.ID)
			waitFor("the report in the channel", func() bool { return len(fake.texts("G1", "")) == 1 })
		})

		It("refuses a channel the owner did not approve, and names the approved ones", func() {
			setup()
			res := propose(fmt.Sprintf(`{"target":"channel","connection":%q,"channel":"Ops"}`, chatB.ID))
			Expect(res.Err).To(HaveOccurred())
			Expect(res.Err.Error()).To(ContainSubstring("General"))
			res = propose(fmt.Sprintf(`{"target":"channel","connection":%q,"channel":"G2"}`, chatB.ID))
			Expect(res.Err).To(HaveOccurred())
			res = propose(fmt.Sprintf(`{"target":"channel","connection":%q,"channel":"nowhere"}`, chatB.ID))
			Expect(res.Err).To(HaveOccurred())
			Expect(waiting()).To(BeEmpty())
		})

		It("refuses the connection of another owner as if it did not exist", func() {
			setup()
			res := propose(fmt.Sprintf(`{"target":"channel","connection":%q,"channel":"B1"}`, other.ID))
			Expect(res.Err).To(MatchError(ContainSubstring("not one of the user's connections")))
			Expect(res.Err.Error()).NotTo(ContainSubstring("Bob"))
			Expect(waiting()).To(BeEmpty())
		})

		It("refuses a connection that is switched off", func() {
			setup()
			b, err := r.store.GetBinding(ctx, chatB.ID)
			Expect(err).NotTo(HaveOccurred())
			b.Enabled = false
			Expect(r.store.PutBinding(ctx, b)).To(Succeed())
			res := propose(fmt.Sprintf(`{"target":"channel","connection":%q,"channel":"G1"}`, chatB.ID))
			Expect(res.Err).To(MatchError(ContainSubstring("switched off")))
			Expect(waiting()).To(BeEmpty())
		})

		It("refuses a connection of a service that agents use as a tool, and says which kinds can receive", func() {
			setup()
			res := propose(fmt.Sprintf(`{"target":"channel","connection":%q,"channel":"G1"}`, svcB.ID))
			Expect(res.Err).To(MatchError(ContainSubstring("cannot receive a report")))
			Expect(res.Err.Error()).To(ContainSubstring("chatty"))
			Expect(waiting()).To(BeEmpty())
		})

		It("refuses when talk-back is off for the connection or the server", func() {
			setup()
			gate.set(contract.CapTalkbackChannels, false)
			res := propose(fmt.Sprintf(`{"target":"channel","connection":%q,"channel":"G1"}`, chatB.ID))
			Expect(res.Err).To(MatchError(ContainSubstring("talk-back is off")))
			Expect(waiting()).To(BeEmpty())
		})

		It("refuses a channel with no connection, an unknown connection, the origin and a stray channel", func() {
			setup()
			Expect(propose(`{"target":"channel","channel":"G1"}`).Err).To(MatchError(ContainSubstring("list_connections")))
			Expect(propose(`{"target":"channel","connection":"nope","channel":"G1"}`).Err).To(MatchError(ContainSubstring("not one of the user's connections")))
			Expect(propose(`{"target":"origin"}`).Err).To(MatchError(ContainSubstring("messaging app")))
			Expect(propose(fmt.Sprintf(`{"target":"inbox","connection":%q}`, chatB.ID)).Err).To(HaveOccurred())
			Expect(propose(`{"mode":"sometimes"}`).Err).To(HaveOccurred())
			Expect(waiting()).To(BeEmpty())
		})

		It("refuses to confirm when the connection went away or the channel is no longer approved", func() {
			setup()
			Expect(propose(fmt.Sprintf(`{"target":"channel","connection":%q,"channel":"G1"}`, chatB.ID)).Err).To(BeNil())
			p := waiting()
			Expect(p).To(HaveLen(1))
			b, _ := r.store.GetBinding(ctx, chatB.ID)
			b.Channels = []connectors.Channel{{ID: "G1", Label: "General"}}
			Expect(r.store.PutBinding(ctx, b)).To(Succeed())
			_, err := r.host.ConfirmProposal(ctx, alice, p[0].ID, "UTC")
			Expect(err).To(MatchError(ContainSubstring("not approved any more")))
			b.Channels = channels
			b.Enabled = false
			Expect(r.store.PutBinding(ctx, b)).To(Succeed())
			_, err = r.host.ConfirmProposal(ctx, alice, p[0].ID, "UTC")
			Expect(err).To(MatchError(ContainSubstring("gone or switched off")))
		})

		It("does not deliver to a channel that stopped being approved after the confirmation", func() {
			setup()
			Expect(propose(fmt.Sprintf(`{"target":"channel","connection":%q,"channel":"G1"}`, chatB.ID)).Err).To(BeNil())
			t, err := r.host.ConfirmProposal(ctx, alice, waiting()[0].ID, "UTC")
			Expect(err).NotTo(HaveOccurred())
			b, _ := r.store.GetBinding(ctx, chatB.ID)
			b.Channels = []connectors.Channel{{ID: "G1", Label: "General"}}
			Expect(r.store.PutBinding(ctx, b)).To(Succeed())
			r.due()
			r.waitEnded(t.ID)
			Consistently(func() int { return len(fake.texts("G1", "")) }, "300ms", "50ms").Should(BeZero())
		})
	})

	Describe("update_trigger with a destination", func() {
		var ref string
		BeforeEach(func() {
			setup()
			tr := r.schedule(studio.TriggerInput{Name: "Plain", Prompt: "p"})
			ref = studio.TriggerRef(tr.ID)
		})
		It("waits on the card, then reports to the channel; undo puts the Inbox back", func() {
			res := c.call(r.trigRig, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"delivery":{"target":"channel","connection":%q,"channel":"general"}}`, ref, chatB.ID))
			Expect(res.Err).To(BeNil(), "%+v", res)
			Expect(res.Out).To(ContainSubstring("delivery"))
			card := lastCard(r.trigRig, chat)
			got, err := r.host.ApplyChange(ctx, alice, card.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Route.Target).To(Equal(triggers.TargetChannel))
			Expect(got.Route.Connection).To(Equal(chatB.ID))
			Expect(got.Route.Channel).To(Equal("G1"))
			Expect(got.Route.Binding).To(BeEmpty())
			back, err := r.host.UndoChange(ctx, alice, card.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(back.Route.TargetOrDefault()).To(Equal(triggers.TargetInbox))
			Expect(back.Route.Connection).To(BeEmpty())
		})
		It("moves the answers back to the Inbox", func() {
			tr := r.schedule(studio.TriggerInput{Name: "Reports", Prompt: "p",
				Route: triggers.Route{Connection: chatB.ID, Channel: "G1", Target: triggers.TargetChannel}})
			res := c.call(r.trigRig, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"delivery":{"target":"inbox"}}`, studio.TriggerRef(tr.ID)))
			Expect(res.Err).To(BeNil(), "%+v", res)
			got, err := r.host.ApplyChange(ctx, alice, lastCard(r.trigRig, chat).ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Route.TargetOrDefault()).To(Equal(triggers.TargetInbox))
			Expect(got.Route.Connection).To(BeEmpty())
			Expect(got.Route.Channel).To(BeEmpty())
		})
		It("refuses the same destinations as a proposal does", func() {
			for name, d := range map[string]string{
				"another owner's connection":   fmt.Sprintf(`{"target":"channel","connection":%q,"channel":"B1"}`, other.ID),
				"a channel not approved":       fmt.Sprintf(`{"target":"channel","connection":%q,"channel":"Ops"}`, chatB.ID),
				"a service-only connection":    fmt.Sprintf(`{"target":"channel","connection":%q,"channel":"G1"}`, svcB.ID),
				"the origin":                   `{"target":"origin"}`,
				"a channel without the target": fmt.Sprintf(`{"connection":%q,"channel":"G1"}`, chatB.ID),
			} {
				res := c.call(r.trigRig, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"delivery":%s}`, ref, d))
				Expect(res.Err).To(HaveOccurred(), name)
			}
		})
	})
	Describe("the mode of the delivery on the cards", func() {
		It("is part of the delivery view of a proposal, and the default adds no word", func() {
			setup()
			Expect(propose(fmt.Sprintf(`{"target":"channel","connection":%q,"channel":"G1","mode":"on_problem"}`, chatB.ID)).Err).To(BeNil())
			Expect(propose(fmt.Sprintf(`{"target":"channel","connection":%q,"channel":"G1"}`, chatB.ID)).Err).To(BeNil())
			modes := map[string]bool{}
			for _, p := range waiting() {
				modes[r.host.DeliveryOf(ctx, p).Mode] = true
			}
			Expect(modes).To(HaveKey(triggers.ModeOnProblem))
			Expect(modes).To(HaveKey(triggers.ModeAlways), "the default is always, which the card does not spell out")
		})
		It("shows a change of the mode alone as a change of the delivery, with the mode on each side", func() {
			setup()
			tr := r.schedule(studio.TriggerInput{Name: "Reports", Prompt: "p",
				Route: triggers.Route{Connection: chatB.ID, Channel: "G1", Target: triggers.TargetChannel}})
			res := c.call(r.trigRig, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"delivery":{"mode":"on_problem"}}`, studio.TriggerRef(tr.ID)))
			Expect(res.Err).To(BeNil(), "%+v", res)
			v, err := r.host.ChangeStatus(ctx, alice, lastCard(r.trigRig, chat).ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(v.Fields).To(ContainElement("delivery"), "a mode-only change is a visible change")
			before, after := r.host.DeliveryOf(ctx, v.Before), r.host.DeliveryOf(ctx, v.After)
			Expect(before.Mode).To(Equal(triggers.ModeAlways))
			Expect(after.Mode).To(Equal(triggers.ModeOnProblem))
			Expect(after.ChannelLabel).To(Equal(before.ChannelLabel))
			Expect(after.ConnectionName).To(Equal(before.ConnectionName))
		})
	})

	Describe("applying and undoing a change after the connection changed", func() {
		var ref, tid string
		toChannel := func() string {
			res := c.call(r.trigRig, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"delivery":{"target":"channel","connection":%q,"channel":"G1"}}`, ref, chatB.ID))
			Expect(res.Err).To(BeNil(), "%+v", res)
			return lastCard(r.trigRig, chat).ID
		}
		edit := func(f func(b *connectors.Binding)) {
			b, err := r.store.GetBinding(ctx, chatB.ID)
			Expect(err).NotTo(HaveOccurred())
			f(&b)
			Expect(r.store.PutBinding(ctx, b)).To(Succeed())
		}
		BeforeEach(func() {
			setup()
			tid = r.schedule(studio.TriggerInput{Name: "Plain", Prompt: "p"}).ID
			ref = studio.TriggerRef(tid)
		})
		It("applies while the connection and the channel are usable", func() {
			got, err := r.host.ApplyChange(ctx, alice, toChannel())
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Route.Connection).To(Equal(chatB.ID))
		})
		It("refuses to apply after the connection was switched off, and keeps the trigger", func() {
			id := toChannel()
			edit(func(b *connectors.Binding) { b.Enabled = false })
			_, err := r.host.ApplyChange(ctx, alice, id)
			Expect(err).To(MatchError(ContainSubstring("gone or switched off")))
			live, err := r.host.GetTrigger(ctx, alice, tid)
			Expect(err).NotTo(HaveOccurred())
			Expect(live.Route.TargetOrDefault()).To(Equal(triggers.TargetInbox))
			Expect(live.Route.Connection).To(BeEmpty())
		})
		It("refuses to apply after the channel was un-approved, and the change can be answered again later", func() {
			id := toChannel()
			edit(func(b *connectors.Binding) { b.Channels = []connectors.Channel{{ID: "G1", Label: "General"}} })
			_, err := r.host.ApplyChange(ctx, alice, id)
			Expect(err).To(MatchError(ContainSubstring("not approved any more")))
			edit(func(b *connectors.Binding) { b.Channels = channels })
			got, err := r.host.ApplyChange(ctx, alice, id)
			Expect(err).NotTo(HaveOccurred(), "a refused apply leaves the change waiting")
			Expect(got.Route.Channel).To(Equal("G1"))
		})
		Context("a change that leaves the destination as it is", func() {
			var live string
			BeforeEach(func() {
				tr := r.schedule(studio.TriggerInput{Name: "Reports", Prompt: "p",
					Route: triggers.Route{Connection: chatB.ID, Channel: "G1", Target: triggers.TargetChannel}})
				live = tr.ID
				edit(func(b *connectors.Binding) { b.Enabled = false })
			})
			ask := func(args string) string {
				res := c.call(r.trigRig, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,%s}`, studio.TriggerRef(live), args))
				Expect(res.Err).To(BeNil(), "%+v", res)
				return lastCard(r.trigRig, chat).ID
			}
			It("renames while the connection is switched off", func() {
				got, err := r.host.ApplyChange(ctx, alice, ask(`"name":"Renamed"`))
				Expect(err).NotTo(HaveOccurred())
				Expect(got.Name).To(Equal("Renamed"))
				Expect(got.Route.Connection).To(Equal(chatB.ID))
			})
			It("pauses while the connection is switched off, and the undo of the pause works too", func() {
				id := ask(`"enabled":false`)
				paused, err := r.host.GetTrigger(ctx, alice, live)
				Expect(err).NotTo(HaveOccurred())
				Expect(paused.Enabled).To(BeFalse(), "a pause needs no answer")
				back, err := r.host.UndoChange(ctx, alice, id)
				Expect(err).NotTo(HaveOccurred())
				Expect(back.Enabled).To(BeTrue())
				Expect(back.Route.Connection).To(Equal(chatB.ID))
			})
		})
		Context("an undo that would bring back a route to a connection", func() {
			var id string
			BeforeEach(func() {
				tr := r.schedule(studio.TriggerInput{Name: "Reports", Prompt: "p",
					Route: triggers.Route{Connection: chatB.ID, Channel: "G1", Target: triggers.TargetChannel}})
				res := c.call(r.trigRig, chat, "update_trigger", fmt.Sprintf(`{"ref":%q,"delivery":{"target":"inbox"}}`, studio.TriggerRef(tr.ID)))
				Expect(res.Err).To(BeNil(), "%+v", res)
				id = lastCard(r.trigRig, chat).ID
				got, err := r.host.ApplyChange(ctx, alice, id)
				Expect(err).NotTo(HaveOccurred())
				Expect(got.Route.TargetOrDefault()).To(Equal(triggers.TargetInbox))
				tid = tr.ID
			})
			It("works while the connection is usable", func() {
				back, err := r.host.UndoChange(ctx, alice, id)
				Expect(err).NotTo(HaveOccurred())
				Expect(back.Route.Connection).To(Equal(chatB.ID))
			})
			It("is refused after the connection was switched off, and leaves the trigger as it is", func() {
				edit(func(b *connectors.Binding) { b.Enabled = false })
				_, err := r.host.UndoChange(ctx, alice, id)
				Expect(err).To(MatchError(ContainSubstring("gone or switched off")))
				live, err := r.host.GetTrigger(ctx, alice, tid)
				Expect(err).NotTo(HaveOccurred())
				Expect(live.Route.TargetOrDefault()).To(Equal(triggers.TargetInbox))
				Expect(live.Route.Connection).To(BeEmpty())
			})
			It("is refused after the channel was un-approved, and leaves the trigger as it is", func() {
				edit(func(b *connectors.Binding) { b.Channels = []connectors.Channel{{ID: "G1", Label: "General"}} })
				_, err := r.host.UndoChange(ctx, alice, id)
				Expect(err).To(MatchError(ContainSubstring("not approved any more")))
				live, err := r.host.GetTrigger(ctx, alice, tid)
				Expect(err).NotTo(HaveOccurred())
				Expect(live.Route.Connection).To(BeEmpty())
			})
		})
	})
})
