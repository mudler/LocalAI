package studio_test

import (
	"context"
	"slices"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A tool exists in a connector chat when the right of the sender on the binding,
// the builtin grants of the agent and the chat switch all allow it. These specs
// run whole turns: they look at the tools the model is offered, and at what
// happens when it calls a tool that it should not have. The platform in them is
// made up; nothing depends on which add-on serves it.
var _ = Describe("builtin grants in the turns of a connector chat", func() {
	ctx := context.Background()
	const (
		chatID  = "C1"
		thread  = "T1"
		general = "G1"
		ops     = "G2"
	)
	var (
		fake *postAddon
		r    *connRig
		b    connectors.Binding
		args string
		seq  int
	)
	script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
		switch text {
		case "send":
			return []studiotest.ToolStep{{Tool: "send_message", Args: args}, {Say: "done"}}
		case "comment":
			return []studiotest.ToolStep{{Tool: "addon__office__comment", Args: `{}`}, {Say: "done"}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
	setup := func() {
		fake = &postAddon{}
		sh := newShared()
		// One read tool and one write tool of an add-on that holds a credential:
		// the write tool declares the capability that it needs.
		write := addonTool("office", "comment")
		write.Action, write.Permission = true, contract.CapToolsWrite
		sh.addons.tools[alice] = append(sh.addons.tools[alice], write)
		tr := studiobot.NewTransport(studiobot.Addon{ID: "chat-addon", Kind: contract.ConnectorKind{ID: "chatty", Title: "Chatty", Delivery: contract.DeliveryPull,
			Roles: []contract.ConnectorRole{{ID: "writer", Title: "Writer"}}, Permissions: []string{contract.CapToolsWrite}}, Runtime: fake})
		r = newConnRigOpts(sh, connStore(), tr, script, nil, func(o *studio.ConnectorOptions) { o.OutboxPoll = 10 * time.Millisecond })
		// The real gate, as the application sets it.
		r.svc.SetCapabilityGate(r.conns.CapabilityGate())
		b = r.bind(studio.ConnectorInput{Kind: "chatty", Token: "tok", Allow: []string{"11", "12"},
			Channels: []connectors.Channel{{ID: general, Label: "General", Approved: true}, {ID: ops, Label: "Ops"}}})
		seq = 0
	}
	// grants sets the builtin grants of the agent of the binding. No argument
	// clears the list.
	grants := func(names ...string) {
		ag := r.agent
		ag.Builtins = append([]string{}, names...)
		saved, err := r.svc.SaveAgent(ctx, alice, ag)
		Expect(err).NotTo(HaveOccurred())
		r.agent = saved
	}
	say := func(sender, text string, media ...string) {
		seq++
		Expect(r.conns.Ingest(ctx, "chat-addon", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: contract.ConnectorMessage{
			Chat: chatID, Thread: thread, Mention: true, MessageID: "m" + string(rune('a'+seq)), SenderID: sender, SenderName: "S", Text: text, Media: media}})).To(Succeed())
	}
	turn := func(text string) {
		before := len(fake.texts(chatID, thread))
		say("11", text)
		waitFor("the answer", func() bool { return len(fake.texts(chatID, thread)) > before })
		// The answer is out before the turn and the reply are over. A change of
		// grants or a message in that gap meets the turn that still runs, and the
		// tools of the next turn would not be the ones this spec reads.
		waitFor("the reply to end", func() bool {
			l, err := r.svc.List(ctx, alice, 10)
			Expect(err).NotTo(HaveOccurred())
			for _, c := range l {
				info, err := r.svc.Turn(ctx, alice, c.ID)
				Expect(err).NotTo(HaveOccurred())
				if info.Running {
					return false
				}
			}
			return !r.conns.RepliesActiveForTest()
		})
	}
	tools := func() []string {
		l, err := r.svc.List(ctx, alice, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(l).NotTo(BeEmpty())
		return r.h.ToolNames(l[0].ID)
	}
	lastResult := func(tool string) studiotest.ToolResult {
		res := r.h.ResultsFor(tool)
		Expect(res).NotTo(BeEmpty())
		return res[len(res)-1]
	}
	denied := func(capability string) bool {
		v, err := r.conns.Access(ctx, alice, b.ID)
		Expect(err).NotTo(HaveOccurred())
		return slices.ContainsFunc(v.Recent, func(e studio.AccessEvent) bool { return e.Capability == capability && !e.Allowed })
	}
	triggerTools := []string{"propose_trigger", "list_triggers"}

	Describe("an agent with no grant list", func() {
		It("keeps the tools that are not sensitive", func() {
			setup()
			turn("hello")
			Expect(tools()).To(ContainElement("send_message"))
			Expect(tools()).To(ContainElement("propose_trigger"))
		})

		It("does not start a conversation on a channel the owner did not approve, which is sensitive", func() {
			setup()
			args = `{"text":"first","channel":"` + ops + `"}`
			turn("send")
			Expect(lastResult("send_message").Err).To(MatchError(ContainSubstring("not permitted")))
			Expect(fake.texts(ops, "")).To(BeEmpty())
			Expect(denied(contract.CapTalkbackNewChan)).To(BeTrue(), "the refusal is audited")
		})
	})

	Describe("an agent with a grant list", func() {
		It("offers a tool only for the grants on the list", func() {
			setup()
			grants(contract.CapMemoryWrite)
			turn("hello")
			Expect(tools()).NotTo(ContainElement("send_message"))
			for _, n := range triggerTools {
				Expect(tools()).NotTo(ContainElement(n), n)
			}
			grants(contract.CapTalkbackChannels)
			turn("hello")
			Expect(tools()).To(ContainElement("send_message"))
			Expect(tools()).NotTo(ContainElement("propose_trigger"))
			grants(contract.CapTriggersPropose)
			turn("hello")
			Expect(tools()).To(ContainElement("propose_trigger"))
			Expect(tools()).NotTo(ContainElement("send_message"))
		})

		It("refuses a call to a tool that is not offered", func() {
			setup()
			grants(contract.CapTriggersPropose)
			args = `{"text":"hi"}`
			turn("send")
			Expect(lastResult("send_message").Err).To(MatchError(ContainSubstring("no tool named")))
			Expect(fake.texts(chatID, thread)).NotTo(ContainElement("hi"))
		})

		It("keeps a new channel closed until the sensitive grant is on the list", func() {
			setup()
			grants(contract.CapTalkbackChannels)
			args = `{"text":"first","channel":"` + ops + `"}`
			turn("send")
			Expect(lastResult("send_message").Err).To(MatchError(ContainSubstring("not permitted")))
			Expect(fake.texts(ops, "")).To(BeEmpty())
			// The approved channel is still open to the same agent.
			args = `{"text":"fine","channel":"` + general + `"}`
			turn("send")
			waitFor("the message", func() bool { return len(fake.texts(general, "")) == 1 })
			grants(contract.CapTalkbackChannels, contract.CapTalkbackNewChan)
			args = `{"text":"first","channel":"` + ops + `"}`
			turn("send")
			waitFor("the message", func() bool { return len(fake.texts(ops, "")) == 1 })
		})

		It("takes the tool away on the next turn when the owner removes the grant", func() {
			setup()
			grants(contract.CapTalkbackChannels)
			turn("hello")
			Expect(tools()).To(ContainElement("send_message"))
			grants(contract.CapMemoryWrite)
			turn("hello")
			Expect(tools()).NotTo(ContainElement("send_message"))
		})

		It("refuses a file from a sender when files.upload is not on the list", func() {
			setup()
			grants(contract.CapTalkbackChannels)
			say("11", "here", "file-1")
			waitFor("the refusal", func() bool {
				return slices.Contains(fake.texts(chatID, thread), "The owner has not turned that on for this bot.")
			})
			Expect(denied(contract.CapFilesUpload)).To(BeTrue())
		})

		It("does not widen what the binding gives the sender", func() {
			setup()
			grants(contract.CapTalkbackChannels, contract.CapTalkbackNewChan, contract.CapTriggersPropose)
			bb, err := r.store.GetBinding(ctx, b.ID)
			Expect(err).NotTo(HaveOccurred())
			bb.Caps = []string{contract.CapMemoryWrite}
			Expect(r.store.PutBinding(ctx, bb)).To(Succeed())
			turn("hello")
			Expect(tools()).NotTo(ContainElement("send_message"))
			Expect(tools()).NotTo(ContainElement("propose_trigger"))
		})
	})

	Describe("the write tools of an add-on that holds a credential", func() {
		It("are offered to a paired sender and run", func() {
			setup()
			turn("comment")
			Expect(tools()).To(ContainElement("addon__office__comment"))
			Expect(tools()).To(ContainElement("addon__office__read-doc"))
			Expect(lastResult("addon__office__comment").Err).NotTo(HaveOccurred())
		})

		It("are not offered when the owner did not enable tools.write for the bot, and the read tool stays", func() {
			setup()
			bb, err := r.store.GetBinding(ctx, b.ID)
			Expect(err).NotTo(HaveOccurred())
			bb.Caps = slices.DeleteFunc(slices.Clone(bb.Caps), func(c string) bool { return c == contract.CapToolsWrite })
			Expect(r.store.PutBinding(ctx, bb)).To(Succeed())
			turn("comment")
			Expect(tools()).NotTo(ContainElement("addon__office__comment"))
			Expect(tools()).To(ContainElement("addon__office__read-doc"))
			Expect(lastResult("addon__office__comment").Err).To(MatchError(ContainSubstring("no tool named")))
		})

		It("are offered to a sender on the allowlist who is not paired and holds no role", func() {
			setup()
			bb, _ := r.store.GetBinding(ctx, b.ID)
			bb.Paired = slices.DeleteFunc(bb.Paired, func(p connectors.PairedSender) bool { return p.SenderID == "12" })
			Expect(r.store.PutBinding(ctx, bb)).To(Succeed())
			say("12", "comment")
			waitFor("the answer", func() bool { return len(fake.texts(chatID, thread)) == 1 })
			Expect(tools()).To(ContainElement("addon__office__comment"))
		})

		It("are not offered to a sender who only came in through a listed channel, and is not paired and holds no role", func() {
			setup()
			say("12", "hello")
			waitFor("the answer", func() bool { return len(fake.texts(chatID, thread)) == 1 })
			bb, _ := r.store.GetBinding(ctx, b.ID)
			bb.Paired = slices.DeleteFunc(bb.Paired, func(p connectors.PairedSender) bool { return p.SenderID == "12" })
			bb.Allow = []string{chatID}
			Expect(r.store.PutBinding(ctx, bb)).To(Succeed())
			say("12", "comment")
			waitFor("the answer", func() bool { return len(fake.texts(chatID, thread)) == 2 })
			l, _ := r.svc.List(ctx, alice, 10)
			for _, c := range l {
				Expect(r.h.ToolNames(c.ID)).NotTo(ContainElement("addon__office__comment"))
			}
		})

		It("are offered to a role that the owner granted tools.write, because the kind lists it", func() {
			setup()
			bb, _ := r.store.GetBinding(ctx, b.ID)
			bb.Paired = nil
			bb.Grants = []connectors.RoleGrant{{Role: "writer", Caps: []string{contract.CapToolsWrite}}}
			Expect(r.store.PutBinding(ctx, bb)).To(Succeed())
			seq++
			Expect(r.conns.Ingest(ctx, "chat-addon", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: contract.ConnectorMessage{
				Chat: chatID, Thread: thread, Mention: true, MessageID: "mr", SenderID: "11", SenderName: "S", Text: "comment", Roles: []string{"writer"}}})).To(Succeed())
			waitFor("the answer", func() bool { return len(fake.texts(chatID, thread)) == 1 })
			Expect(tools()).To(ContainElement("addon__office__comment"))
		})

		It("are not offered to a role when the binding did not grant it", func() {
			setup()
			bb, _ := r.store.GetBinding(ctx, b.ID)
			bb.Paired, bb.Grants = nil, nil
			bb.Allow = []string{chatID}
			Expect(r.store.PutBinding(ctx, bb)).To(Succeed())
			Expect(r.conns.Ingest(ctx, "chat-addon", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: contract.ConnectorMessage{
				Chat: chatID, Thread: thread, Mention: true, MessageID: "mr", SenderID: "11", SenderName: "S", Text: "comment", Roles: []string{"writer"}}})).To(Succeed())
			waitFor("the answer", func() bool { return len(fake.texts(chatID, thread)) == 1 })
			Expect(tools()).NotTo(ContainElement("addon__office__comment"))
		})
	})

	It("keeps the act classes outside the gate, so an agent that holds them gets no new tool", func() {
		setup()
		grants("act.files", "act.addons", "act.send", "act.spend")
		turn("hello")
		Expect(tools()).NotTo(ContainElement("send_message"))
		for _, a := range []string{"act.files", "act.addons", "act.send", "act.spend"} {
			Expect(studio.GrantGatedCapability(a)).To(BeFalse(), a)
		}
	})
})
