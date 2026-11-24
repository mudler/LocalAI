package studio_test

import (
	"context"
	"encoding/json"
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

// An add-on that keeps the owner's own data and has no credential (a diary,
// here; the Calendar add-on in the product). The rule is by shape: its write
// tools need tools.write in a messaging chat, which the owner and paired
// senders hold and every other sender lacks, while its read tool is open to
// every sender the bot lets in.
var _ = Describe("owner data write tools in a messaging chat", func() {
	ctx := context.Background()
	const (
		chatID = "C1"
		thread = "T1"
		write  = "addon__diary__entry-add"
		read   = "addon__diary__entries-list"
	)
	var (
		fake  *postAddon
		r     *connRig
		slack connectors.Binding
	)
	setup := func() {
		fake = &postAddon{}
		sh := newShared()
		schema := json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`)
		sh.addons.tools[alice] = append(sh.addons.tools[alice],
			studio.AddonTool{Name: read, AddonID: "diary", Tool: "entries-list", Description: "reads", Schema: schema, OwnerData: true},
			studio.AddonTool{Name: write, AddonID: "diary", Tool: "entry-add", Description: "adds an entry", Schema: schema, OwnerData: true, Action: true, Permission: contract.CapToolsWrite})
		tr := studiobot.NewTransport(studiobot.Addon{ID: "chat-addon", Kind: contract.ConnectorKind{ID: "chatty", Title: "Chatty", Delivery: contract.DeliveryPull}, Runtime: fake})
		r = newConnRigOpts(sh, connStore(), tr, func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} }, nil,
			func(o *studio.ConnectorOptions) { o.OutboxPoll = 10 * time.Millisecond })
		r.untrusted = true
		r.svc.SetCapabilityGate(r.conns.CapabilityGate())
		ag := r.agent
		ag.Name, ag.Model, ag.Addons = "Planner", "m", []string{"diary"}
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
	ask := func(sender string) []string {
		before := len(fake.texts(chatID, thread))
		Expect(r.conns.Ingest(ctx, "chat-addon", contract.ConnectorInbound{BindingID: slack.ID, ConnectorMessage: contract.ConnectorMessage{
			Chat: chatID, Thread: thread, Mention: true, MessageID: "m" + sender + string(rune('a'+before)), SenderID: sender, SenderName: "S", Text: "add an entry"}})).To(Succeed())
		waitFor("the answer", func() bool { return len(fake.texts(chatID, thread)) > before })
		var chat string
		waitFor("the message to reach the model", func() bool {
			l, err := r.svc.List(ctx, alice, 10)
			Expect(err).NotTo(HaveOccurred())
			if len(l) == 0 {
				return false
			}
			chat = l[0].ID
			return len(r.h.SentRaw(chat)) > 0
		})
		return r.h.ToolNames(chat)
	}

	It("lets a paired sender write with no tick, and every other sender only read", func() {
		setup()
		edit(func(b *connectors.Binding) {
			b.Paired = append(b.Paired, connectors.PairedSender{SenderID: "11", At: r.sh.clock.Now()})
		})
		tools := ask("11")
		Expect(slices.Contains(tools, write)).To(BeTrue(), "paired sender, tools = %v", tools)
		Expect(slices.Contains(tools, read)).To(BeTrue())
	})

	It("keeps the write tool from a sender who is only on the allowlist, and offers the read tool", func() {
		setup()
		tools := ask("12")
		Expect(slices.Contains(tools, write)).To(BeFalse(), "tools = %v", tools)
		Expect(slices.Contains(tools, read)).To(BeTrue(), "tools = %v", tools)
	})

	It("keeps the write tool from a sender let in because the bot accepts anyone", func() {
		setup()
		edit(func(b *connectors.Binding) { b.AcceptAny, b.Allow = true, nil })
		tools := ask("99")
		Expect(slices.Contains(tools, write)).To(BeFalse(), "tools = %v", tools)
		Expect(slices.Contains(tools, read)).To(BeTrue())
	})
	// Allow everyone: the owner ticks tools.write for the bot (the same tick
	// that lets listed senders use the write tools of credentialed add-ons).
	Context("when the owner ticks tools.write for the bot", func() {
		tick := func() {
			edit(func(b *connectors.Binding) { b.Caps = append(b.Caps, contract.CapToolsWrite) })
		}
		It("lets a listed sender write", func() {
			setup()
			tick()
			tools := ask("12")
			Expect(slices.Contains(tools, write)).To(BeTrue(), "tools = %v", tools)
			Expect(slices.Contains(tools, read)).To(BeTrue())
		})
		It("lets a paired sender write", func() {
			setup()
			tick()
			edit(func(b *connectors.Binding) {
				b.Paired = append(b.Paired, connectors.PairedSender{SenderID: "11", At: r.sh.clock.Now()})
			})
			Expect(slices.Contains(ask("11"), write)).To(BeTrue())
		})
		It("lets a sender write who is let in because the bot accepts anyone", func() {
			setup()
			tick()
			edit(func(b *connectors.Binding) { b.AcceptAny, b.Allow = true, nil })
			tools := ask("99")
			Expect(slices.Contains(tools, write)).To(BeTrue(), "tools = %v", tools)
			Expect(slices.Contains(tools, read)).To(BeTrue())
		})
	})
})
