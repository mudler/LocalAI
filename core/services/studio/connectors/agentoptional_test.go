package connectors_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
)

var _ = Describe("a binding without an agent", func() {
	bare := func() connectors.Binding { return connectors.Binding{Name: "Tokens", Kind: "github"} }

	It("is refused by Validate and by a kind that does not declare agentOptional", func() {
		Expect(connectors.Validate(bare())).To(MatchError(connectors.ErrInvalid))
		Expect(connectors.ValidateKind(bare(), false)).To(MatchError(ContainSubstring("agent is required")))
	})
	It("is accepted by a kind that declares agentOptional while it sets nothing an agent needs", func() {
		b := bare()
		b.WriteMode = connectors.WritePropose
		b.Resources = connectors.NewResources([]string{"acme/widgets"})
		Expect(connectors.ValidateKind(b, true)).To(Succeed())
		Expect(b.CredentialsOnly()).To(BeTrue())
	})
	It("is accepted with the default approval written out, as a form sends it", func() {
		b := bare()
		b.Approval = connectors.Approval{Mode: connectors.ApprovalAsk}
		Expect(connectors.ValidateKind(b, true)).To(Succeed())
		b.Approval = connectors.Approval{Mode: connectors.ApprovalAsk, Acts: nil}.Cleaned()
		Expect(connectors.ValidateKind(b, true)).To(Succeed())
	})
	It("still needs an agent for an approval that is not the default", func() {
		for _, a := range []connectors.Approval{
			{Mode: connectors.ApprovalAsk, MaxPending: 3},
			{Mode: connectors.ApprovalAsk, Acts: []string{"act.files"}},
			{Mode: connectors.ApprovalAuto},
		} {
			b := bare()
			b.Approval = a
			Expect(connectors.ValidateKind(b, true)).To(MatchError(ContainSubstring("trigger approval needs an agent")), "%+v", a)
		}
	})
	DescribeTable("needs an agent for",
		func(mod func(*connectors.Binding)) {
			b := bare()
			mod(&b)
			Expect(connectors.ValidateKind(b, true)).To(MatchError(connectors.ErrInvalid))
			b.AgentID = "a1"
			Expect(connectors.ValidateKind(b, true)).To(Succeed())
		},
		Entry("listening", func(b *connectors.Binding) { b.Enabled = true }),
		Entry("accepting anyone", func(b *connectors.Binding) { b.AcceptAny = true }),
		Entry("an allowlist", func(b *connectors.Binding) { b.Allow = []string{"11"} }),
		Entry("a notification chat", func(b *connectors.Binding) { b.NotifyChat = "11" }),
		Entry("channels", func(b *connectors.Binding) { b.Channels = []connectors.Channel{{ID: "C1"}} }),
		Entry("a capability ceiling", func(b *connectors.Binding) { b.Caps = []string{"memory.write"} }),
		Entry("pairing", func(b *connectors.Binding) { b.Paired = []connectors.PairedSender{{SenderID: "11"}} }),
		Entry("trigger approval", func(b *connectors.Binding) { b.Approval = connectors.Approval{Mode: connectors.ApprovalAutoReadonly} }),
	)
})
