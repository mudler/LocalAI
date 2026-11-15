package studio_test

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("changing and deleting triggers from a connector chat", func() {
	ctx := context.Background()
	const (
		chatID = "C1"
		thread = "T1"
	)
	var (
		fake   *postAddon
		r      *connRig
		b      connectors.Binding
		logs   *safeBuf
		on     map[string]bool
		deny22 bool // the sender 22 does not hold triggers.change
		ref    string
		seq    int
	)
	script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
		switch {
		case strings.Contains(text, "ACT-propose"):
			return []studiotest.ToolStep{{Tool: "propose_trigger", Args: `{"name":"Waiting","prompt":"Say hi.","when":"daily 09:00","time_zone":"UTC","delivery":{"target":"origin"}}`}, {Say: "proposed"}}
		case strings.Contains(text, "ACT-update"):
			return []studiotest.ToolStep{{Tool: "update_trigger", Args: `{"ref":"` + ref + `","name":"Renamed"}`}, {Say: "asked"}}
		case strings.Contains(text, "ACT-widen"):
			return []studiotest.ToolStep{{Tool: "update_trigger", Args: `{"ref":"` + ref + `","delivery":{"target":"inbox"}}`}, {Say: "asked"}}
		case strings.Contains(text, "ACT-pause"):
			return []studiotest.ToolStep{{Tool: "update_trigger", Args: `{"ref":"` + ref + `","enabled":false}`}, {Say: "asked"}}
		case strings.Contains(text, "ACT-delete"):
			return []studiotest.ToolStep{{Tool: "delete_trigger", Args: `{"ref":"` + ref + `"}`}, {Say: "asked"}}
		case strings.Contains(text, "ACT-list"):
			return []studiotest.ToolStep{{Tool: "list_triggers", Args: `{}`}, {Say: "listed"}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
	setup := func(ap connectors.Approval) {
		fake = &postAddon{}
		on = map[string]bool{studio.CapTriggersPropose: true, studio.CapTriggersChange: true, studio.CapProposalsConfirm: true}
		deny22 = false
		logs = &safeBuf{}
		sh := newShared()
		sh.deps = func(d *studio.Deps) { d.Logger = slog.New(slog.NewTextHandler(logs, nil)) }
		tr := studiobot.NewTransport(studiobot.Addon{ID: "chat-addon", Kind: contract.ConnectorKind{ID: "chatty", Title: "Chatty", Delivery: contract.DeliveryPull}, Runtime: fake})
		r = newConnRigOpts(sh, connStore(), tr, script, nil, func(o *studio.ConnectorOptions) {
			o.OutboxPoll = 10 * time.Millisecond
			o.RatePerMinute = 100
		})
		r.svc.SetCapabilityGate(studio.CapabilityGateFunc(func(_ context.Context, q studio.CapabilityRequest) bool {
			if q.Capability == studio.CapTriggersChange && deny22 && q.Sender == "22" {
				return false
			}
			if q.Sensitive && deny22 && q.Sender == "22" {
				return false
			}
			return on[q.Capability]
		}))
		b = r.bind(studio.ConnectorInput{Kind: "chatty", Token: "tok", Allow: []string{"11", "22"}, ReplyLanguage: englishReplies(), Approval: &ap})
		seq = 0
		// A live trigger of the agent of the binding.
		tr0 := r.schedule(studio.TriggerInput{AgentID: r.agent.ID, Name: "Digest", Prompt: "Summarise."})
		ref = studio.TriggerRef(tr0.ID)
	}
	say := func(sender, text string) {
		seq++
		Expect(r.conns.Ingest(ctx, "chat-addon", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: contract.ConnectorMessage{
			Chat: chatID, Thread: thread, Mention: true, MessageID: "m" + string(rune('a'+seq)), SenderID: sender, SenderName: "Cy", Text: text}})).To(Succeed())
	}
	// turn sends a message and waits for the answer of the turn. A tool of the
	// turn may already post a notice to the chat (a change that applied at
	// once does), and that text arrives before the tool result is recorded, so
	// the wait is for the closing text that the script says after its tools.
	turn := func(text string) {
		before := len(fake.texts(chatID, thread))
		say("11", text)
		waitFor("the answer", func() bool {
			for _, t := range fake.texts(chatID, thread)[before:] {
				switch strings.TrimSpace(t) {
				case "ok", "asked", "proposed", "listed":
					return true
				}
			}
			return false
		})
	}
	waiting := func() []triggers.Trigger {
		all, err := r.sh.trg.ListTriggers(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		var out []triggers.Trigger
		for _, t := range all {
			if t.IsChange() && t.Outcome == "" {
				out = append(out, t)
			}
		}
		return out
	}
	trigger := func() (triggers.Trigger, error) {
		all, err := r.sh.trg.ListTriggers(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		for _, t := range all {
			if !t.IsChange() && !t.Proposed && t.Name != "" && studio.TriggerRef(t.ID) == ref {
				return t, nil
			}
		}
		return triggers.Trigger{}, triggers.ErrNotFound
	}
	last := func(tool string) studiotest.ToolResult {
		res := r.h.ResultsFor(tool)
		Expect(res).NotTo(BeEmpty())
		return res[len(res)-1]
	}
	// answer writes the code as a sender and waits for the host's reply.
	answer := func(verb string, t triggers.Trigger) string {
		before := len(fake.texts(chatID, thread))
		say("11", verb+" "+t.Change.Code)
		waitFor("the reply", func() bool { return len(fake.texts(chatID, thread)) > before })
		texts := fake.texts(chatID, thread)
		return texts[len(texts)-1]
	}
	toolNames := func() []string {
		l, err := r.svc.List(ctx, alice, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(l).NotTo(BeEmpty())
		return r.h.ToolNames(l[0].ID)
	}

	Describe("the capability triggers.change", func() {
		It("is off by default: update and delete are not offered, and propose and list still are", func() {
			setup(connectors.Approval{})
			on[studio.CapTriggersChange] = false
			turn("hello")
			names := toolNames()
			Expect(names).To(ContainElements("propose_trigger", "list_triggers"))
			Expect(names).NotTo(ContainElement("update_trigger"))
			Expect(names).NotTo(ContainElement("delete_trigger"))
			Expect(contract.KnownCapability("triggers.change")).To(BeTrue())
			Expect(contract.UsualConnectorCapabilities()).NotTo(ContainElement("triggers.change"), "it is never part of the usual preset")
		})

		It("offers update and delete when the connection allows it and the sender holds it", func() {
			setup(connectors.Approval{})
			turn("hello")
			Expect(toolNames()).To(ContainElements("update_trigger", "delete_trigger"))
		})

		It("leaves them out for a sender that does not hold it, with the reason in the trace", func() {
			setup(connectors.Approval{})
			deny22 = true
			before := len(fake.texts(chatID, thread))
			say("22", "hello")
			waitFor("the answer", func() bool { return len(fake.texts(chatID, thread)) > before })
			names := toolNames()
			Expect(names).To(ContainElement("propose_trigger"))
			Expect(names).NotTo(ContainElement("update_trigger"))
			Expect(logs.String()).To(ContainSubstring("update_trigger=not permitted"))
			Expect(logs.String()).To(ContainSubstring("sender_holds_change=false"))
			Expect(logs.String()).To(ContainSubstring("approval_mode=ask"))
		})
	})

	Describe("with the proposal workflow (ask, the default)", func() {
		It("asks the owner to confirm a change, and applies it only on the code", func() {
			setup(connectors.Approval{})
			turn("ACT-update")
			ws := waiting()
			Expect(ws).To(HaveLen(1))
			t := ws[0]
			Expect(t.Change.Code).To(MatchRegexp(`^[A-Z2-9]{4}$`))
			Expect(t.Change.Binding).To(Equal(b.ID))
			Expect(t.Change.Place).To(Equal(chatID))
			Expect(t.Change.Thread).To(Equal(thread))
			texts := fake.texts(chatID, thread)
			Expect(texts[0]).To(ContainSubstring("Proposed change to"))
			Expect(texts[0]).To(ContainSubstring("confirm " + t.Change.Code))
			res := last("update_trigger")
			Expect(res.Err).To(BeNil(), "result = %+v", res)
			Expect(res.Out).To(ContainSubstring("Nothing is changed yet"))
			Expect(res.Out).NotTo(ContainSubstring(t.Change.Code), "the model does not repeat the code")
			Expect(res.Out).To(ContainSubstring(`"change":"pending"`))
			cur, err := trigger()
			Expect(err).NotTo(HaveOccurred())
			Expect(cur.Name).To(Equal("Digest"), "nothing changes before the answer")

			reply := answer("confirm", t)
			Expect(reply).To(ContainSubstring("Changed"))
			cur, err = trigger()
			Expect(err).NotTo(HaveOccurred())
			Expect(cur.Name).To(Equal("Renamed"))
			Expect(logs.String()).To(ContainSubstring("mode=ask"))
		})

		It("leaves the trigger as it is on cancel", func() {
			setup(connectors.Approval{})
			turn("ACT-update")
			t := waiting()[0]
			Expect(answer("cancel", t)).To(ContainSubstring("Cancelled"))
			cur, _ := trigger()
			Expect(cur.Name).To(Equal("Digest"))
			Expect(waiting()).To(BeEmpty())
		})

		It("asks before a pause, and pauses on confirm", func() {
			setup(connectors.Approval{})
			turn("ACT-pause")
			cur, _ := trigger()
			Expect(cur.Enabled).To(BeTrue(), "a pause waits for the owner here")
			t := waiting()[0]
			Expect(t.Change.Op).To(Equal(triggers.ChangePause))
			Expect(fake.texts(chatID, thread)[0]).To(ContainSubstring("Pause"))
			Expect(answer("confirm", t)).To(ContainSubstring("Paused"))
			cur, _ = trigger()
			Expect(cur.Enabled).To(BeFalse())
		})

		It("asks before a delete, and deletes on confirm", func() {
			setup(connectors.Approval{})
			turn("ACT-delete")
			_, err := trigger()
			Expect(err).NotTo(HaveOccurred(), "still there")
			t := waiting()[0]
			Expect(t.Change.Op).To(Equal(triggers.ChangeDelete))
			res := last("delete_trigger")
			Expect(res.Out).To(ContainSubstring(`"state":"deleted_pending"`))
			Expect(answer("confirm", t)).To(ContainSubstring("Deleted"))
			_, err = trigger()
			Expect(err).To(MatchError(triggers.ErrNotFound))
		})

		It("asks before it withdraws a waiting proposal, and withdraws it on confirm", func() {
			setup(connectors.Approval{})
			turn("ACT-propose")
			ps, err := r.host.Proposals(ctx, alice)
			Expect(err).NotTo(HaveOccurred())
			Expect(ps).To(HaveLen(1))
			ref = studio.TriggerRef(ps[0].ID)
			turn("ACT-delete")
			ps, _ = r.host.Proposals(ctx, alice)
			Expect(ps).To(HaveLen(1), "the proposal waits until the owner answers")
			t := waiting()[0]
			Expect(answer("confirm", t)).To(ContainSubstring("Withdrew the proposal"))
			ps, _ = r.host.Proposals(ctx, alice)
			Expect(ps).To(BeEmpty())
		})

		It("refuses a confirmation from a sender who may not confirm", func() {
			setup(connectors.Approval{})
			turn("ACT-update")
			t := waiting()[0]
			on[studio.CapProposalsConfirm] = false
			Expect(answer("confirm", t)).NotTo(ContainSubstring("Changed"))
			cur, _ := trigger()
			Expect(cur.Name).To(Equal("Digest"))
		})

		It("lapses after 24 hours", func() {
			setup(connectors.Approval{})
			turn("ACT-update")
			t := waiting()[0]
			r.sh.clock.Advance(25 * time.Hour)
			Expect(answer("confirm", t)).To(ContainSubstring("expired"))
			cur, _ := trigger()
			Expect(cur.Name).To(Equal("Digest"))
		})

		It("does not touch a trigger of another agent", func() {
			setup(connectors.Approval{})
			other := r.schedule(studio.TriggerInput{Name: "Mine", Prompt: "p"})
			ref = studio.TriggerRef(other.ID)
			turn("ACT-update")
			Expect(last("update_trigger").Err).To(HaveOccurred())
			Expect(waiting()).To(BeEmpty())
		})
	})

	Describe("with allow all (approval auto)", func() {
		auto := connectors.Approval{Mode: connectors.ApprovalAuto}

		It("applies a change at once, tells the chat, and proves it", func() {
			setup(auto)
			turn("ACT-update")
			cur, _ := trigger()
			Expect(cur.Name).To(Equal("Renamed"))
			Expect(waiting()).To(BeEmpty())
			res := last("update_trigger")
			Expect(res.Out).To(ContainSubstring("at once, under the owner's settings"))
			Expect(res.Out).To(ContainSubstring(`"state":"active"`))
			Expect(strings.Join(fake.texts(chatID, thread), "\n")).To(ContainSubstring("Changed"))
			Expect(logs.String()).To(ContainSubstring("mode=auto"))
			Expect(logs.String()).To(ContainSubstring("approval_mode=auto"))
		})

		It("pauses and deletes at once", func() {
			setup(auto)
			turn("ACT-pause")
			cur, _ := trigger()
			Expect(cur.Enabled).To(BeFalse())
			Expect(last("update_trigger").Out).To(ContainSubstring(`"state":"paused"`))
			turn("ACT-delete")
			_, err := trigger()
			Expect(err).To(MatchError(triggers.ErrNotFound))
			Expect(last("delete_trigger").Out).To(ContainSubstring("Deleted"))
		})

		It("withdraws a waiting proposal at once", func() {
			setup(auto)
			// A proposal made with the proposal workflow of the same binding is
			// the one that waits; here the trigger is proposed by the host call.
			p, err := r.host.ProposeTrigger(ctx, alice, studio.TriggerInput{Name: "Waiting", Prompt: "p", Kind: triggers.KindSchedule,
				Schedule: "daily 09:00", TZ: "UTC", Model: "m", AgentID: r.agent.ID})
			Expect(err).NotTo(HaveOccurred())
			ref = studio.TriggerRef(p.ID)
			turn("ACT-delete")
			ps, _ := r.host.Proposals(ctx, alice)
			Expect(ps).To(BeEmpty())
			res := last("delete_trigger")
			Expect(res.Out).To(ContainSubstring("Withdrew the proposal"))
			Expect(res.Out).To(ContainSubstring(`"state":"withdrawn"`))
		})

		It("still asks for a change that widens acts", func() {
			setup(auto)
			// A trigger that this chat made at once, so it has a messaging place.
			turn("ACT-propose")
			all, err := r.sh.trg.ListTriggers(ctx, alice)
			Expect(err).NotTo(HaveOccurred())
			for _, t := range all {
				if t.Name == "Waiting" && !t.IsChange() {
					ref = studio.TriggerRef(t.ID)
				}
			}
			turn("ACT-widen")
			Expect(last("update_trigger").Err).To(BeNil())
			Expect(last("update_trigger").Out).To(ContainSubstring("Asked the person to confirm"))
			Expect(waiting()).To(HaveLen(1))
		})
	})

	Describe("the server switch", func() {
		It("names triggers.change among the capabilities it takes away", func() {
			Expect(contract.ConnectorCapabilityNames()).To(ContainElement("triggers.change"))
		})
	})
})
