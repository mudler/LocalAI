package studio_test

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The turns of a connector chat follow the permission model: a tool the sender
// may not use is not offered, and a call that gets through anyway is refused
// with the reason. The kind in these specs only declares roles; nothing
// depends on which add-on serves it.
var _ = Describe("permissions in the turns of a connector chat", func() {
	var (
		r *connRig
		b connectors.Binding
	)
	ctx := context.Background()

	script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
		switch text {
		case "remember":
			return []studiotest.ToolStep{{Tool: "propose_change", Args: proposeArgs("memory", "channel", "index.md", "- home: Rome\n", "Where the user lives")}, {Say: "done"}}
		case "profile":
			return []studiotest.ToolStep{{Tool: "propose_change", Args: proposeArgs("profile", "agent", "", "- be brief\n", "A rule")}, {Say: "done"}}
		}
		return []studiotest.ToolStep{{Say: "hello"}}
	}
	// ask sends a direct message from a sender with the given roles and waits
	// for the answer number n.
	n := 0
	ask := func(sender string, roles []string, text string) {
		n++
		Expect(r.conns.Ingest(ctx, "telegram", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: contract.ConnectorMessage{
			Chat: sender, Direct: true, SenderID: sender, SenderName: "S", Text: text, Roles: roles}})).To(Succeed())
		waitFor("the answer", func() bool { return len(r.bot.answers()) >= n })
	}
	tools := func() []string {
		chats, err := r.svc.List(ctx, alice, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(chats).NotTo(BeEmpty())
		return r.h.ToolNames(chats[0].ID)
	}
	setup := func(caps []string, grants []connectors.RoleGrant, paired ...string) {
		n = 0
		bot := newScriptedBot()
		addon := studiobot.Telegram(&transport.API{BaseURL: bot.srv.URL, Client: bot.srv.Client()})
		addon.Kind.Roles = []contract.ConnectorRole{{ID: "admin", Title: "Admin"}}
		addon.Kind.Permissions = []string{contract.CapMemoryWrite, contract.CapProfileWrite, contract.CapProposalsConfirm}
		sh := changeShared(grantAll)
		r = newConnRigWith(sh, connStore(), studiobot.NewTransport(addon), script, nil)
		r.bot, r.untrusted = bot, true
		r.grantBuiltins(contract.CapMemoryWrite, contract.CapProfileWrite, contract.CapTriggersPropose)
		b = r.bind(studio.ConnectorInput{Allow: []string{"11", "12"}, Caps: caps, Grants: grants})
		got, err := r.store.GetBinding(ctx, b.ID)
		Expect(err).NotTo(HaveOccurred())
		for _, p := range paired {
			got.Paired = append(got.Paired, connectors.PairedSender{SenderID: p, At: sh.clock.Now()})
		}
		Expect(r.store.PutBinding(ctx, got)).To(Succeed())
		b = got
	}
	admin := []connectors.RoleGrant{{Role: "admin", Caps: []string{contract.CapMemoryWrite}}}

	It("offers no gated tool to a sender who holds no capability", func() {
		setup([]string{contract.CapMemoryWrite}, admin)
		ask("11", nil, "remember")
		Expect(tools()).NotTo(ContainElement("propose_change"))
		res := r.h.ResultsFor("propose_change")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).To(MatchError(ContainSubstring("no tool named")), "the model had no such tool")
		list, _ := r.svc.Changes().List(ctx, alice)
		Expect(list).To(BeEmpty())
	})

	It("offers nothing when the owner enabled nothing, even to a paired sender", func() {
		setup(nil, nil, "11")
		ask("11", nil, "remember")
		Expect(tools()).NotTo(ContainElement("propose_change"))
	})

	It("offers the tool for the capability of a role and refuses the kind of change the role may not make", func() {
		setup([]string{contract.CapMemoryWrite, contract.CapProfileWrite}, admin)
		ask("11", []string{"admin"}, "remember")
		Expect(tools()).To(ContainElement("propose_change"))
		res := r.h.ResultsFor("propose_change")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).NotTo(HaveOccurred())
		ask("11", []string{"admin"}, "profile")
		res = r.h.ResultsFor("propose_change")
		Expect(res).To(HaveLen(2))
		Expect(res[1].Err).To(MatchError(ContainSubstring("not permitted: the owner has not allowed profile.write")))
		list, _ := r.svc.Changes().List(ctx, alice)
		Expect(list).To(HaveLen(1), "the refused change made no proposal")
	})

	It("lets a paired sender make the sensitive change", func() {
		setup([]string{contract.CapMemoryWrite, contract.CapProfileWrite}, nil, "12")
		ask("12", nil, "profile")
		res := r.h.ResultsFor("propose_change")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).NotTo(HaveOccurred())
	})

	It("does not trust a role that the sender claims on a binding that did not grant it", func() {
		setup([]string{contract.CapMemoryWrite}, nil)
		ask("11", []string{"admin"}, "remember")
		Expect(tools()).NotTo(ContainElement("propose_change"))
	})

	It("audits the decisions without text or codes", func() {
		setup([]string{contract.CapMemoryWrite, contract.CapProfileWrite}, admin)
		ask("11", []string{"admin"}, "profile")
		view, err := r.conns.Access(ctx, alice, b.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(view.Recent).NotTo(BeEmpty())
		raw, _ := json.Marshal(view.Recent)
		Expect(string(raw)).To(ContainSubstring(`"capability":"profile.write"`))
		Expect(string(raw)).To(ContainSubstring(`"allowed":false`))
		Expect(strings.Contains(string(raw), "be brief")).To(BeFalse(), "no message content in the audit")
	})

	It("shows what each kind of sender may do", func() {
		setup([]string{contract.CapMemoryWrite, contract.CapProfileWrite}, admin)
		view, err := r.conns.Access(ctx, alice, b.ID)
		Expect(err).NotTo(HaveOccurred())
		allowed := map[string][]string{}
		for _, s := range view.Subjects {
			for _, d := range s.Decisions {
				if d.Allowed {
					allowed[s.ID] = append(allowed[s.ID], d.Capability)
				}
			}
		}
		Expect(allowed).To(Equal(map[string][]string{
			"paired":     {contract.CapMemoryWrite, contract.CapProfileWrite},
			"role:admin": {contract.CapMemoryWrite},
		}))
	})

	It("refuses a file from a sender without files.upload and starts no turn", func() {
		setup([]string{contract.CapMemoryWrite}, admin)
		Expect(r.conns.Ingest(ctx, "telegram", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: contract.ConnectorMessage{
			Chat: "11", Direct: true, SenderID: "11", Media: []string{"m1"}}})).To(Succeed())
		waitFor("the refusal", func() bool { return len(r.bot.answers()) == 1 })
		Expect(r.bot.answers()[0]).To(Equal("You may not send files to this bot."))
		chats, _ := r.svc.List(ctx, alice, 10)
		Expect(chats).To(BeEmpty())
	})

	It("answers the capability gate of the other features from the same rules", func() {
		setup([]string{contract.CapTriggersPropose, contract.CapProposalsConfirm}, []connectors.RoleGrant{{Role: "admin", Caps: []string{contract.CapProposalsConfirm}}}, "12")
		gate := r.conns.CapabilityGate()
		req := func(cr studio.CapabilityRequest) bool {
			cr.Binding, cr.Owner = b.ID, alice
			return gate.Allow(ctx, cr)
		}
		Expect(req(studio.CapabilityRequest{Capability: contract.CapTriggersPropose})).To(BeTrue(), "the binding allows it")
		Expect(req(studio.CapabilityRequest{Capability: contract.CapTriggersRun})).To(BeFalse(), "not in the ceiling")
		Expect(req(studio.CapabilityRequest{Capability: contract.CapProposalsConfirm, Sender: "11"})).To(BeFalse())
		Expect(req(studio.CapabilityRequest{Capability: contract.CapProposalsConfirm, Sender: "11", Roles: []string{"admin"}})).To(BeTrue(), "a role the owner granted it")
		Expect(req(studio.CapabilityRequest{Capability: contract.CapProposalsConfirm, Sender: "11", Roles: []string{"admin"}, Sensitive: true})).To(BeFalse(), "a role never counts for a sensitive request")
		Expect(req(studio.CapabilityRequest{Capability: contract.CapProposalsConfirm, Sender: "12"})).To(BeTrue(), "paired")
		Expect(gate.Allow(ctx, studio.CapabilityRequest{Capability: contract.CapProposalsConfirm, Binding: "nope", Sender: "12"})).To(BeFalse())
		Expect(gate.Allow(ctx, studio.CapabilityRequest{Capability: contract.CapProposalsConfirm, Binding: b.ID, Owner: "mallory", Sender: "12"})).To(BeFalse())
	})

	It("lets an extra switch turn a capability off for everyone", func() {
		setup([]string{contract.CapMemoryWrite}, nil, "11")
		off := true
		r.conns.AddCapabilitySwitch(func(_ context.Context, _ connectors.Binding, c string) string {
			if off && c == contract.CapMemoryWrite {
				return "memory is switched off on this server"
			}
			return ""
		})
		ask("11", nil, "remember")
		Expect(tools()).NotTo(ContainElement("propose_change"))
	})
})
