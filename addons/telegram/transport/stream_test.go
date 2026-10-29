// SPDX-License-Identifier: MIT
package transport_test

import (
	"context"
	"net/url"
	"strings"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// parseRefused is how Telegram refuses MarkdownV2 that it cannot read.
const parseRefused = `{"ok":false,"error_code":400,"description":"Bad Request: can't parse entities: Character '.' is reserved"}`

var _ = Describe("drafts, answers in MarkdownV2 and the token in errors", func() {
	ctx := context.Background()
	run := func(f *fakeBot) transport.Runtime { return transport.Runtime{API: f.client()} }

	It("shows a draft with its ID, in the topic, as plain text", func() {
		f := newFakeBot()
		out, err := run(f).Connector(ctx, contract.ConnectorRequest{Op: "draft", Kind: "telegram", Token: secretToken, Chat: "5", Thread: "9", DraftID: 42, Text: "half an *answer ▉"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Error).To(BeNil())
		d := f.called("sendMessageDraft")
		Expect(d).To(HaveLen(1))
		Expect(d[0].Body).To(And(HaveKeyWithValue("chat_id", float64(5)), HaveKeyWithValue("draft_id", float64(42)),
			HaveKeyWithValue("message_thread_id", float64(9)), HaveKeyWithValue("text", "half an *answer ▉")))
		Expect(d[0].Body).NotTo(HaveKey("parse_mode"), "a preview may hold open markup")
	})

	It("refuses a draft without an ID, and the API refuses one too", func() {
		f := newFakeBot()
		_, err := run(f).Connector(ctx, contract.ConnectorRequest{Op: "draft", Kind: "telegram", Token: secretToken, Chat: "5", Text: "x"})
		Expect(err).To(MatchError(contract.ErrRefused))
		Expect(f.client().Draft(ctx, secretToken, 5, 0, 0, "x")).To(MatchError(ContainSubstring("draft ID")))
		Expect(f.called("sendMessageDraft")).To(BeEmpty())
	})

	It("answers a refused draft with a typed error, so the host can fall back", func() {
		f := newFakeBot()
		f.reply["sendMessageDraft"] = func(map[string]any) (int, string) {
			return 400, `{"ok":false,"error_code":400,"description":"Bad Request: method is available only in private chats"}`
		}
		out, err := run(f).Connector(ctx, contract.ConnectorRequest{Op: "draft", Kind: "telegram", Token: secretToken, Chat: "-5", DraftID: 1, Text: "x"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Error.Code).To(Equal(contract.ConnectorFailed))
	})

	It("sends an answer as MarkdownV2 without a link preview, and a preview edit as plain text", func() {
		f := newFakeBot()
		r := run(f)
		_, err := r.Connector(ctx, contract.ConnectorRequest{Op: "send", Kind: "telegram", Token: secretToken, Chat: "5", MessageID: "6", Text: "**Done.** See [docs](https://example.com/a_(b))."})
		Expect(err).NotTo(HaveOccurred())
		e := f.called("editMessageText")[0].Body
		Expect(e).To(And(HaveKeyWithValue("parse_mode", "MarkdownV2"),
			HaveKeyWithValue("text", "*Done\\.* See [docs](https://example.com/a_\\(b\\))\\.")))
		Expect(e).To(HaveKeyWithValue("link_preview_options", HaveKeyWithValue("is_disabled", true)))
		_, err = r.Connector(ctx, contract.ConnectorRequest{Op: "edit", Kind: "telegram", Token: secretToken, Chat: "5", MessageID: "6", Text: "**Do ▉"})
		Expect(err).NotTo(HaveOccurred())
		p := f.called("editMessageText")[1].Body
		Expect(p).NotTo(HaveKey("parse_mode"))
		Expect(p).To(HaveKeyWithValue("text", "**Do ▉"))
	})

	It("sends a part as plain text when Telegram cannot parse its markup, and only that part", func() {
		f := newFakeBot()
		f.reply["sendMessage"] = func(body map[string]any) (int, string) {
			if body["parse_mode"] == "MarkdownV2" && strings.Contains(body["text"].(string), "second") {
				return 400, parseRefused
			}
			return 200, `{"ok":true,"result":{"message_id":3}}`
		}
		text := "first " + strings.Repeat("a", 4080) + "\n\nsecond [link](https://example.com) **b**"
		_, err := run(f).Connector(ctx, contract.ConnectorRequest{Op: "send", Kind: "telegram", Token: secretToken, Chat: "5", Text: text})
		Expect(err).NotTo(HaveOccurred())
		calls := f.called("sendMessage")
		Expect(calls).To(HaveLen(3), "the first part, the second refused, the second again")
		Expect(calls[0].Body).To(HaveKeyWithValue("parse_mode", "MarkdownV2"))
		Expect(calls[2].Body).NotTo(HaveKey("parse_mode"))
		Expect(calls[2].Body).To(HaveKeyWithValue("text", "second link (https://example.com) b"))
	})

	It("cuts a long answer at 4096 characters without breaking a code block", func() {
		f := newFakeBot()
		text := "Here:\n```python\n" + strings.Repeat("print('x')\n", 500) + "```\nDone."
		_, err := run(f).Connector(ctx, contract.ConnectorRequest{Op: "send", Kind: "telegram", Token: secretToken, Chat: "5", Text: text})
		Expect(err).NotTo(HaveOccurred())
		calls := f.called("sendMessage")
		Expect(len(calls)).To(BeNumerically(">", 1))
		for i, c := range calls {
			t := c.Body["text"].(string)
			Expect(strings.Count(t, "```")%2).To(Equal(0), "part %d", i)
			if i > 0 {
				Expect(t).To(HavePrefix("```python\n"), "part %d opens the block again with its language", i)
			}
		}
	})

	It("takes an edit that changes nothing as done, so the answer is not sent twice", func() {
		f := newFakeBot()
		f.reply["editMessageText"] = func(map[string]any) (int, string) {
			return 400, `{"ok":false,"error_code":400,"description":"Bad Request: message is not modified"}`
		}
		out, err := run(f).Connector(ctx, contract.ConnectorRequest{Op: "send", Kind: "telegram", Token: secretToken, Chat: "5", MessageID: "6", Text: "same"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out.MessageID).To(Equal("6"))
		Expect(f.called("sendMessage")).To(BeEmpty())
	})

	DescribeTable("keeps the token out of every error",
		func(method string, call func(*transport.API) error) {
			f := newFakeBot()
			f.reply[method] = func(map[string]any) (int, string) {
				return 400, `{"ok":false,"error_code":400,"description":"Bad Request: bot ` + secretToken + ` or ` + url.PathEscape(secretToken) + ` is wrong"}`
			}
			err := call(f.client())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).NotTo(ContainSubstring("SECRET"))
			Expect(err.Error()).To(ContainSubstring("<token>"))
		},
		Entry("a draft", "sendMessageDraft", func(a *transport.API) error { return a.Draft(ctx, secretToken, 1, 0, 3, "x") }),
		Entry("an answer", "sendMessage", func(a *transport.API) error {
			_, err := a.SendMarkdown(ctx, secretToken, 1, 0, "x")
			return err
		}),
		Entry("an edit", "editMessageText", func(a *transport.API) error { return a.EditMarkdown(ctx, secretToken, 1, 2, "x") }),
	)

	It("keeps the token out of the error of a dead connection for a draft", func() {
		dead := &transport.API{BaseURL: "http://127.0.0.1:1"}
		err := dead.Draft(ctx, secretToken, 1, 0, 3, "x")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).NotTo(ContainSubstring("SECRET"))
	})
})
