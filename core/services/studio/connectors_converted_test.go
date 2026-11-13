package studio_test

import (
	"context"
	"strings"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The conversion of an old agent makes its Telegram connector through this
// service with a minimal input: off, owned by the agent's owner, bound to the
// converted agent, with the old admins as the allowlist.
var _ = Describe("a binding made by the conversion of an old agent", func() {
	ctx := context.Background()

	It("is switched off, has the allowlist and the agent, and keeps its token sealed", func() {
		st := connStore()
		bot := newScriptedBot()
		r := newConnRigWith(newShared(), st, studiobot.NewTransport(studiobot.Telegram(&transport.API{BaseURL: bot.srv.URL, Client: bot.srv.Client()})), connSay("never"), nil)
		r.bot, r.untrusted = bot, true
		kinds, err := r.conns.ConnectorKinds(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(kinds).To(ContainElement(HaveField("ID", "telegram")))

		const dummy = "123456:TEST"
		got, err := r.conns.CreateConnector(ctx, alice, studio.ConnectorInput{Kind: "telegram", Name: "Support: telegram", Token: dummy,
			AgentID: r.agentID(), Allow: []string{"@ada_l", "@bo_b"}, Enabled: false})
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Enabled).To(BeFalse())
		Expect(got.HasToken).To(BeTrue())
		Expect(got.Token).To(BeEmpty(), "the view of a binding never holds the token")
		Expect(got.Allow).To(Equal([]string{"@ada_l", "@bo_b"}))
		Expect(got.AcceptAny).To(BeFalse())

		raw, err := st.GetBinding(ctx, got.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(raw.Owner).To(Equal(alice))
		Expect(raw.AgentID).To(Equal(r.agentID()))
		Expect(raw.Enabled).To(BeFalse())
		Expect(string(raw.Token)).NotTo(ContainSubstring(dummy), "the stored token is sealed")
		Expect(strings.Contains(string(raw.Token), "TEST")).To(BeFalse())
	})
})
