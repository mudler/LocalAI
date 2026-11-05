package studio_test

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The same Telegram bot registered twice: the case of the same Slack app
// registered with two token pairs, on the add-on that the scripted bot serves.
var _ = Describe("a bot connected twice", func() {
	var r *connRig
	ctx := context.Background()
	msg := func(id string) contract.ConnectorMessage {
		return contract.ConnectorMessage{Chat: "11", Direct: true, MessageID: id, SenderID: "11", SenderName: "S11", Text: "hello"}
	}
	BeforeEach(func() {
		r = newConnRig(newShared(), connStore(), newScriptedBot(), connSay("one answer"),
			func(context.Context) bool { return false }) // no polling loop: the specs hand the messages over themselves
	})

	It("asks before it saves the second connection, names the first, and marks both", func() {
		first := r.bind(studio.ConnectorInput{Name: "Support bot", Allow: []string{"11"}})
		in := studio.ConnectorInput{Kind: "telegram", Name: "Same bot again", Token: "777:other", AgentID: r.agentID(), Enabled: true, Allow: []string{"11"}, Reply: connectors.ReplyOff}
		_, err := r.conns.CreateConnector(ctx, alice, in)
		Expect(err).To(MatchError(studio.ErrDuplicateConnection))
		Expect(err.Error()).To(Equal("This bot is also connected as Support bot. Both connections will answer the same message."))
		list, _ := r.conns.ListConnectors(ctx, alice)
		Expect(list).To(HaveLen(1), "nothing was saved")

		in.ConfirmDuplicate = studio.DuplicateConfirmation
		second, err := r.conns.CreateConnector(ctx, alice, in)
		Expect(err).NotTo(HaveOccurred())
		Expect(second.SameBotAs).To(Equal([]studio.DuplicateRef{{ID: first.ID, Name: "Support bot"}}))
		list, _ = r.conns.ListConnectors(ctx, alice)
		Expect(list).To(HaveLen(2))
		for _, i := range list {
			Expect(i.SameBotAs).To(HaveLen(1), i.Name)
		}

		// Turning one off clears the warning on the other.
		off, err := r.conns.SetConnectorEnabled(ctx, alice, second.ID, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(off.SameBotAs).To(BeEmpty())
		got, _ := r.conns.GetConnector(ctx, alice, first.ID)
		Expect(got.SameBotAs).To(BeEmpty())
	})

	It("answers one message once, from the older connection, and from the other when the older is off", func() {
		first := r.bind(studio.ConnectorInput{Name: "Support bot", Allow: []string{"11"}})
		r.sh.clock.Advance(time.Minute) // a later creation time
		second := r.bind(studio.ConnectorInput{Name: "Same bot again", Token: "777:other", Allow: []string{"11"}})
		Expect(second.Created.After(first.Created)).To(BeTrue())

		Expect(r.conns.Ingest(ctx, first.AddonID, contract.ConnectorInbound{BindingID: second.ID, ConnectorMessage: msg("m1")})).To(Succeed())
		Expect(r.conns.Ingest(ctx, first.AddonID, contract.ConnectorInbound{BindingID: first.ID, ConnectorMessage: msg("m1")})).To(Succeed())
		Eventually(func() int { return len(r.bot.answers()) }, "10s").Should(Equal(1))
		Consistently(func() int { return len(r.bot.answers()) }, "300ms", "50ms").Should(Equal(1), "the younger connection stays silent")
		chats, _ := r.svc.List(ctx, alice, 20)
		Expect(chats).To(HaveLen(1))
		Expect(chats[0].OriginID).To(Equal(first.ID))

		_, err := r.conns.SetConnectorEnabled(ctx, alice, first.ID, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.conns.Ingest(ctx, first.AddonID, contract.ConnectorInbound{BindingID: second.ID, ConnectorMessage: msg("m2")})).To(Succeed())
		Eventually(func() int { return len(r.bot.answers()) }, "10s").Should(Equal(2))
	})
})
