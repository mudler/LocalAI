// SPDX-License-Identifier: MIT
package transport_test

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/addons/discord/transport"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func call(rt *transport.Runtime, req contract.ConnectorRequest) contract.ConnectorResponse {
	req.Kind, req.Token = "discord", secretToken
	out, err := rt.Connector(context.Background(), req)
	Expect(err).NotTo(HaveOccurred())
	return out
}

var _ = Describe("the connector route", func() {
	var (
		f  *fakeDiscord
		rt *transport.Runtime
	)
	BeforeEach(func() {
		f = newFakeDiscord()
		rt = f.runtime(nil)
		DeferCleanup(func() { rt.Close(); f.Close() })
	})

	Describe("me", func() {
		It("names the bot and says nothing else when the Message Content intent is on", func() {
			out := call(rt, contract.ConnectorRequest{Op: "me"})
			Expect(out.Error).To(BeNil())
			Expect(out.BotID).To(Equal(botID))
			Expect(out.BotUsername).To(Equal("localai-bot"))
			Expect(out.AccountID).To(Equal(botID), "the application names the account")
		})

		It("warns in the name when the Message Content intent is off", func() {
			f.intentsOff = true
			out := call(rt, contract.ConnectorRequest{Op: "me"})
			Expect(out.Error).To(BeNil())
			Expect(out.BotUsername).To(ContainSubstring("Message Content intent is off"))
		})

		It("keeps a failing application call from failing the check", func() {
			f.on["GET /applications/@me"] = func(restCall) reply { return reply{Status: 403, Body: `{"message":"no"}`} }
			out := call(rt, contract.ConnectorRequest{Op: "me"})
			Expect(out.Error).To(BeNil())
			Expect(out.BotUsername).To(Equal("localai-bot"))
			Expect(out.AccountID).To(Equal(botID), "the bot user stands in for the application")
		})

		It("reports a refused token as unauthorized, without the token", func() {
			f.on["GET /users/@me"] = func(restCall) reply { return reply{Status: 401, Body: `{"message":"401: Unauthorized"}`} }
			out := call(rt, contract.ConnectorRequest{Op: "me"})
			Expect(out.Error).NotTo(BeNil())
			Expect(out.Error.Code).To(Equal(contract.ConnectorUnauthorized))
			Expect(out.Error.Message).NotTo(ContainSubstring("SECRET"))
		})
	})

	Describe("send", func() {
		It("sends with allowed_mentions that parse nothing, and breaks up the mentions of the text", func() {
			out := call(rt, contract.ConnectorRequest{Op: "send", Chat: chanID, Text: "hi @everyone and @here, <@400000000000000001> and <@&600000000000000001>"})
			Expect(out.Error).To(BeNil())
			Expect(out.MessageID).NotTo(BeEmpty())
			c := f.called("POST /channels/:id/messages")
			Expect(c).To(HaveLen(1))
			Expect(c[0].Path).To(Equal("/channels/" + chanID + "/messages"))
			Expect(c[0].Header.Get("Authorization")).To(Equal("Bot " + secretToken))
			Expect(c[0].Body["allowed_mentions"]).To(Equal(map[string]any{"parse": []any{}}))
			text := c[0].Body["content"].(string)
			Expect(text).NotTo(ContainSubstring("@everyone"))
			Expect(text).NotTo(ContainSubstring("@here"))
			Expect(text).NotTo(ContainSubstring("<@4"))
			Expect(text).NotTo(ContainSubstring("<@&"))
			Expect(strings.ReplaceAll(text, "​", "")).To(Equal("hi @everyone and @here, <@400000000000000001> and <@&600000000000000001>"), "the text reads the same")
		})

		It("cuts a long answer into messages of at most 2000 characters, none of them without allowed_mentions", func() {
			long := strings.Repeat("word ", 1500)
			out := call(rt, contract.ConnectorRequest{Op: "send", Chat: chanID, Text: long})
			Expect(out.Error).To(BeNil())
			calls := f.called("POST /channels/:id/messages")
			Expect(len(calls)).To(BeNumerically(">=", 4))
			var all strings.Builder
			for _, c := range calls {
				text := c.Body["content"].(string)
				Expect([]rune(text)).To(HaveLen(len([]rune(text))))
				Expect(len([]rune(text))).To(BeNumerically("<=", 2000))
				Expect(c.Body["allowed_mentions"]).To(Equal(map[string]any{"parse": []any{}}))
				all.WriteString(text + " ")
			}
			Expect(strings.Fields(all.String())).To(HaveLen(1500))
		})

		It("keeps a code block whole in each part", func() {
			code := "```go\n" + strings.Repeat("x := 1\n", 400) + "```"
			call(rt, contract.ConnectorRequest{Op: "send", Chat: chanID, Text: code})
			for _, c := range f.called("POST /channels/:id/messages") {
				text := c.Body["content"].(string)
				Expect(strings.Count(text, "```")%2).To(Equal(0), "each part opens and closes its block")
				Expect(len([]rune(text))).To(BeNumerically("<=", 2000))
			}
		})

		It("writes in the thread when the update has one", func() {
			call(rt, contract.ConnectorRequest{Op: "send", Chat: chanID, Thread: threadI, Text: "in the thread"})
			Expect(f.called("POST /channels/:id/messages")[0].Path).To(Equal("/channels/" + threadI + "/messages"))
		})

		It("opens the direct conversation for a chat of the form user:<id>, once", func() {
			call(rt, contract.ConnectorRequest{Op: "send", Chat: "user:" + userID, Text: "a notification"})
			call(rt, contract.ConnectorRequest{Op: "send", Chat: "user:" + userID, Text: "another"})
			Expect(f.called("POST /users/@me/channels")).To(HaveLen(1))
			Expect(f.called("POST /users/@me/channels")[0].Body).To(HaveKeyWithValue("recipient_id", userID))
			Expect(f.called("POST /channels/:id/messages")[1].Path).To(Equal("/channels/" + dmID + "/messages"))
		})

		It("refuses a chat that is not an ID, so nothing else goes into the URL", func() {
			out := call(rt, contract.ConnectorRequest{Op: "send", Chat: "../users/@me", Text: "x"})
			Expect(out.Error).NotTo(BeNil())
			Expect(f.called("POST /channels/:id/messages")).To(BeEmpty())
		})

		It("replaces a working message with the first part, and posts a new one when that fails", func() {
			out := call(rt, contract.ConnectorRequest{Op: "send", Chat: chanID, MessageID: "800000000000000009", Text: "the answer"})
			Expect(out.Error).To(BeNil())
			Expect(f.called("PATCH /channels/:id/messages/:id")).To(HaveLen(1))
			Expect(f.called("POST /channels/:id/messages")).To(BeEmpty())
			f.on["PATCH /channels/:id/messages/:id"] = func(restCall) reply { return reply{Status: 404, Body: `{"code":10008,"message":"Unknown Message"}`} }
			out = call(rt, contract.ConnectorRequest{Op: "send", Chat: chanID, MessageID: "800000000000000009", Text: "again"})
			Expect(out.Error).To(BeNil())
			Expect(f.called("POST /channels/:id/messages")).To(HaveLen(1))
		})

		It("waits the time Discord asks for after a 429, then sends once more", func() {
			var n atomic.Int32
			f.on["POST /channels/:id/messages"] = func(restCall) reply {
				if n.Add(1) == 1 {
					return reply{Status: 429, Body: `{"message":"You are being rate limited.","retry_after":0.2,"global":false}`, Header: map[string]string{"Retry-After": "1"}}
				}
				return reply{Body: `{"id":"900000000000000001"}`}
			}
			start := time.Now()
			out := call(rt, contract.ConnectorRequest{Op: "send", Chat: chanID, Text: "hello"})
			Expect(out.Error).To(BeNil())
			Expect(out.MessageID).To(Equal("900000000000000001"))
			Expect(n.Load()).To(BeEquivalentTo(2))
			Expect(time.Since(start)).To(BeNumerically(">=", 900*time.Millisecond))
		})

		It("reads the wait from the body when the header is missing, rounded up", func() {
			f.on["POST /channels/:id/messages"] = func(restCall) reply {
				return reply{Status: 429, Body: `{"message":"You are being rate limited.","retry_after":0.4,"global":true}`}
			}
			out := call(rt, contract.ConnectorRequest{Op: "send", Chat: chanID, Text: "x"})
			Expect(out.Error).NotTo(BeNil())
			Expect(out.Error.Code).To(Equal(contract.ConnectorRateLimited))
			Expect(out.Error.RetryAfter).To(Equal(1))
		})

		It("reports a second 429 to the host with the wait", func() {
			f.on["POST /channels/:id/messages"] = func(restCall) reply {
				return reply{Status: 429, Body: `{"retry_after":0.1}`, Header: map[string]string{"Retry-After": "1"}}
			}
			out := call(rt, contract.ConnectorRequest{Op: "send", Chat: chanID, Text: "x"})
			Expect(out.Error).NotTo(BeNil())
			Expect(out.Error.Code).To(Equal(contract.ConnectorRateLimited))
			Expect(out.Error.RetryAfter).To(Equal(1))
		})

		It("keeps the token out of the error of a failed send", func() {
			f.on["POST /channels/:id/messages"] = func(restCall) reply {
				return reply{Status: 403, Body: `{"code":50013,"message":"Missing Permissions ` + secretToken + `"}`}
			}
			out := call(rt, contract.ConnectorRequest{Op: "send", Chat: chanID, Text: "x"})
			Expect(out.Error).NotTo(BeNil())
			Expect(out.Error.Code).To(Equal(contract.ConnectorFailed))
			Expect(out.Error.Message).To(ContainSubstring("50013"))
			Expect(out.Error.Message).NotTo(ContainSubstring("SECRET"))
		})
	})

	Describe("edit", func() {
		It("replaces the text, with allowed_mentions, and cuts it to one message", func() {
			out := call(rt, contract.ConnectorRequest{Op: "edit", Chat: chanID, MessageID: "800000000000000009", Text: strings.Repeat("a ", 2500) + "@everyone"})
			Expect(out.Error).To(BeNil())
			c := f.called("PATCH /channels/:id/messages/:id")
			Expect(c).To(HaveLen(1))
			Expect(c[0].Path).To(Equal("/channels/" + chanID + "/messages/800000000000000009"))
			Expect(len([]rune(c[0].Body["content"].(string)))).To(BeNumerically("<=", 2000))
			Expect(c[0].Body["allowed_mentions"]).To(Equal(map[string]any{"parse": []any{}}))
		})

		It("is not repeated after a 429: a preview is skipped, not waited for", func() {
			f.on["PATCH /channels/:id/messages/:id"] = func(restCall) reply {
				return reply{Status: 429, Body: `{"retry_after":1}`, Header: map[string]string{"Retry-After": "1"}}
			}
			out := call(rt, contract.ConnectorRequest{Op: "edit", Chat: chanID, MessageID: "800000000000000009", Text: "x"})
			Expect(out.Error).NotTo(BeNil())
			Expect(out.Error.Code).To(Equal(contract.ConnectorRateLimited))
			Expect(f.called("PATCH /channels/:id/messages/:id")).To(HaveLen(1))
		})
	})

	It("has no typing indicator: the op does nothing", func() {
		out := call(rt, contract.ConnectorRequest{Op: "typing", Chat: chanID})
		Expect(out.Error).To(BeNil())
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, c := range f.calls {
			Expect(c.Path).NotTo(ContainSubstring("typing"))
		}
	})

	Describe("poll", func() {
		It("hands over the updates with the next offset", func() {
			call(rt, contract.ConnectorRequest{Op: "poll", Offset: 1})
			Eventually(func() int { return len(f.handshakes()) }, 10*time.Second).Should(BeNumerically(">=", 1))
			time.Sleep(100 * time.Millisecond)
			f.dispatch("MESSAGE_CREATE", msg{channel: dmID, sender: userID, text: "hi"}.payload())
			var out contract.ConnectorResponse
			Eventually(func() int {
				out = call(rt, contract.ConnectorRequest{Op: "poll", Offset: 1, TimeoutSeconds: 1})
				return len(out.Updates)
			}, 10*time.Second).Should(Equal(1))
			Expect(out.NextOffset).To(Equal(out.Updates[0].Cursor + 1))
		})

		It("follows no thread with the option followThreads set to never", func() {
			opts := map[string]string{"followThreads": "never"}
			call(rt, contract.ConnectorRequest{Op: "poll", Offset: 1, Options: opts})
			Eventually(func() int { return len(f.handshakes()) }, 10*time.Second).Should(BeNumerically(">=", 1))
			time.Sleep(100 * time.Millisecond)
			f.dispatch("THREAD_CREATE", map[string]any{"id": threadI, "type": 11, "parent_id": chanID, "guild_id": guildID})
			f.dispatch("MESSAGE_CREATE", msg{channel: threadI, guild: guildID, sender: userID, text: "mention", mentionBot: true}.payload())
			f.dispatch("MESSAGE_CREATE", msg{channel: threadI, guild: guildID, sender: userID, text: "no mention"}.payload())
			f.dispatch("MESSAGE_CREATE", msg{channel: dmID, sender: userID, text: "marker"}.payload())
			var out contract.ConnectorResponse
			Eventually(func() int {
				out = call(rt, contract.ConnectorRequest{Op: "poll", Offset: 1, TimeoutSeconds: 1, Options: opts})
				return len(out.Updates)
			}, 10*time.Second).Should(Equal(2))
			Expect(out.Updates[0].Text).To(Equal("mention"))
			Expect(out.Updates[1].Text).To(Equal("marker"))
		})
	})

	Describe("history", func() {
		hist := func(id, sender, text string, extra map[string]any) map[string]any {
			m := map[string]any{"id": id, "channel_id": threadI, "content": text, "type": 0, "timestamp": "2026-11-04T10:00:00.000000+00:00",
				"author": author(sender, "person", sender == "100000000000000009"), "mentions": []any{}, "attachments": []any{}}
			for k, v := range extra {
				m[k] = v
			}
			return m
		}
		BeforeEach(func() {
			// Newest first, as Discord returns them.
			f.history[threadI] = []map[string]any{
				hist("800000000000000007", userID, "the question now", nil),
				hist("800000000000000006", botID, "my earlier answer", nil),
				hist("800000000000000005", userID, "look at this", map[string]any{"attachments": []any{map[string]any{"id": "1", "filename": "plan.pdf", "size": 5}}}),
				hist("800000000000000004", "100000000000000009", "a webhook-ish bot", nil),
				hist("800000000000000003", userID, "joined", map[string]any{"type": 7}),
				hist("800000000000000002", userID, fmt.Sprintf("<@%s> first", botID), map[string]any{"mentions": []any{author(botID, "localai-bot", true)}}),
				hist("800000000000000001", userID, "older", nil),
			}
		})

		It("returns the messages before the one being handled, oldest first, with the bot's own marked", func() {
			out := call(rt, contract.ConnectorRequest{Op: "history", Chat: chanID, Thread: threadI, Before: "800000000000000007", Limit: 30})
			Expect(out.Error).To(BeNil())
			var texts []string
			for _, m := range out.Messages {
				texts = append(texts, m.Text)
			}
			Expect(texts).To(Equal([]string{"older", "first", "look at this", "my earlier answer"}), "no bot, no system message, the bot's mention removed")
			Expect(out.Truncated).To(BeFalse())
			own := out.Messages[3]
			Expect(own.Own).To(BeTrue())
			Expect(out.Messages[2].Media).To(Equal(1))
			Expect(out.Messages[2].MediaNames).To(Equal([]string{"plan.pdf"}))
			Expect(out.Messages[0].SenderName).To(Equal("Person"))
			Expect(out.Messages[0].TS.IsZero()).To(BeFalse())
			c := f.called("GET /channels/:id/messages")
			Expect(c).To(HaveLen(1))
			Expect(c[0].Path).To(Equal("/channels/" + threadI + "/messages"))
			Expect(c[0].Query.Get("before")).To(Equal("800000000000000007"))
			Expect(c[0].Query.Get("limit")).To(Equal("31"), "one more than asked, to know that older ones exist")
		})

		It("says that older messages exist when the limit cuts the thread", func() {
			out := call(rt, contract.ConnectorRequest{Op: "history", Chat: chanID, Thread: threadI, Before: "800000000000000007", Limit: 2})
			Expect(out.Error).To(BeNil())
			Expect(out.Messages).To(HaveLen(2))
			Expect(out.Messages[1].Text).To(Equal("my earlier answer"))
			Expect(out.Truncated).To(BeTrue())
		})

		It("refuses a request without a thread", func() {
			_, err := rt.Connector(context.Background(), contract.ConnectorRequest{Op: "history", Kind: "discord", Token: secretToken, Chat: chanID, Limit: 5})
			Expect(err).To(HaveOccurred())
		})

		It("is not repeated after a 429", func() {
			f.on["GET /channels/:id/messages"] = func(restCall) reply {
				return reply{Status: 429, Body: `{"retry_after":1}`, Header: map[string]string{"Retry-After": "1"}}
			}
			out := call(rt, contract.ConnectorRequest{Op: "history", Chat: chanID, Thread: threadI, Limit: 5})
			Expect(out.Error).NotTo(BeNil())
			Expect(out.Error.Code).To(Equal(contract.ConnectorRateLimited))
			Expect(f.called("GET /channels/:id/messages")).To(HaveLen(1))
		})
	})
})
