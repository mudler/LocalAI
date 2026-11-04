package studio_test

import (
	"context"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A kind that declares agentOptional gives bindings that only hold
// credentials. Every place that reads the agent of a binding must treat such a
// binding as unable to receive, answer, deliver or be authorized.
var _ = Describe("a credentials-only connector binding", func() {
	ctx := context.Background()
	var (
		r  *connRig
		st connectors.Store
		b  connectors.Binding
	)
	newRig := func(optional bool) {
		st = connStore()
		bot := newScriptedBot()
		addon := studiobot.Telegram(&transport.API{BaseURL: bot.srv.URL, Client: bot.srv.Client()})
		addon.Kind.AgentOptional = optional
		r = newConnRigWith(newShared(), st, studiobot.NewTransport(addon), connSay("never"), nil)
		r.bot, r.untrusted = bot, true
	}
	credOnly := func(in studio.ConnectorInput) studio.ConnectorInput {
		in.Kind, in.Name, in.Token = "telegram", "Tokens", botToken
		return in
	}
	// stored writes an agentless binding that is switched on, as a store could
	// hold it after an edit by hand or a downgrade.
	stored := func() connectors.Binding {
		got, err := r.conns.CreateConnector(ctx, alice, credOnly(studio.ConnectorInput{Resources: []string{"acme/widgets"}}))
		Expect(err).NotTo(HaveOccurred())
		raw, err := st.GetBinding(ctx, got.ID)
		Expect(err).NotTo(HaveOccurred())
		raw.Enabled, raw.AcceptAny = true, true
		Expect(st.PutBinding(ctx, raw)).To(Succeed())
		return raw
	}

	Describe("creating and editing", func() {
		It("is refused when the kind does not declare agentOptional", func() {
			newRig(false)
			_, err := r.conns.CreateConnector(ctx, alice, credOnly(studio.ConnectorInput{}))
			Expect(err).To(MatchError(connectors.ErrInvalid))
			Expect(err).To(MatchError(ContainSubstring("agent is required")))
		})
		It("is accepted when the kind declares it, with no agent and listening off", func() {
			newRig(true)
			got, err := r.conns.CreateConnector(ctx, alice, credOnly(studio.ConnectorInput{Resources: []string{"acme/widgets"}}))
			Expect(err).NotTo(HaveOccurred())
			Expect(got.AgentID).To(BeEmpty())
			Expect(got.CredentialsOnly()).To(BeTrue())
			grant, err := r.conns.CredentialFor(ctx, alice, got.AddonID, got.ID, "acme/widgets")
			Expect(err).NotTo(HaveOccurred())
			Expect(grant.Secret).To(Equal(botToken))
		})
		DescribeTable("needs an agent for what only an agent can use",
			func(mod func(*studio.ConnectorInput), words string) {
				newRig(true)
				in := credOnly(studio.ConnectorInput{})
				mod(&in)
				_, err := r.conns.CreateConnector(ctx, alice, in)
				Expect(err).To(MatchError(connectors.ErrInvalid))
				Expect(err).To(MatchError(ContainSubstring(words)))
			},
			Entry("listening", func(in *studio.ConnectorInput) { in.Enabled = true }, "listening needs an agent"),
			Entry("accepting anyone", func(in *studio.ConnectorInput) { in.AcceptAny = true }, "accepting anyone"),
			Entry("an allowlist", func(in *studio.ConnectorInput) { in.Allow = []string{"11"} }, "the allowlist"),
			Entry("a notification chat", func(in *studio.ConnectorInput) { in.NotifyChat = "11" }, "the notification chat"),
			Entry("talk-back channels", func(in *studio.ConnectorInput) {
				in.Channels = []connectors.Channel{{ID: "C1", Approved: true}}
			}, "the talk-back channels"),
			Entry("permissions", func(in *studio.ConnectorInput) { in.Caps = []string{contract.CapMemoryWrite} }, "permissions"),
		)
		It("cannot be switched on, and an agent can be added later", func() {
			newRig(true)
			got, err := r.conns.CreateConnector(ctx, alice, credOnly(studio.ConnectorInput{}))
			Expect(err).NotTo(HaveOccurred())
			_, err = r.conns.SetConnectorEnabled(ctx, alice, got.ID, true)
			Expect(err).To(MatchError(connectors.ErrInvalid))
			on, err := r.conns.UpdateConnector(ctx, alice, got.ID, credOnly(studio.ConnectorInput{AgentID: r.agentID(), Enabled: true, AcceptAny: true}))
			Expect(err).NotTo(HaveOccurred())
			Expect(on.CredentialsOnly()).To(BeFalse())
			Expect(on.Enabled).To(BeTrue())
		})
		It("refuses an agent that is not the owner's", func() {
			newRig(true)
			_, err := r.conns.CreateConnector(ctx, alice, credOnly(studio.ConnectorInput{AgentID: "someone-elses"}))
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("a record that is on without an agent", func() {
		BeforeEach(func() { newRig(true); b = stored() })

		It("receives nothing: Ingest refuses it and no sender is logged", func() {
			err := r.conns.Ingest(ctx, b.AddonID, contract.ConnectorInbound{BindingID: b.ID,
				ConnectorMessage: contract.ConnectorMessage{Chat: "11", Direct: true, SenderID: "11", Text: "hi"}})
			Expect(err).To(MatchError(connectors.ErrForbidden))
			senders, _ := st.Senders(ctx, b.ID, 10)
			Expect(senders).To(BeEmpty())
			chats, _ := r.svc.List(ctx, alice, 10)
			Expect(chats).To(BeEmpty())
		})
		It("starts no polling loop", func() {
			r.conns.Sync(ctx)
			Consistently(func() int { return len(r.bot.answers()) }, "300ms").Should(BeZero())
		})
		It("denies every capability, also for a paired sender with the ceiling set", func() {
			b.Caps = contract.ConnectorCapabilityNames()
			b.Paired = []connectors.PairedSender{{SenderID: "11", At: b.Created}}
			for _, name := range contract.ConnectorCapabilityNames() {
				d := studio.Authorize(b, nil, studio.AccessSubject{SenderID: "11", Allowlisted: true}, name, b.Updated, "")
				Expect(d.Allowed).To(BeFalse(), name)
				Expect(d.Reason).To(ContainSubstring("no agent"))
			}
		})
		It("gives no pairing code", func() {
			_, err := r.conns.StartPairing(ctx, alice, b.ID)
			Expect(err).To(MatchError(connectors.ErrInvalid))
		})
		It("is not told a notification", func() {
			raw, _ := st.GetBinding(ctx, b.ID)
			raw.NotifyChat = "11"
			Expect(st.PutBinding(ctx, raw)).To(Succeed())
			r.conns.Forward(ctx, notify.Notification{Principal: alice, Title: "t", Body: "b"})
			Consistently(func() int { return len(r.bot.answers()) }, "300ms").Should(BeZero())
		})
		It("still gives its token to a tool call", func() {
			grant, err := r.conns.CredentialFor(ctx, alice, b.AddonID, b.ID, "acme/widgets")
			Expect(err).NotTo(HaveOccurred())
			Expect(grant.Secret).To(Equal(botToken))
		})
	})
})
