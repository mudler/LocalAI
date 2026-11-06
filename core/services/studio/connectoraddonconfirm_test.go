package studio_test

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	addontools "github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A write of an add-on that waits because the connection asks first is
// written in the chat where it was asked for, and a sender who is paired with
// the bot answers it there. The add-on and the platform are stand-ins.
var _ = Describe("a held write of an add-on in a messaging chat", func() {
	ctx := context.Background()
	const (
		tool   = "addon__office__create-issue"
		asker  = "11" // on the allowlist, not paired; holds tools.write
		paired = "12"
		member = "13" // writes in a listed group, is not on the allowlist
		anyone = "99"
		group  = "-100"
	)
	var (
		sh   *shared
		bot  *scriptedBot
		r    *connRig
		b    connectors.Binding
		held addontools.HeldProposal
	)
	script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
		if strings.HasPrefix(text, "open") {
			return []studiotest.ToolStep{{Tool: tool, Args: `{"title":"Broken build"}`}, {Say: "I asked."}}
		}
		return []studiotest.ToolStep{{Say: "model"}}
	}
	caps := []string{contract.CapToolsWrite, contract.CapProposalsConfirm}
	setupWith := func(binCaps []string, pairedIDs ...string) {
		sh, bot = newShared(), newScriptedBot()
		sh.deps = func(d *studio.Deps) { d.Clock = sh.clock.Now }
		schema := json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}}}`)
		held = addontools.HeldProposal{ID: "op1", Digest: "digest-1", AddonID: "office", Tool: tool, Title: "Open an issue", Hold: "write_mode_propose",
			ExpiresAt: sh.clock.Now().Add(24 * time.Hour), Resource: "acme/widgets", Facts: []contract.Fact{{Key: "title", Value: "Broken build @channel"}}}
		sh.addons.tools[alice] = append(sh.addons.tools[alice],
			studio.AddonTool{Name: tool, AddonID: "office", Tool: "create-issue", Description: "opens an issue", Schema: schema, Permission: contract.CapToolsWrite})
		sh.addons.held, sh.addons.heldTool = &held, tool
		addon := studiobot.Telegram(&transport.API{BaseURL: bot.srv.URL, Client: bot.srv.Client()})
		addon.Kind.Permissions = caps
		r = newConnRigWith(sh, connStore(), studiobot.NewTransport(addon), script, func(context.Context) bool { return false })
		r.bot, r.untrusted = bot, true
		r.svc.SetCapabilityGate(r.conns.CapabilityGate())
		b = r.bind(studio.ConnectorInput{Allow: []string{asker, paired, group}, Caps: binCaps})
		got, err := r.store.GetBinding(ctx, b.ID)
		Expect(err).NotTo(HaveOccurred())
		for _, id := range pairedIDs {
			got.Paired = append(got.Paired, connectors.PairedSender{SenderID: id, At: sh.clock.Now()})
		}
		Expect(r.store.PutBinding(ctx, got)).To(Succeed())
		b = got
	}
	setup := func() { setupWith(caps, paired) }
	// sayIn writes in the group and waits for n messages of the bot.
	sayIn := func(sender, text string, n int) []string {
		before := len(bot.answers())
		Expect(r.conns.Ingest(ctx, "telegram", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: contract.ConnectorMessage{
			Chat: group, Mention: true, SenderID: sender, SenderName: "S", Text: text}})).To(Succeed())
		waitFor("the answers", func() bool { return len(bot.answers()) >= before+n })
		sh.clock.Advance(15 * time.Second)
		return bot.answers()[before:]
	}
	code := func(proposal string) string {
		i := strings.Index(proposal, `"confirm `)
		Expect(i).To(BeNumerically(">", 0), proposal)
		return proposal[i+len(`"confirm `) : i+len(`"confirm `)+4]
	}
	toolResult := func() string {
		res := r.h.ResultsFor(tool)
		Expect(res).NotTo(BeEmpty())
		return res[len(res)-1].Out
	}

	It("is written in the chat with what it does, and the model is told it waits there", func() {
		setup()
		out := sayIn(asker, "open", 2)
		Expect(out[0]).To(ContainSubstring("Waiting for your confirmation: Open an issue (acme/widgets)"))
		Expect(out[0]).To(ContainSubstring("- title: Broken build"))
		Expect(out[0]).NotTo(ContainSubstring("@channel"), "mentions of everyone are broken")
		Expect(out[0]).To(ContainSubstring(`Reply "yes" to do it or "no" to drop it`))
		Expect(out[1]).To(Equal("I asked."))
		res := toolResult()
		Expect(res).To(ContainSubstring("waiting for confirmation in this chat, code " + code(out[0])))
		Expect(res).NotTo(ContainSubstring("web UI"))
		Expect(sh.addons.confirmed).To(BeEmpty())
	})

	DescribeTable("who answers, and what the answer does",
		func(sender, text string, wantPrefix string, confirmed, cancelled int, mutate func()) {
			setup()
			sayIn(asker, "open", 2)
			if mutate != nil {
				mutate()
			}
			out := sayIn(sender, text, 1)
			Expect(out[0]).To(HavePrefix(wantPrefix))
			Expect(sh.addons.confirmed).To(HaveLen(confirmed))
			Expect(sh.addons.cancelled).To(HaveLen(cancelled))
		},
		Entry("paired with the capability says yes: it runs", paired, "yes", "Done: Open an issue (acme/widgets). https://example.test/issues/7 It cannot be undone from here.", 1, 0, nil),
		Entry("paired says no: it is dropped", paired, "no", "Cancelled. Nothing was sent.", 0, 1, nil),
		Entry("the listed sender who asked cannot confirm", asker, "yes", "Only a person the owner verified with this bot can answer this.", 0, 0, nil),
		Entry("a member of the listed group cannot confirm", member, "yes", "Only a person the owner verified with this bot can answer this.", 0, 0, nil),
		Entry("anyone, on a bot that accepts anyone, cannot confirm", anyone, "yes", "Only a person the owner verified with this bot can answer this.", 0, 0, func() {
			got, err := r.store.GetBinding(ctx, b.ID)
			Expect(err).NotTo(HaveOccurred())
			got.AcceptAny = true
			Expect(r.store.PutBinding(ctx, got)).To(Succeed())
		}),
		Entry("a sender whose pairing is gone cannot confirm", paired, "yes", "Only a person the owner verified with this bot can answer this.", 0, 0, func() {
			got, err := r.store.GetBinding(ctx, b.ID)
			Expect(err).NotTo(HaveOccurred())
			got.Paired = nil
			Expect(r.store.PutBinding(ctx, got)).To(Succeed())
		}),
	)

	It("is answered by confirm and the code, in any case, and a wrong code is an ordinary message", func() {
		setup()
		out := sayIn(asker, "open", 2)
		c := code(out[0])
		Expect(sayIn(paired, "yes ZZZZ", 1)[0]).To(Equal("model"))
		Expect(sh.addons.confirmed).To(BeEmpty())
		Expect(sayIn(paired, "Confirm "+strings.ToLower(c), 1)[0]).To(HavePrefix("Done:"))
		Expect(sh.addons.confirmed).To(Equal([]string{"op1"}))
	})

	It("runs once: a second yes does nothing", func() {
		setup()
		sayIn(asker, "open", 2)
		Expect(sayIn(paired, "yes", 1)[0]).To(HavePrefix("Done:"))
		Expect(sayIn(paired, "yes", 1)[0]).To(Equal("model"))
		Expect(sh.addons.confirmed).To(HaveLen(1))
	})

	It("says it expired, soon after, and then lets the word through", func() {
		setup()
		out := sayIn(asker, "open", 2)
		c := code(out[0])
		sh.clock.Advance(24*time.Hour + time.Minute)
		Expect(sayIn(paired, "yes", 1)[0]).To(Equal("That proposal expired. Ask again if you still want it."))
		Expect(sayIn(paired, "confirm "+c, 1)[0]).To(Equal("That proposal expired. Ask again if you still want it."))
		Expect(sh.addons.confirmed).To(BeEmpty())
		sh.clock.Advance(2 * time.Hour)
		Expect(sayIn(paired, "yes", 1)[0]).To(Equal("model"))
	})

	It("refuses an answer from another place and keeps waiting", func() {
		setup()
		out := sayIn(asker, "open", 2)
		c := code(out[0])
		before := len(bot.answers())
		Expect(r.conns.Ingest(ctx, "telegram", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: contract.ConnectorMessage{
			Chat: paired, Direct: true, SenderID: paired, SenderName: "S", Text: "confirm " + c}})).To(Succeed())
		waitFor("the answer", func() bool { return len(bot.answers()) > before })
		Expect(bot.answers()[before]).To(Equal("Answer in the same place where it was proposed, or open Studio."))
		Expect(sh.addons.confirmed).To(BeEmpty())
		Expect(sayIn(paired, "yes", 1)[0]).To(HavePrefix("Done:"))
	})

	It("asks which when two wait, and tells them apart by code", func() {
		setup()
		out1 := sayIn(asker, "open", 2)
		sh.addons.held.ID = "op2"
		out2 := sayIn(asker, "open again", 2)
		c1, c2 := code(out1[0]), code(out2[0])
		Expect(c1).NotTo(Equal(c2))
		multi := sayIn(paired, "yes", 1)[0]
		Expect(multi).To(ContainSubstring(c1))
		Expect(multi).To(ContainSubstring(c2))
		Expect(sh.addons.confirmed).To(BeEmpty())
		Expect(sayIn(paired, "cancel "+c1, 1)[0]).To(HavePrefix("Cancelled."))
		Expect(sh.addons.cancelled).To(Equal([]string{"op1"}))
		Expect(sayIn(paired, "yes", 1)[0]).To(HavePrefix("Done:"))
		Expect(sh.addons.confirmed).To(Equal([]string{"op2"}))
	})

	It("says the plain reason when the service refuses", func() {
		setup()
		sh.addons.heldErr = &studio.AddonHoldFailure{Reason: "upstream_403", Status: 403}
		sayIn(asker, "open", 2)
		out := sayIn(paired, "yes", 1)[0]
		Expect(out).To(HavePrefix("I could not do that: The service refused the call (403)"))
	})

	It("says nothing was done when the owner already answered in the web UI", func() {
		setup()
		sayIn(asker, "open", 2)
		sh.addons.heldState = map[string]string{"op1": "applied"}
		Expect(sayIn(paired, "yes", 1)[0]).To(Equal("That was already answered, in the web UI or by someone else."))
		Expect(sh.addons.confirmed).To(BeEmpty())
	})

	It("stops asking in the chat when too many wait", func() {
		setup()
		for i := 0; i < studio.MaxWaitingHolds; i++ {
			sh.addons.held.ID = "op" + string(rune('a'+i))
			sayIn(asker, "open", 2)
		}
		sh.addons.held.ID = "opz"
		sayIn(asker, "open", 1)
		Expect(toolResult()).NotTo(ContainSubstring("in this chat, code"))
		Expect(toolResult()).NotTo(ContainSubstring("in this chat, code"))
	})

	It("is not written in the chat when the bot has no confirm capability, or nobody is paired", func() {
		setupWith([]string{contract.CapToolsWrite}, paired)
		sayIn(asker, "open", 1)
		Expect(toolResult()).NotTo(ContainSubstring("in this chat, code"))
		setupWith(caps)
		sayIn(asker, "open", 1)
		Expect(toolResult()).NotTo(ContainSubstring("in this chat, code"))
	})

	It("is not written in the chat for a tool that always asks", func() {
		setup()
		sh.addons.held.Hold = "approval_always"
		sayIn(asker, "open", 1)
		Expect(toolResult()).NotTo(ContainSubstring("in this chat, code"))
	})

	It("shows waiting, then confirmed, in the latest decisions of the connection", func() {
		setup()
		sayIn(asker, "open", 2)
		v, err := r.conns.Access(ctx, alice, b.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.ToolOutcomes[0].Outcome).To(Equal("held"))
		Expect(v.ToolOutcomes[0].Reason).To(Equal("waiting_in_chat"))
		sayIn(paired, "yes", 1)
		v, err = r.conns.Access(ctx, alice, b.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.ToolOutcomes[0].Outcome).To(Equal("confirmed"))
		Expect(v.ToolOutcomes[0].Reason).To(Equal("chat_confirmed"))
		Expect(v.ToolOutcomes[0].Senders).To(Equal([]string{paired}))
		Expect(v.ToolOutcomes[0].Asker).To(Equal(asker))
	})
})
