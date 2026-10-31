package connectors_test

import (
	"bytes"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
)

var _ = Describe("sealing and validating bindings", func() {
	It("hides the token and binds it to its binding", func() {
		key, _ := connectors.NewKey()
		s, err := connectors.NewSealer(key)
		Expect(err).NotTo(HaveOccurred())
		token := []byte("123456:ABC-secret-token")
		aad := connectors.AAD("b1", "u1")
		a, _ := s.Seal(aad, token)
		b, _ := s.Seal(aad, token)
		Expect(bytes.Equal(a, b)).To(BeFalse(), "two seals of one token are equal")
		Expect(bytes.Contains(a, []byte("secret"))).To(BeFalse(), "the token is in the sealed bytes")
		got, err := s.Open(aad, a)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(token))
		_, err = s.Open(connectors.AAD("b2", "u1"), a)
		Expect(err).To(HaveOccurred(), "a token opened for another binding")
		a[len(a)-1] ^= 1
		_, err = s.Open(aad, a)
		Expect(err).To(HaveOccurred(), "a changed value opened")
		_, err = s.Open(aad, []byte("x"))
		Expect(err).To(HaveOccurred(), "a short value opened")
		_, err = connectors.NewSealer([]byte("short"))
		Expect(err).To(HaveOccurred(), "a short key was accepted")
	})

	It("validates a binding", func() {
		ok := connectors.Binding{Name: "Bot", Kind: connectors.KindTelegram, AgentID: "a", Allow: []string{"12", "-100123", "@alice_b"}, NotifyChat: "55"}
		Expect(connectors.Validate(ok)).To(Succeed())
		// Slack names people and channels with upper-case IDs, and its names may have dots.
		slack := connectors.Binding{Name: "App", Kind: "slack", AgentID: "a", Allow: []string{"U012AB3CD", "C0123456", "@ada.l-ovelace"}, NotifyChat: "U012AB3CD"}
		Expect(connectors.Validate(slack)).To(Succeed())
		many := make([]string, connectors.MaxAllow+1)
		for i := range many {
			many[i] = "1"
		}
		for name, b := range map[string]connectors.Binding{
			"no name":    {Kind: connectors.KindTelegram, AgentID: "a"},
			"long name":  {Name: strings.Repeat("n", 81), Kind: connectors.KindTelegram, AgentID: "a"},
			"bad kind":   {Name: "b", Kind: "Not A Kind", AgentID: "a"},
			"no agent":   {Name: "b", Kind: connectors.KindTelegram},
			"bad allow":  {Name: "b", Kind: connectors.KindTelegram, AgentID: "a", Allow: []string{"alice"}},
			"short name": {Name: "b", Kind: connectors.KindTelegram, AgentID: "a", Allow: []string{"@ab"}},
			"many":       {Name: "b", Kind: connectors.KindTelegram, AgentID: "a", Allow: many},
			"bad notify": {Name: "b", Kind: connectors.KindTelegram, AgentID: "a", NotifyChat: "me"},
			"lower id":   {Name: "b", Kind: "slack", AgentID: "a", Allow: []string{"u012ab"}},
			"id notify":  {Name: "b", Kind: "slack", AgentID: "a", NotifyChat: "@ada"},
		} {
			Expect(connectors.Validate(b)).To(MatchError(connectors.ErrInvalid), name)
		}
	})

	It("matches an allowlist by ID or username", func() {
		b := connectors.Binding{Allow: []string{"42", "@Alice"}}
		Expect(b.Allows("42", "")).To(BeTrue())
		Expect(b.Allows("7", "alice")).To(BeTrue())
		Expect(b.Allows("7", "bob")).To(BeFalse())
		Expect(b.Allows("7", "")).To(BeFalse())
		Expect(connectors.Binding{}.Allows("1", "x")).To(BeFalse(), "an empty allowlist accepted someone")
	})

	It("checks the reply and conversation modes", func() {
		base := connectors.Binding{Name: "Bot", Kind: connectors.KindTelegram, AgentID: "a"}
		for _, ok := range []connectors.Binding{
			base,
			{Name: "Bot", Kind: "telegram", AgentID: "a", Reply: connectors.ReplyOff, Conversation: connectors.ConversationChat},
			{Name: "Bot", Kind: "telegram", AgentID: "a", Reply: connectors.ReplyStream, Conversation: connectors.ConversationThread},
			{Name: "Bot", Kind: "telegram", AgentID: "a", Reply: connectors.ReplyProgress, Conversation: connectors.ConversationWindow, WindowMinutes: 30},
		} {
			Expect(connectors.Validate(ok)).To(Succeed(), "%+v", ok)
		}
		for name, b := range map[string]connectors.Binding{
			"unknown reply":        {Name: "b", Kind: "telegram", AgentID: "a", Reply: "loud"},
			"unknown conversation": {Name: "b", Kind: "telegram", AgentID: "a", Conversation: "forever"},
			"window without time":  {Name: "b", Kind: "telegram", AgentID: "a", Conversation: connectors.ConversationWindow},
			"window too long":      {Name: "b", Kind: "telegram", AgentID: "a", Conversation: connectors.ConversationWindow, WindowMinutes: connectors.MaxWindowMinutes + 1},
		} {
			Expect(connectors.Validate(b)).To(MatchError(connectors.ErrInvalid), name)
		}
		Expect(base.ReplyMode()).To(Equal(connectors.ReplyProgress), "progress is the default")
		Expect(base.ConversationMode()).To(Equal(connectors.ConversationSender), "one chat per sender is the default")
		Expect(base.SendsFiles()).To(BeTrue(), "files go out by default")
	})
})
