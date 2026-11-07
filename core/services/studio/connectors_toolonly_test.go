package studio_test

import (
	"context"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/connsets"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A kind that declares toolOnly serves the tools of chats and agents and
// nothing else. One binding is used by many agents through their sets of
// connections, so it never has an agent, never listens and never accepts
// senders, whatever a client sends or an older row holds.
var _ = Describe("a binding of a tool-only kind", func() {
	ctx := context.Background()
	var (
		r  *connRig
		st connectors.Store
	)
	BeforeEach(func() {
		st = connStore()
		bot := newScriptedBot()
		addon := studiobot.Telegram(&transport.API{BaseURL: bot.srv.URL, Client: bot.srv.Client()})
		addon.Kind.ToolOnly = true
		r = newConnRigWith(newShared(), st, studiobot.NewTransport(addon), connSay("never"), nil)
		r.bot, r.untrusted = bot, true
	})
	input := func(in studio.ConnectorInput) studio.ConnectorInput {
		in.Kind, in.Name, in.Token = "telegram", "Tools", botToken
		return in
	}

	It("needs no agent to be saved, although the kind does not set agentOptional", func() {
		got, err := r.conns.CreateConnector(ctx, alice, input(studio.ConnectorInput{}))
		Expect(err).NotTo(HaveOccurred())
		Expect(got.CredentialsOnly()).To(BeTrue())
	})
	It("drops an agent that a client sends, on create and on update", func() {
		got, err := r.conns.CreateConnector(ctx, alice, input(studio.ConnectorInput{AgentID: r.agentID()}))
		Expect(err).NotTo(HaveOccurred())
		Expect(got.AgentID).To(BeEmpty())
		raw, err := st.GetBinding(ctx, got.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(raw.AgentID).To(BeEmpty(), "the stored row has no agent")

		up, err := r.conns.UpdateConnector(ctx, alice, got.ID, input(studio.ConnectorInput{AgentID: r.agentID()}))
		Expect(err).NotTo(HaveOccurred())
		Expect(up.AgentID).To(BeEmpty())
		raw, _ = st.GetBinding(ctx, got.ID)
		Expect(raw.AgentID).To(BeEmpty())
	})
	It("does not look up the agent a client names", func() {
		_, err := r.conns.CreateConnector(ctx, alice, input(studio.ConnectorInput{AgentID: "someone-elses"}))
		Expect(err).NotTo(HaveOccurred())
	})
	DescribeTable("refuses what only a listening binding has",
		func(mod func(*studio.ConnectorInput), words string) {
			in := input(studio.ConnectorInput{AgentID: r.agentID()})
			mod(&in)
			_, err := r.conns.CreateConnector(ctx, alice, in)
			Expect(err).To(MatchError(connectors.ErrInvalid))
			Expect(err).To(MatchError(ContainSubstring(words)))
		},
		Entry("enabled", func(in *studio.ConnectorInput) { in.Enabled = true }, "does not listen"),
		Entry("accept_any", func(in *studio.ConnectorInput) { in.AcceptAny = true }, "accept anyone"),
	)
	It("cannot be switched on, even when an older row holds an agent", func() {
		got, err := r.conns.CreateConnector(ctx, alice, input(studio.ConnectorInput{}))
		Expect(err).NotTo(HaveOccurred())
		raw, _ := st.GetBinding(ctx, got.ID)
		raw.AgentID = r.agentID()
		Expect(st.PutBinding(ctx, raw)).To(Succeed())
		_, err = r.conns.SetConnectorEnabled(ctx, alice, got.ID, true)
		Expect(err).To(MatchError(connectors.ErrInvalid))
	})
	It("shows an older row as agentless and off", func() {
		got, err := r.conns.CreateConnector(ctx, alice, input(studio.ConnectorInput{}))
		Expect(err).NotTo(HaveOccurred())
		raw, _ := st.GetBinding(ctx, got.ID)
		raw.AgentID, raw.Enabled, raw.AcceptAny = r.agentID(), true, true
		Expect(st.PutBinding(ctx, raw)).To(Succeed())

		one, err := r.conns.GetConnector(ctx, alice, got.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(one.AgentID).To(BeEmpty())
		Expect(one.Enabled).To(BeFalse())
		Expect(one.AcceptAny).To(BeFalse())
		Expect(one.CredentialsOnly()).To(BeTrue())
		all, err := r.conns.ListConnectors(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(HaveLen(1))
		Expect(all[0].AgentID).To(BeEmpty())
		Expect(all[0].Enabled).To(BeFalse())
	})

	Describe("how many agents use it", func() {
		save := func(name string, ids ...string) {
			_, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: name, Model: "m", Connections: connsets.Sets{"telegram": ids}})
			Expect(err).NotTo(HaveOccurred())
		}
		It("counts the agents of the person whose set holds the binding", func() {
			a, err := r.conns.CreateConnector(ctx, alice, input(studio.ConnectorInput{}))
			Expect(err).NotTo(HaveOccurred())
			Expect(a.UsedBy).NotTo(BeNil())
			Expect(*a.UsedBy).To(Equal(0))

			b, err := r.conns.CreateConnector(ctx, alice, studio.ConnectorInput{Kind: "telegram", Name: "Other", Token: botToken})
			Expect(err).NotTo(HaveOccurred())
			save("One", a.ID)
			save("Two", a.ID, b.ID, a.ID) // listing the same ID twice counts the agent once
			save("Three", b.ID)

			all, err := r.conns.ListConnectors(ctx, alice)
			Expect(err).NotTo(HaveOccurred())
			used := map[string]int{}
			for _, c := range all {
				Expect(c.UsedBy).NotTo(BeNil())
				used[c.ID] = *c.UsedBy
			}
			Expect(used).To(Equal(map[string]int{a.ID: 2, b.ID: 2}))
			one, err := r.conns.GetConnector(ctx, alice, a.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(*one.UsedBy).To(Equal(2))
		})
		It("does not count the agents of another person", func() {
			a, err := r.conns.CreateConnector(ctx, alice, input(studio.ConnectorInput{}))
			Expect(err).NotTo(HaveOccurred())
			_, err = r.svc.SaveAgent(ctx, bob, agents.Agent{Name: "Bobs", Model: "m", Connections: connsets.Sets{"telegram": {a.ID}}})
			if err == nil {
				got, err := r.conns.GetConnector(ctx, alice, a.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(*got.UsedBy).To(Equal(0))
			}
		})
	})
})

var _ = Describe("a binding of a kind that is not tool-only", func() {
	It("carries no used-by count", func() {
		ctx := context.Background()
		st := connStore()
		bot := newScriptedBot()
		addon := studiobot.Telegram(&transport.API{BaseURL: bot.srv.URL, Client: bot.srv.Client()})
		r := newConnRigWith(newShared(), st, studiobot.NewTransport(addon), connSay("never"), nil)
		r.bot, r.untrusted = bot, true
		got, err := r.conns.CreateConnector(ctx, alice, studio.ConnectorInput{Kind: "telegram", Name: "Bot", Token: botToken, AgentID: r.agentID()})
		Expect(err).NotTo(HaveOccurred())
		Expect(got.UsedBy).To(BeNil())
		Expect(got.AgentID).NotTo(BeEmpty())
	})
})
