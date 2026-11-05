// SPDX-License-Identifier: MIT
package transport_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/discord/transport"
)

// connected polls once to open the link and waits until the fake Gateway
// has seen the identify.
func connected(f *fakeDiscord, g *transport.Gateway) {
	_, err := g.Poll(context.Background(), secretToken, 1, 0)
	Expect(err).NotTo(HaveOccurred())
	Eventually(func() int { return len(f.handshakes()) }, 10*time.Second, 10*time.Millisecond).Should(BeNumerically(">=", 1))
	Eventually(func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.conn != nil
	}).Should(BeTrue())
	// READY is sent after identify; give the client a moment to take it.
	time.Sleep(100 * time.Millisecond)
}

// next polls until n updates are there.
func next(g *transport.Gateway, offset int64, n int) []contract.ConnectorMessage {
	var out []contract.ConnectorMessage
	Eventually(func() int {
		var err error
		out, err = g.Poll(context.Background(), secretToken, offset, 1)
		Expect(err).NotTo(HaveOccurred())
		return len(out)
	}, 10*time.Second, 10*time.Millisecond).Should(BeNumerically(">=", n))
	return out
}

// none says that no update arrives: a message that is read is queued before
// the one that follows, so a marker message shows the earlier ones were dropped.
func settle(f *fakeDiscord, g *transport.Gateway) []contract.ConnectorMessage {
	f.dispatch("MESSAGE_CREATE", msg{channel: dmID, sender: userID, text: "marker"}.payload())
	return next(g, 1, 1)
}

var _ = Describe("the Gateway link", func() {
	var (
		f *fakeDiscord
		g *transport.Gateway
	)
	BeforeEach(func() {
		f = newFakeDiscord()
		g = f.newGateway("")
		DeferCleanup(func() { g.Close(); f.Close() })
	})

	It("identifies with the intents it needs and the token, and answers heartbeats", func() {
		connected(f, g)
		shake := f.handshakes()[0]
		Expect(shake.Op).To(Equal(2))
		Expect(shake.Data["token"]).To(ContainSubstring(secretToken))
		Expect(shake.Data["intents"]).To(BeNumerically("==", 1+512+4096+32768), "guilds, server messages, direct messages, message content")
		Eventually(func() int32 { return f.heartbeats.Load() }, 5*time.Second).Should(BeNumerically(">=", 2))
		Expect(f.called("GET /users/@me")).NotTo(BeEmpty(), "the token is checked before the link opens")
		Expect(f.called("GET /users/@me")[0].Header.Get("Authorization")).To(Equal("Bot " + secretToken))
	})

	It("reads a direct message as direct, with the sender and the text", func() {
		connected(f, g)
		f.dispatch("MESSAGE_CREATE", msg{channel: dmID, sender: userID, text: "hello"}.payload())
		got := next(g, 1, 1)
		Expect(got).To(HaveLen(1))
		m := got[0]
		Expect(m.Direct).To(BeTrue())
		Expect(m.Chat).To(Equal(dmID))
		Expect(m.Thread).To(BeEmpty())
		Expect(m.SenderID).To(Equal(userID))
		Expect(m.SenderName).To(Equal("Ada"))
		Expect(m.SenderUsername).To(BeEmpty(), "only the ID names a sender: a username can change hands")
		Expect(m.Text).To(Equal("hello"))
		Expect(m.Cursor).To(BeNumerically(">", 1))
	})

	It("reads a server message only when it mentions the bot or replies to it", func() {
		connected(f, g)
		f.dispatch("MESSAGE_CREATE", msg{channel: chanID, guild: guildID, sender: userID, text: "just talking"}.payload())
		f.dispatch("MESSAGE_CREATE", msg{channel: chanID, guild: guildID, sender: userID, text: "@everyone look"}.payload())
		f.dispatch("MESSAGE_CREATE", msg{channel: chanID, guild: guildID, sender: userID, text: fmt.Sprintf("<@%s> what is 2+2?", botID), mentionBot: true}.payload())
		f.dispatch("MESSAGE_CREATE", msg{channel: chanID, guild: guildID, sender: userID, text: "and then?", reply: botID}.payload())
		f.dispatch("MESSAGE_CREATE", msg{channel: chanID, guild: guildID, sender: userID, text: "reply to someone else", reply: userID}.payload())
		got := next(g, 1, 2)
		Expect(got).To(HaveLen(2))
		Expect(got[0].Mention).To(BeTrue())
		Expect(got[0].Direct).To(BeFalse())
		Expect(got[0].Chat).To(Equal(chanID), "the chat is the channel, so an allowlist of channels works")
		Expect(got[0].Text).To(Equal("what is 2+2?"), "the mention of the bot is not part of the question")
		Expect(got[1].Text).To(Equal("and then?"))
		Expect(got[1].ReplyTo).NotTo(BeEmpty())
		Expect(got[1].Mention).To(BeTrue())
	})

	It("marks a message of a server, a channel or a thread as not direct", func() {
		connected(f, g)
		f.dispatch("THREAD_CREATE", map[string]any{"id": threadI, "type": 11, "parent_id": chanID, "guild_id": guildID, "newly_created": true})
		f.dispatch("MESSAGE_CREATE", msg{channel: chanID, guild: guildID, sender: userID, text: "in the channel", mentionBot: true}.payload())
		f.dispatch("MESSAGE_CREATE", msg{channel: threadI, guild: guildID, sender: userID, text: "in the thread", mentionBot: true}.payload())
		f.dispatch("MESSAGE_CREATE", msg{channel: dmID, sender: userID, text: "in a direct message"}.payload())
		got := next(g, 1, 3)
		Expect(got).To(HaveLen(3))
		Expect([]bool{got[0].Direct, got[1].Direct, got[2].Direct}).To(Equal([]bool{false, false, true}))
		Expect(got[1].Thread).To(Equal(threadI))
	})

	It("ignores every message that a bot, a webhook or Discord wrote, and the bot's own", func() {
		connected(f, g)
		f.dispatch("MESSAGE_CREATE", msg{channel: dmID, sender: botID, text: "my own"}.payload())
		f.dispatch("MESSAGE_CREATE", msg{channel: dmID, sender: "400000000000000009", text: "another bot", bot: true}.payload())
		f.dispatch("MESSAGE_CREATE", msg{channel: chanID, guild: guildID, sender: userID, text: "webhook", mentionBot: true, webhook: true}.payload())
		f.dispatch("MESSAGE_CREATE", msg{channel: chanID, guild: guildID, sender: userID, text: "pinned a message", mentionBot: true, typ: 6}.payload())
		got := settle(f, g)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Text).To(Equal("marker"))
	})

	It("puts the parent channel in the chat of a thread message, from what the Gateway told", func() {
		connected(f, g)
		f.dispatch("THREAD_CREATE", map[string]any{"id": threadI, "type": 11, "parent_id": chanID, "guild_id": guildID, "newly_created": true})
		f.dispatch("MESSAGE_CREATE", msg{channel: threadI, guild: guildID, sender: userID, text: "in the thread", mentionBot: true}.payload())
		got := next(g, 1, 1)
		Expect(got[0].Chat).To(Equal(chanID), "the allowlist covers the threads of a listed channel")
		Expect(got[0].Thread).To(Equal(threadI))
		Expect(got[0].Mention).To(BeTrue())
		Expect(f.called("GET /channels/:id")).To(BeEmpty(), "no lookup was needed")
	})

	It("learns a thread from the guild it belongs to, and asks for one it does not know", func() {
		connected(f, g)
		f.dispatch("GUILD_CREATE", map[string]any{"id": guildID, "name": "g", "channels": []any{map[string]any{"id": chanID, "type": 0}},
			"threads": []any{map[string]any{"id": "300000000000000077", "type": 12, "parent_id": chanID}}})
		f.dispatch("MESSAGE_CREATE", msg{channel: "300000000000000077", guild: guildID, sender: userID, text: "private thread", mentionBot: true}.payload())
		got := next(g, 1, 1)
		Expect(got[0].Thread).To(Equal("300000000000000077"))
		Expect(got[0].Chat).To(Equal(chanID))
		// A thread the Gateway never named: one lookup of the channel.
		f.dispatch("MESSAGE_CREATE", msg{channel: threadI, guild: guildID, sender: userID, text: "unknown thread", mentionBot: true}.payload())
		got = next(g, got[0].Cursor+1, 1)
		Expect(got[0].Chat).To(Equal(chanID))
		Expect(got[0].Thread).To(Equal(threadI))
		Expect(f.called("GET /channels/:id")).To(HaveLen(1))
	})

	It("follows a thread once the bot was mentioned in it, and not with the option never", func() {
		connected(f, g)
		f.dispatch("THREAD_CREATE", map[string]any{"id": threadI, "type": 11, "parent_id": chanID, "guild_id": guildID})
		f.dispatch("MESSAGE_CREATE", msg{channel: threadI, guild: guildID, sender: userID, text: "no mention before"}.payload())
		f.dispatch("MESSAGE_CREATE", msg{channel: threadI, guild: guildID, sender: userID, text: "hey bot", mentionBot: true}.payload())
		f.dispatch("MESSAGE_CREATE", msg{channel: threadI, guild: guildID, sender: "400000000000000002", text: "a second person, no mention"}.payload())
		got := next(g, 1, 2)
		Expect(got).To(HaveLen(2), "the first message came before the mention")
		Expect(got[0].Text).To(Equal("hey bot"))
		Expect(got[1].Text).To(Equal("a second person, no mention"))
		Expect(got[1].Mention).To(BeTrue(), "addressed because the bot follows the thread")
		Expect(got[1].Thread).To(Equal(threadI))
	})

	It("keeps the followed threads over a restart", func() {
		dir := GinkgoT().TempDir()
		g.Close()
		g = f.newGateway(dir)
		connected(f, g)
		f.dispatch("THREAD_CREATE", map[string]any{"id": threadI, "type": 11, "parent_id": chanID, "guild_id": guildID})
		f.dispatch("MESSAGE_CREATE", msg{channel: threadI, guild: guildID, sender: userID, text: "start", mentionBot: true}.payload())
		first := next(g, 1, 1)
		g.Close()

		g = f.newGateway(dir)
		connected(f, g)
		f.dispatch("THREAD_CREATE", map[string]any{"id": threadI, "type": 11, "parent_id": chanID, "guild_id": guildID})
		f.dispatch("MESSAGE_CREATE", msg{channel: threadI, guild: guildID, sender: userID, text: "after the restart"}.payload())
		got := next(g, first[0].Cursor+1, 1)
		Expect(got[0].Text).To(Equal("after the restart"))
	})

	It("hands over a file as a reference to the channel, message and file", func() {
		connected(f, g)
		p := msg{channel: dmID, sender: userID, text: "", attachments: []map[string]any{{"id": "800000000000000001", "filename": "a.png", "size": 10, "url": "https://cdn.discordapp.com/x"}}}.payload()
		f.dispatch("MESSAGE_CREATE", p)
		got := next(g, 1, 1)
		Expect(got[0].Media).To(Equal([]string{dmID + "/" + p["id"].(string) + "/800000000000000001"}))
		Expect(got[0].Unsupported).To(BeFalse())
		f.dispatch("MESSAGE_CREATE", msg{channel: dmID, sender: userID}.payload())
		got = next(g, got[0].Cursor+1, 1)
		Expect(got[0].Unsupported).To(BeTrue(), "no text and no file")
	})

	It("does not hand over the same message twice, nor one the host has handled", func() {
		connected(f, g)
		p := msg{channel: dmID, sender: userID, text: "once"}.payload()
		f.dispatch("MESSAGE_CREATE", p)
		f.dispatch("MESSAGE_CREATE", p)
		got := next(g, 1, 1)
		Expect(got).To(HaveLen(1))
		again, err := g.Poll(context.Background(), secretToken, 1, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(HaveLen(1), "not handled yet: still there")
		done, err := g.Poll(context.Background(), secretToken, got[0].Cursor+1, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeEmpty())
	})

	It("resumes the session after the link is lost and goes on with the messages", func() {
		connected(f, g)
		f.dispatch("MESSAGE_CREATE", msg{channel: dmID, sender: userID, text: "before"}.payload())
		first := next(g, 1, 1)
		f.drop()
		Eventually(func() int { return len(f.handshakes()) }, 15*time.Second, 20*time.Millisecond).Should(BeNumerically(">=", 2))
		shake := f.handshakes()[1]
		Expect(shake.Op).To(Equal(6), "a resume, not a new identify")
		Expect(shake.Data["session_id"]).To(Equal("sess-1"))
		Expect(shake.Data["seq"]).To(BeNumerically(">", 0))
		Expect(shake.Data["token"]).To(ContainSubstring(secretToken))
		time.Sleep(200 * time.Millisecond)
		f.dispatch("MESSAGE_CREATE", msg{channel: dmID, sender: userID, text: "after"}.payload())
		got := next(g, first[0].Cursor+1, 1)
		Expect(got[0].Text).To(Equal("after"))
	})

	DescribeTable("stops for good and says why when Discord refuses the token or the intents",
		func(code int, want string) {
			f.closeIdentify = code
			var err error
			Eventually(func() error {
				_, err = g.Poll(context.Background(), secretToken, 1, 1)
				return err
			}, 15*time.Second, 50*time.Millisecond).Should(HaveOccurred())
			Expect(errors.Is(err, connector.ErrUnauthorized)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring(want))
			Expect(err.Error()).NotTo(ContainSubstring("SECRET"))
			Expect(len(f.handshakes())).To(Equal(1), "a refused token is not tried again: identifies are limited")
		},
		Entry("a bad token", 4004, "refused the bot token"),
		Entry("intents that are not allowed", 4014, "Message Content"),
		Entry("invalid intents", 4013, "Message Content"),
	)

	It("stops for good when the REST API refuses the token", func() {
		f.on["GET /users/@me"] = func(restCall) reply { return reply{Status: 401, Body: `{"message":"401: Unauthorized","code":0}`} }
		var err error
		Eventually(func() error {
			_, err = g.Poll(context.Background(), secretToken, 1, 1)
			return err
		}, 5*time.Second, 20*time.Millisecond).Should(HaveOccurred())
		Expect(errors.Is(err, connector.ErrUnauthorized)).To(BeTrue())
		Expect(f.handshakes()).To(BeEmpty(), "no Gateway link for a token that is refused")
	})

	It("closes a link that nobody polls", func() {
		g.Close()
		g = f.newGateway("")
		g.IdleAfter = 300 * time.Millisecond
		connected(f, g)
		Expect(g.Links()).To(Equal(1))
		Eventually(g.Links, 5*time.Second, 50*time.Millisecond).Should(Equal(0))
	})

	It("never hands out a cursor below the offset the host stored", func() {
		connected(f, g)
		high := time.Now().Add(time.Hour).UnixMicro()
		g.Close()
		g = f.newGateway("")
		_, err := g.Poll(context.Background(), secretToken, high, 0)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() int { return len(f.handshakes()) }, 10*time.Second).Should(BeNumerically(">=", 2))
		time.Sleep(100 * time.Millisecond)
		f.dispatch("MESSAGE_CREATE", msg{channel: dmID, sender: userID, text: "late"}.payload())
		got := next(g, high, 1)
		Expect(got[0].Cursor).To(BeNumerically(">=", high))
	})

	It("limits the messages of one sender and counts what it drops", func() {
		connected(f, g)
		total := transport.SenderPerMinute + 10
		for i := range total {
			f.dispatch("MESSAGE_CREATE", msg{channel: dmID, sender: userID, text: fmt.Sprintf("m%d", i)}.payload())
		}
		f.dispatch("MESSAGE_CREATE", msg{channel: dmID, sender: "400000000000000002", text: "someone else"}.payload())
		Eventually(g.Dropped, 5*time.Second).Should(BeEquivalentTo(10))
		got := next(g, 1, transport.SenderPerMinute+1)
		Expect(got).To(HaveLen(transport.SenderPerMinute + 1))
		Expect(got[len(got)-1].Text).To(Equal("someone else"), "another sender is not held back")
	})

	It("keeps the queue bounded: it drops the oldest messages and counts them", func() {
		connected(f, g)
		senders := 20
		perSender := 29
		for i := range senders * perSender {
			f.dispatch("MESSAGE_CREATE", msg{channel: dmID, sender: fmt.Sprintf("4000000000000001%02d", i%senders), text: fmt.Sprintf("m%d", i)}.payload())
		}
		Eventually(g.Dropped, 10*time.Second).Should(BeEquivalentTo(senders*perSender - 500))
		var got []contract.ConnectorMessage
		Eventually(func() int {
			var err error
			got, err = g.Poll(context.Background(), secretToken, 1, 0)
			Expect(err).NotTo(HaveOccurred())
			return len(got)
		}, 5*time.Second).Should(BeNumerically(">", 0))
		Expect(len(got)).To(BeNumerically("<=", 100), "one poll hands over at most 100")
		Expect(strings.HasPrefix(got[0].Text, "m")).To(BeTrue())
		Expect(got[0].Text).NotTo(Equal("m0"), "the oldest were dropped")
	})
})
