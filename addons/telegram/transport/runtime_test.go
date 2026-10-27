// SPDX-License-Identifier: MIT
package transport_test

import (
	"context"
	"strings"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the connector route", func() {
	ctx := context.Background()
	run := func(f *fakeBot) transport.Runtime { return transport.Runtime{API: f.client()} }

	It("polls and answers the next offset past the last update, skipped ones included", func() {
		f := newFakeBot()
		f.reply["getUpdates"] = func(map[string]any) (int, string) {
			return 200, `{"ok":true,"result":[{"update_id":7,"message":{"chat":{"id":1,"type":"private"},"from":{"id":1},"text":"a"}},{"update_id":8,"edited_message":{}}]}`
		}
		out, err := run(f).Connector(ctx, contract.ConnectorRequest{Op: "poll", Kind: "telegram", Token: secretToken, Offset: 5, TimeoutSeconds: 99})
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Error).To(BeNil())
		Expect(out.Updates).To(HaveLen(2))
		Expect(out.NextOffset).To(Equal(int64(9)))
		Expect(f.called("getUpdates")[0].Body["timeout"]).To(Equal(float64(contract.MaxPollSeconds)), "the wait is capped")
	})

	It("keeps the offset when there is no update", func() {
		f := newFakeBot()
		f.reply["getUpdates"] = func(map[string]any) (int, string) { return 200, `{"ok":true,"result":[]}` }
		out, err := run(f).Connector(ctx, contract.ConnectorRequest{Op: "poll", Kind: "telegram", Token: secretToken, Offset: 5})
		Expect(err).NotTo(HaveOccurred())
		Expect(out.NextOffset).To(Equal(int64(5)))
	})

	It("splits a long text into parts of at most 4096 characters", func() {
		f := newFakeBot()
		f.reply["sendMessage"] = func(map[string]any) (int, string) { return 200, `{"ok":true,"result":{"message_id":4}}` }
		_, err := run(f).Connector(ctx, contract.ConnectorRequest{Op: "send", Kind: "telegram", Token: secretToken, Chat: "9", Text: strings.Repeat("word ", 1900)})
		Expect(err).NotTo(HaveOccurred())
		calls := f.called("sendMessage")
		Expect(calls).To(HaveLen(3))
		for _, c := range calls {
			Expect(len([]rune(c.Body["text"].(string)))).To(BeNumerically("<=", 4096))
		}
	})

	DescribeTable("answers a failure of Telegram with a typed error",
		func(code int, body, want string) {
			f := newFakeBot()
			f.reply["getUpdates"] = func(map[string]any) (int, string) { return code, body }
			out, err := run(f).Connector(ctx, contract.ConnectorRequest{Op: "poll", Kind: "telegram", Token: secretToken})
			Expect(err).NotTo(HaveOccurred())
			Expect(out.Error).NotTo(BeNil())
			Expect(out.Error.Code).To(Equal(want))
			Expect(out.Error.Message).NotTo(ContainSubstring("SECRET"))
		},
		Entry("a refused token", 401, `{"ok":false,"error_code":401,"description":"x"}`, contract.ConnectorUnauthorized),
		Entry("another reader", 409, `{"ok":false,"error_code":409,"description":"x"}`, contract.ConnectorConflict),
		Entry("a wait", 429, `{"ok":false,"error_code":429,"description":"x","parameters":{"retry_after":3}}`, contract.ConnectorRateLimited),
	)

	It("answers who the bot is, types and edits", func() {
		f := newFakeBot()
		f.reply["getMe"] = func(map[string]any) (int, string) { return 200, `{"ok":true,"result":{"id":99,"username":"helper_bot"}}` }
		r := run(f)
		me, err := r.Connector(ctx, contract.ConnectorRequest{Op: "me", Kind: "telegram", Token: secretToken})
		Expect(err).NotTo(HaveOccurred())
		Expect([]string{me.BotID, me.BotUsername}).To(Equal([]string{"99", "helper_bot"}))
		for _, req := range []contract.ConnectorRequest{{Op: "typing", Chat: "5"}, {Op: "edit", Chat: "5", MessageID: "6", Text: "x"}} {
			req.Kind, req.Token = "telegram", secretToken
			out, err := r.Connector(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(out.Error).To(BeNil())
		}
		Expect(f.called("sendChatAction")).To(HaveLen(1))
		Expect(f.called("editMessageText")).To(HaveLen(1))
	})

	It("refuses another kind, a missing token and an unknown op", func() {
		r := run(newFakeBot())
		for _, req := range []contract.ConnectorRequest{{Op: "poll", Kind: "slack", Token: "t"}, {Op: "poll", Kind: "telegram"}, {Op: "nope", Kind: "telegram", Token: "t"}} {
			_, err := r.Connector(ctx, req)
			Expect(err).To(MatchError(contract.ErrRefused))
		}
	})
})
