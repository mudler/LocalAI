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
		l, err := r.svc.List(ctx, alice, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(l).NotTo(BeEmpty())
		raw := r.h.SentRaw(l[0].ID)
		return r.h.ToolNames(l[0].ID), raw[len(raw)-1]
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
		// The live case: nothing ticked on the messaging binding.
		Entry("no permission ticked on the messaging binding (the live case): write tool withheld, read stays, the model is told why",
			func() { pairSender("11") }, "11", false, true, "Held back from this sender", "create-issue", "change things in other services"),
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
