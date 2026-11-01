// SPDX-License-Identifier: MIT
package transport_test

import (
	"context"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/slack/slacktest"
	"github.com/mudler/LocalAI/addons/slack/transport"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the connector route", func() {
	ctx := context.Background()
	call := func(r *transport.Runtime, req contract.ConnectorRequest) contract.ConnectorResponse {
		req.Kind = "slack"
		if req.Token == "" {
			req.Token = secret
		}
		out, err := r.Connector(ctx, req)
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		return out
	}
	// pollUntil polls until the updates are there and hands back the answer.
	pollUntil := func(r *transport.Runtime, offset int64, n int) contract.ConnectorResponse {
		var out contract.ConnectorResponse
		EventuallyWithOffset(1, func() int {
			out = call(r, contract.ConnectorRequest{Op: "poll", Offset: offset, TimeoutSeconds: 1})
			ExpectWithOffset(2, out.Error).To(BeNil())
			return len(out.Updates)
		}, 10*time.Second, 50*time.Millisecond).Should(Equal(n))
		return out
	}

	It("reads a direct message over Socket Mode, acknowledges it and hands it over once", func() {
		f := fake()
		r := runtimeOf(f)
		Expect(call(r, contract.ConnectorRequest{Op: "poll", Offset: 1}).Updates).To(BeEmpty())
		Eventually(func() bool { return f.Connected(appToken) }, 5*time.Second).Should(BeTrue())
		f.Push(appToken, slacktest.DM("D1", "U1", "hello &amp; <https://x.example|x>"))
		out := pollUntil(r, 1, 1)
		u := out.Updates[0]
		Expect([]string{u.Chat, u.Thread, u.SenderID, u.SenderName, u.SenderUsername, u.Text}).
			To(Equal([]string{"D1", "", "U1", "Ada Lovelace", "ada", "hello & x (https://x.example)"}))
		Expect(u.Direct).To(BeTrue())
		Expect(u.MessageID).NotTo(BeEmpty(), "a message names its timestamp, so a thread mode can start a thread from it")
		Expect(u.Cursor).To(BeNumerically(">=", 1))
		Expect(out.NextOffset).To(Equal(u.Cursor + 1))
		Expect(f.Acks()).To(HaveLen(1))
		Expect(call(r, contract.ConnectorRequest{Op: "poll", Offset: out.NextOffset}).Updates).To(BeEmpty(), "a handled update comes back")
		// Slack sends an event again when it missed the acknowledgement.
		f.PushEnvelope(appToken, "EvSame", slacktest.DM("D1", "U1", "again"))
		f.PushEnvelope(appToken, "EvSame", slacktest.DM("D1", "U1", "again"))
		Expect(pollUntil(r, out.NextOffset, 1).Updates[0].Text).To(Equal("again"))
		Consistently(func() int {
			return len(call(r, contract.ConnectorRequest{Op: "poll", Offset: out.NextOffset}).Updates)
		}, 300*time.Millisecond, 50*time.Millisecond).Should(Equal(1), "the repeated event was read twice")
	})

	It("answers a mention in its thread, follows the thread and skips the rest of a channel", func() {
		f := fake()
		r := runtimeOf(f)
		call(r, contract.ConnectorRequest{Op: "poll", Offset: 1})
		Eventually(func() bool { return f.Connected(appToken) }, 5*time.Second).Should(BeTrue())
		mention := slacktest.Mention("C1", "U1", "UBOT", "what is up", "")
		f.Push(appToken, mention)
		// Slack also sends the mention as a plain message: it is not read twice.
		f.Push(appToken, map[string]any{"type": "message", "channel_type": "channel", "channel": "C1", "user": "U1", "text": "<@UBOT> what is up", "ts": mention["ts"]})
		f.Push(appToken, slacktest.Reply("C1", "U1", "not for the bot", "999.1"))
		f.Push(appToken, map[string]any{"type": "message", "channel_type": "channel", "channel": "C1", "user": "U1", "text": "chatter", "ts": "1.2"})
		f.Push(appToken, map[string]any{"type": "message", "subtype": "message_changed", "channel_type": "im", "channel": "D1", "user": "U1", "text": "x"})
		f.Push(appToken, map[string]any{"type": "message", "channel_type": "im", "channel": "D1", "bot_id": "B2", "user": "U9", "text": "a bot"})
		f.Push(appToken, map[string]any{"type": "message", "channel_type": "im", "channel": "D1", "user": "UBOT", "text": "myself"})
		f.Push(appToken, slacktest.Reply("C1", "U2", "a follow-up", mention["ts"].(string)))
		f.Push(appToken, map[string]any{"type": "reaction_added", "user": "U1"})
		f.Push(appToken, map[string]any{"type": "message", "channel_type": "im", "channel": "D1", "user": "U1", "text": "", "files": []any{map[string]any{"id": "F1"}}})
		f.Push(appToken, map[string]any{"type": "message", "channel_type": "im", "channel": "D1", "user": "U1", "text": ""})
		out := pollUntil(r, 1, 4)
		m, follow, file, empty := out.Updates[0], out.Updates[1], out.Updates[2], out.Updates[3]
		Expect([]any{m.Chat, m.Thread, m.Text, m.Mention, m.Direct}).To(Equal([]any{"C1", mention["ts"], "what is up", true, false}))
		Expect([]any{follow.Thread, follow.Text, follow.Mention, follow.SenderID}).To(Equal([]any{mention["ts"], "a follow-up", true, "U2"}))
		Expect(follow.SenderName).To(BeEmpty(), "an unknown person keeps only an ID")
		Expect(file.Media).To(Equal([]string{"F1"}))
		Expect(file.Unsupported).To(BeFalse())
		Expect(empty.Unsupported).To(BeTrue())
		// A mention inside a thread answers in that thread.
		f.Push(appToken, slacktest.Mention("C1", "U1", "UBOT", "in a thread", "500.5"))
		Expect(pollUntil(r, out.NextOffset, 1).Updates[0].Thread).To(Equal("500.5"))
	})

	It("waits for an event during a long poll", func() {
		f := fake()
		r := runtimeOf(f)
		call(r, contract.ConnectorRequest{Op: "poll", Offset: 1})
		Eventually(func() bool { return f.Connected(appToken) }, 5*time.Second).Should(BeTrue())
		go func() {
			defer GinkgoRecover()
			time.Sleep(200 * time.Millisecond)
			f.Push(appToken, slacktest.DM("D1", "U1", "late"))
		}()
		start := time.Now()
		out := call(r, contract.ConnectorRequest{Op: "poll", Offset: 1, TimeoutSeconds: 5})
		Expect(out.Updates).To(HaveLen(1))
		Expect(time.Since(start)).To(BeNumerically("<", 4*time.Second))
	})

	It("opens a new link when Slack asks, and stops on a refused token or a disabled link", func() {
		f := fake()
		r := runtimeOf(f)
		call(r, contract.ConnectorRequest{Op: "poll", Offset: 1})
		Eventually(func() bool { return f.Connected(appToken) }, 5*time.Second).Should(BeTrue())
		f.Disconnect(appToken, "refresh_requested")
		Eventually(f.Links, 5*time.Second).Should(Equal(2))
		f.Push(appToken, slacktest.DM("D1", "U1", "after refresh"))
		pollUntil(r, 1, 1)

		f.Disconnect(appToken, "link_disabled")
		Eventually(func() string {
			out := call(r, contract.ConnectorRequest{Op: "poll", Offset: 1, TimeoutSeconds: 1})
			if out.Error == nil {
				return ""
			}
			return out.Error.Code
		}, 10*time.Second, 50*time.Millisecond).Should(Equal(contract.ConnectorUnauthorized))

		f.Refuse(botToken, "token_revoked")
		out := call(r, contract.ConnectorRequest{Op: "poll", Offset: 1, TimeoutSeconds: 2})
		Expect(out.Error).NotTo(BeNil())
		Expect(out.Error.Code).To(Equal(contract.ConnectorUnauthorized))
		Expect(out.Error.Message).NotTo(ContainSubstring("SECRET"))
	})

	It("closes a link that is not polled", func() {
		f := fake()
		r := transport.New(apiOf(f))
		r.Socket.IdleAfter = 200 * time.Millisecond
		DeferCleanup(r.Close)
		call(r, contract.ConnectorRequest{Op: "poll", Offset: 1})
		Eventually(func() bool { return f.Connected(appToken) }, 5*time.Second).Should(BeTrue())
		Eventually(r.Socket.Links, 5*time.Second).Should(Equal(0))
		Eventually(func() bool { return f.Connected(appToken) }, 5*time.Second).Should(BeFalse())
	})

	It("posts in mrkdwn, in the thread, and splits a long answer", func() {
		f := fake()
		r := runtimeOf(f)
		out := call(r, contract.ConnectorRequest{Op: "send", Chat: "C1", Thread: "7.7", Text: "**done** " + strings.Repeat("word ", 1700)})
		Expect(out.Error).To(BeNil())
		posts := f.Posts(botToken)
		Expect(posts).To(HaveLen(3))
		for _, p := range posts {
			Expect(len([]rune(p.Text))).To(BeNumerically("<=", transport.MaxMessageRunes))
			Expect(p.Thread).To(Equal("7.7"))
		}
		Expect(posts[0].Text).To(HavePrefix("*done* "))
		Expect(out.MessageID).To(Equal(posts[2].TS))
	})

	It("shows a working message once, and the answer replaces it", func() {
		f := fake()
		r := runtimeOf(f)
		for range 3 {
			Expect(call(r, contract.ConnectorRequest{Op: "typing", Chat: "D1"}).Error).To(BeNil())
		}
		posts := f.Posts(botToken)
		Expect(posts).To(HaveLen(1))
		Expect(posts[0].Text).To(Equal(transport.Working))
		out := call(r, contract.ConnectorRequest{Op: "send", Chat: "D1", Text: "the answer"})
		posts = f.Posts(botToken)
		Expect(posts).To(HaveLen(2))
		Expect(posts[1]).To(MatchPost("D1", "", "the answer", true))
		Expect(out.MessageID).To(Equal(posts[0].TS))
		call(r, contract.ConnectorRequest{Op: "send", Chat: "D1", Text: "next"})
		Expect(f.Posts(botToken)[2].Edited).To(BeFalse(), "a working message is replaced once")
	})

	It("edits a message, waits once when Slack asks, and types other failures", func() {
		f := fake()
		r := runtimeOf(f)
		Expect(call(r, contract.ConnectorRequest{Op: "edit", Chat: "C1", MessageID: "1.1", Text: "# Title"}).Error).To(BeNil())
		Expect(f.Posts(botToken)[0]).To(MatchPost("C1", "", "*Title*", true))
		f.RateLimitOnce("chat.postMessage", 1)
		start := time.Now()
		Expect(call(r, contract.ConnectorRequest{Op: "send", Chat: "C1", Text: "after a wait"}).Error).To(BeNil())
		Expect(time.Since(start)).To(BeNumerically(">=", 900*time.Millisecond))
		out := call(r, contract.ConnectorRequest{Op: "send", Chat: "", Text: "x"})
		Expect(out.Error.Code).To(Equal(contract.ConnectorFailed))
	})

	It("checks both tokens with me", func() {
		f := fake()
		r := runtimeOf(f)
		out := call(r, contract.ConnectorRequest{Op: "me"})
		Expect(out.Error).To(BeNil())
		Expect([]string{out.BotID, out.BotUsername}).To(Equal([]string{"UBOT", "helper (Acme)"}))
		for _, bad := range []string{botToken, appToken, botToken + " xapp-SECRET-other"} {
			out = call(r, contract.ConnectorRequest{Op: "me", Token: bad})
			Expect(out.Error).NotTo(BeNil(), bad)
			Expect(out.Error.Code).To(Equal(contract.ConnectorUnauthorized), bad)
			Expect(out.Error.Message).NotTo(ContainSubstring("SECRET"))
		}
		for _, op := range []string{"send", "edit", "typing", "media"} {
			out = call(r, contract.ConnectorRequest{Op: op, Token: appToken, Chat: "C1", Text: "x", Media: "F1", MessageID: "1"})
			Expect(out.Error.Code).To(Equal(contract.ConnectorUnauthorized), op)
		}
	})

	It("reads a file of a message, within the limit", func() {
		f := fake()
		r := runtimeOf(f)
		f.AddFile("F1", "plan.pdf", "application/pdf", []byte("%PDF-1"))
		out := call(r, contract.ConnectorRequest{Op: "media", Media: "F1"})
		Expect(out.Error).To(BeNil())
		Expect(*out.File).To(Equal(contract.ConnectorFile{Name: "plan.pdf", MIME: "application/pdf", Data: []byte("%PDF-1")}))
		f.AddFile("F2", "big.bin", "application/octet-stream", make([]byte, contract.MaxConnectorFileBytes+1))
		Expect(call(r, contract.ConnectorRequest{Op: "media", Media: "F2"}).Error.Message).To(ContainSubstring("too large"))
		Expect(call(r, contract.ConnectorRequest{Op: "media", Media: "../etc"}).Error.Message).To(ContainSubstring("not a file ID"))
		Expect(call(r, contract.ConnectorRequest{Op: "media", Media: "F404"}).Error.Message).To(ContainSubstring("file_not_found"))
	})

	It("refuses another kind and an unknown op", func() {
		r := runtimeOf(fake())
		for _, req := range []contract.ConnectorRequest{{Op: "poll", Kind: "telegram", Token: secret}, {Op: "nope", Kind: "slack", Token: secret}, {Op: "poll", Kind: "slack"}} {
			_, err := r.Connector(ctx, req)
			Expect(err).To(MatchError(contract.ErrRefused))
		}
	})

	It("posts no working message of its own while the host shows one", func() {
		f := fake()
		r := runtimeOf(f)
		Expect(call(r, contract.ConnectorRequest{Op: "typing", Chat: "D1", MessageID: "5.5"}).Error).To(BeNil())
		Expect(f.Posts(botToken)).To(BeEmpty())
	})

	It("uploads a file into the thread of the conversation", func() {
		f := fake()
		r := runtimeOf(f)
		out := call(r, contract.ConnectorRequest{Op: "send_file", Chat: "C1", Thread: "7.7",
			File: &contract.ConnectorFile{Name: "report.csv", MIME: "text/csv", Data: []byte("a,b\n1,2\n")}})
		Expect(out.Error).To(BeNil())
		ups := f.Uploads(botToken)
		Expect(ups).To(HaveLen(1))
		Expect(ups[0].Name).To(Equal("report.csv"))
		Expect(ups[0].Title).To(Equal("report.csv"))
		Expect(string(ups[0].Data)).To(Equal("a,b\n1,2\n"))
		Expect([]string{ups[0].Channel, ups[0].Thread}).To(Equal([]string{"C1", "7.7"}))
		Expect(out.MessageID).To(Equal(ups[0].ID))
	})

	It("types a refused upload and keeps the token out of it", func() {
		f := fake()
		r := runtimeOf(f)
		f.Refuse(botToken, "invalid_auth")
		out := call(r, contract.ConnectorRequest{Op: "send_file", Chat: "C1",
			File: &contract.ConnectorFile{Name: "a.txt", Data: []byte("x")}})
		Expect(out.Error.Code).To(Equal(contract.ConnectorUnauthorized))
		Expect(out.Error.Message).NotTo(ContainSubstring("SECRET"))
	})
})
