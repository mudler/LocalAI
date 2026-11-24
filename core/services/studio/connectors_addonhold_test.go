package studio_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A saved agent that names an add-on with a credential, used through a
// messaging connection: every gate between "the agent form lists the
// connection" and "the model has the write tool" is tried here, and so is what
// the model hears when a tool is held back. The add-on and the platform are
// stand-ins; the rule is by shape, so nothing depends on a product.
var _ = Describe("add-on write tools in a messaging chat", func() {
	ctx := context.Background()
	const (
		chatID = "C1"
		thread = "T1"
		write  = "addon__tracker__create-issue"
		read   = "addon__tracker__list-issues"
	)
	var (
		fake  *postAddon
		r     *connRig
		slack connectors.Binding
		cred  connectors.Binding
	)
	// setup builds the agent, its connection and the messaging binding of the
	// live case: write mode auto, approval ask, a wildcard resource list, a
	// disabled binding, and a messaging binding that never got the usual
	// permissions.
	setup := func() {
		fake = &postAddon{}
		sh := newShared()
		schema := json.RawMessage(`{"type":"object","properties":{"repo":{"type":"string"},"title":{"type":"string"}},"required":["repo","title"]}`)
		sh.addons.tools[alice] = append(sh.addons.tools[alice],
			studio.AddonTool{Name: read, AddonID: "tracker", Tool: "list-issues", Description: "reads", Schema: schema, Credentialed: true},
			studio.AddonTool{Name: write, AddonID: "tracker", Tool: "create-issue", Description: "opens an issue", Schema: schema, Credentialed: true, Action: true, Permission: contract.CapToolsWrite})
		tr := studiobot.NewTransport(studiobot.Addon{ID: "chat-addon", Kind: contract.ConnectorKind{ID: "chatty", Title: "Chatty", Delivery: contract.DeliveryPull}, Runtime: fake})
		r = newConnRigOpts(sh, connStore(), tr, func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} }, nil,
			func(o *studio.ConnectorOptions) { o.OutboxPoll = 10 * time.Millisecond })
		r.untrusted = true
		r.svc.SetCapabilityGate(r.conns.CapabilityGate())
		cred = connectors.Binding{ID: "tracker-b1", Owner: alice, Kind: "tracker", AddonID: "tracker", Name: "Tracker", Token: []byte("sealed"),
			WriteMode: connectors.WriteAuto, Resources: []connectors.ResourceEntry{{Key: contract.AnyResource, ID: connectors.ResourceID(contract.AnyResource)}},
			Approval: connectors.Approval{Mode: connectors.ApprovalAsk}, Created: r.sh.clock.Now()}
		Expect(r.store.PutBinding(ctx, cred)).To(Succeed())
		ag := r.agent
		ag.Name, ag.Model, ag.Addons = "Support", "m", []string{"tracker"}
		ag.Connections = map[string][]string{"tracker": {cred.ID}}
		saved, err := r.svc.SaveAgent(ctx, alice, ag)
		Expect(err).NotTo(HaveOccurred())
		r.agent = saved
		slack = r.bind(studio.ConnectorInput{Kind: "chatty", Token: "tok", Allow: []string{"11", "12"}})
	}
	edit := func(f func(*connectors.Binding)) {
		b, err := r.store.GetBinding(ctx, slack.ID)
		Expect(err).NotTo(HaveOccurred())
		f(&b)
		Expect(r.store.PutBinding(ctx, b)).To(Succeed())
	}
	// ask is one message from a sender; it returns the tools of the turn and
	// the message the model got, with the note about held back tools.
	ask := func(sender string) (tools []string, got string) {
		before := len(fake.texts(chatID, thread))
		Expect(r.conns.Ingest(ctx, "chat-addon", contract.ConnectorInbound{BindingID: slack.ID, ConnectorMessage: contract.ConnectorMessage{
			Chat: chatID, Thread: thread, Mention: true, MessageID: "m" + sender + string(rune('a'+before)), SenderID: sender, SenderName: "S", Text: "open an issue"}})).To(Succeed())
		waitFor("the answer", func() bool { return len(fake.texts(chatID, thread)) > before })
		// The first text to the thread can be the working note, which goes out
		// before the model gets the message. The model's message is what is read.
		var chat string
		var raw []string
		waitFor("the message to reach the model", func() bool {
			l, err := r.svc.List(ctx, alice, 10)
			Expect(err).NotTo(HaveOccurred())
			if len(l) == 0 {
				return false
			}
			chat = l[0].ID
			raw = r.h.SentRaw(chat)
			return len(raw) > 0
		})
		return r.h.ToolNames(chat), raw[len(raw)-1]
	}
	setMode := func(mode string) {
		cred.WriteMode = mode
		Expect(r.store.PutBinding(ctx, cred)).To(Succeed())
	}
	// saveCaps is the owner's save of the permissions on the page.
	saveCaps := func(caps ...string) {
		_, err := r.conns.UpdateConnector(ctx, alice, slack.ID, studio.ConnectorInput{Name: "Chatty", AgentID: r.agent.ID, Allow: []string{"11", "12"}, Enabled: true, Caps: append([]string{}, caps...)})
		Expect(err).NotTo(HaveOccurred())
	}
	pairSender := func(id string) {
		edit(func(b *connectors.Binding) {
			b.Paired = append(b.Paired, connectors.PairedSender{SenderID: id, At: r.sh.clock.Now()})
		})
	}

	DescribeTable("the gates between the agent form and the tool list",
		func(arrange func(), sender string, offered bool, readOffered bool, noteHas ...string) {
			setup()
			arrange()
			tools, got := ask(sender)
			Expect(slices.Contains(tools, write)).To(Equal(offered), "tools = %v", tools)
			Expect(slices.Contains(tools, read)).To(Equal(readOffered), "tools = %v", tools)
			for _, w := range noteHas {
				Expect(got).To(ContainSubstring(w))
			}
			if offered {
				Expect(got).NotTo(ContainSubstring("create-issue"))
			}
		},
		// The live case: nothing ticked on the messaging binding. The agent is
		// connected to the add-on and the connection is in write mode auto, so
		// the owner and paired senders have the write tool without a tick.
		Entry("nothing ticked, paired sender, agent connected, write mode auto: offered, no note",
			func() { pairSender("11") }, "11", true, true),
		Entry("nothing ticked, paired sender, write mode propose: offered (the proposal still asks)",
			func() { pairSender("11"); setMode(connectors.WritePropose) }, "11", true, true),
		Entry("nothing ticked, paired sender, write mode off: withheld, the model is told why",
			func() { pairSender("11"); setMode(connectors.WriteOff) }, "11", false, true, "Held back from this sender", "create-issue", "change things in other services"),
		Entry("nothing ticked, sender on the allowlist but not paired: withheld",
			func() {}, "12", false, true, "Held back from this sender", "create-issue"),
		Entry("nothing ticked, sender let in only through a listed channel: withheld",
			func() { edit(func(b *connectors.Binding) { b.Allow = []string{chatID} }) }, "12", false, true, "Held back from this sender", "create-issue"),
		Entry("nothing ticked, the bot accepts anyone: withheld",
			func() { edit(func(b *connectors.Binding) { b.AcceptAny, b.Allow = true, nil }) }, "99", false, true, "Held back from this sender", "create-issue"),
		Entry("nothing ticked, paired sender, the agent does not list the add-on: no tool at all",
			func() {
				pairSender("11")
				ag := r.agent
				ag.Addons, ag.Connections = nil, nil
				saved, err := r.svc.SaveAgent(ctx, alice, ag)
				Expect(err).NotTo(HaveOccurred())
				r.agent = saved
			}, "11", false, false),
		Entry("nothing ticked, paired sender, the agent selects no connection (the only one is picked): withheld",
			func() {
				pairSender("11")
				ag := r.agent
				ag.Connections = map[string][]string{}
				saved, err := r.svc.SaveAgent(ctx, alice, ag)
				Expect(err).NotTo(HaveOccurred())
				r.agent = saved
			}, "11", false, true, "Held back from this sender", "create-issue"),
		Entry("the owner ticked tools.write and then took the tick away: the choice holds for the paired sender",
			func() {
				pairSender("11")
				saveCaps(contract.CapToolsWrite)
				saveCaps()
			}, "11", false, true, "Held back from this sender", "create-issue"),
		Entry("the owner ticked, took the tick away and ticked again: offered",
			func() {
				pairSender("11")
				saveCaps(contract.CapToolsWrite)
				saveCaps()
				saveCaps(contract.CapToolsWrite)
			}, "11", true, true),
		Entry("tools.write ticked, sender paired: offered", func() {
			edit(func(b *connectors.Binding) { b.Caps = []string{contract.CapToolsWrite} })
			pairSender("11")
		}, "11", true, true),
		Entry("tools.write ticked, sender on the allowlist but not paired: offered (not a sensitive capability)", func() {
			edit(func(b *connectors.Binding) { b.Caps = []string{contract.CapToolsWrite} })
		}, "12", true, true),
		Entry("tools.write ticked, sender let in only through a listed channel: withheld, the model is told", func() {
			edit(func(b *connectors.Binding) { b.Caps = []string{contract.CapToolsWrite}; b.Allow = []string{chatID} })
		}, "12", false, true, "Held back from this sender", "create-issue"),
		Entry("the credential binding is disabled and in write mode off with approval ask: the tool is still offered (those gate the call, not the list)", func() {
			edit(func(b *connectors.Binding) { b.Caps = []string{contract.CapToolsWrite} })
			cred.WriteMode, cred.Enabled = connectors.WriteOff, false
			Expect(r.store.PutBinding(ctx, cred)).To(Succeed())
		}, "12", true, true),
		Entry("the agent names a connection that no longer exists: both tools withheld, the model is told there is no connection", func() {
			edit(func(b *connectors.Binding) { b.Caps = []string{contract.CapToolsWrite} })
			Expect(r.store.DeleteBinding(ctx, cred.ID)).To(Succeed())
		}, "12", false, false, "Held back from this sender", "tracker", "connection"),
	)

	It("does not let the id of the messaging binding hide the connection of the agent", func() {
		setup()
		rec := session.ChatRecord{Principal: alice, Origin: session.OriginConnector, OriginID: slack.ID, AgentID: r.agent.ID}
		l, err := r.conns.ConnectionsOf(ctx, alice, rec, "tracker")
		Expect(err).NotTo(HaveOccurred())
		Expect(l.IDs()).To(Equal([]string{cred.ID}))
		Expect(l.By).To(Equal(studio.ConnectionByAgent))
	})

	It("shows the owner which tools a missing permission holds back, with the fix, on the access view", func() {
		setup()
		v, err := r.conns.Access(ctx, alice, slack.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.AddonTools).To(HaveLen(1))
		h := v.AddonTools[0]
		Expect(h.Addon).To(Equal("tracker"))
		Expect(h.Capability).To(Equal(contract.CapToolsWrite))
		Expect(h.Tools).To(Equal([]string{"create-issue"}))
		listed := decisionFor(v, "listed", contract.CapToolsWrite)
		Expect(listed.Allowed).To(BeFalse())
		Expect(listed.Code).To(Equal(studio.CodeConnectionPermissionOff))
		Expect(listed.Fix).NotTo(BeNil())
		Expect(strings.TrimSpace(listed.Message)).NotTo(BeEmpty())
		// An agent with no such add-on has nothing to list.
		ag := r.agent
		ag.Addons = nil
		saved, err := r.svc.SaveAgent(ctx, alice, ag)
		Expect(err).NotTo(HaveOccurred())
		r.agent = saved
		v, err = r.conns.Access(ctx, alice, slack.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.AddonTools).To(BeEmpty())
	})
})

var _ = Describe("access view of write tools covered by the agent's connection", func() {
	ctx := context.Background()
	It("says who needs no tick and why, and who still does", func() {
		fake := &postAddon{}
		sh := newShared()
		schema := json.RawMessage(`{"type":"object"}`)
		sh.addons.tools[alice] = append(sh.addons.tools[alice],
			studio.AddonTool{Name: "addon__tracker__create-issue", AddonID: "tracker", Tool: "create-issue", Description: "w", Schema: schema, Credentialed: true, Action: true, Permission: contract.CapToolsWrite})
		tr := studiobot.NewTransport(studiobot.Addon{ID: "chat-addon", Kind: contract.ConnectorKind{ID: "chatty", Title: "Chatty", Delivery: contract.DeliveryPull}, Runtime: fake})
		r := newConnRigOpts(sh, connStore(), tr, func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} }, nil, nil)
		r.untrusted = true
		r.svc.SetCapabilityGate(r.conns.CapabilityGate())
		cred := connectors.Binding{ID: "tracker-b1", Owner: alice, Kind: "tracker", AddonID: "tracker", Name: "Work tracker", Token: []byte("sealed"),
			WriteMode: connectors.WritePropose, Created: sh.clock.Now()}
		Expect(r.store.PutBinding(ctx, cred)).To(Succeed())
		ag := r.agent
		ag.Name, ag.Model, ag.Addons, ag.Connections = "Support", "m", []string{"tracker"}, map[string][]string{"tracker": {cred.ID}}
		saved, err := r.svc.SaveAgent(ctx, alice, ag)
		Expect(err).NotTo(HaveOccurred())
		r.agent = saved
		slack := r.bind(studio.ConnectorInput{Kind: "chatty", Token: "tok", Allow: []string{"11", "12"}})

		v, err := r.conns.Access(ctx, alice, slack.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.AddonTools).To(HaveLen(1))
		Expect(v.AddonTools[0].WithoutTick).To(Equal([]string{studio.SenderPaired}))
		Expect(v.AddonTools[0].Connections).To(Equal([]studio.AddonToolConnection{{Name: "Work tracker", WriteMode: connectors.WritePropose}}))
		d := decisionFor(v, "paired", contract.CapToolsWrite)
		Expect(d.Allowed).To(BeTrue())
		Expect(d.Via).To(Equal(studio.ViaAgentConnection))
		for _, who := range []string{"listed", "anyone"} {
			d := decisionFor(v, who, contract.CapToolsWrite)
			Expect(d.Allowed).To(BeFalse(), who)
			Expect(d.Code).To(Equal(studio.CodeConnectionPermissionOff), who)
		}

		// The preview for a draft that ticks the permission: nothing is covered "without a tick".
		pv, err := r.conns.AccessPreview(ctx, alice, slack.ID, studio.AccessDraft{Caps: []string{contract.CapToolsWrite}})
		Expect(err).NotTo(HaveOccurred())
		Expect(pv.AddonTools[0].WithoutTick).To(BeEmpty())

		// Write mode off: nobody is covered, and the paired sender needs the tick too.
		cred.WriteMode = connectors.WriteOff
		Expect(r.store.PutBinding(ctx, cred)).To(Succeed())
		v, err = r.conns.Access(ctx, alice, slack.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.AddonTools[0].WithoutTick).To(BeEmpty())
		Expect(decisionFor(v, "paired", contract.CapToolsWrite).Allowed).To(BeFalse())
		cred.WriteMode = connectors.WriteAuto
		Expect(r.store.PutBinding(ctx, cred)).To(Succeed())

		// The owner ticks and then takes the tick away: the choice wins, and the draft shows it before the save.
		_, err = r.conns.UpdateConnector(ctx, alice, slack.ID, studio.ConnectorInput{Name: "Chatty", AgentID: r.agent.ID, Allow: []string{"11", "12"}, Enabled: true, Caps: []string{contract.CapToolsWrite}})
		Expect(err).NotTo(HaveOccurred())
		pv, err = r.conns.AccessPreview(ctx, alice, slack.ID, studio.AccessDraft{Caps: []string{}})
		Expect(err).NotTo(HaveOccurred())
		Expect(pv.AddonTools[0].TickRemoved).To(BeTrue())
		Expect(pv.AddonTools[0].WithoutTick).To(BeEmpty())
		_, err = r.conns.UpdateConnector(ctx, alice, slack.ID, studio.ConnectorInput{Name: "Chatty", AgentID: r.agent.ID, Allow: []string{"11", "12"}, Enabled: true, Caps: []string{}})
		Expect(err).NotTo(HaveOccurred())
		v, err = r.conns.Access(ctx, alice, slack.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.AddonTools[0].TickRemoved).To(BeTrue())
		Expect(decisionFor(v, "paired", contract.CapToolsWrite).Allowed).To(BeFalse())
	})
})

func decisionFor(v studio.AccessView, subject, capability string) studio.Decision {
	for _, s := range v.Subjects {
		if s.ID != subject {
			continue
		}
		for _, d := range s.Decisions {
			if d.Capability == capability {
				return d
			}
		}
	}
	Fail("no decision for " + subject + " " + capability)
	return studio.Decision{}
}
