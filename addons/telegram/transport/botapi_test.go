// SPDX-License-Identifier: MIT
package transport_test

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the Bot API client", func() {
	ctx := context.Background()

	It("reads text and marks the rest", func() {
		f := newFakeBot()
		f.reply["getUpdates"] = func(map[string]any) (int, string) {
			return 200, `{"ok":true,"result":[
 {"update_id":10,"message":{"chat":{"id":55,"type":"private"},"from":{"id":55,"first_name":"Ada","last_name":"L","username":"ada"},"text":"hello"}},
 {"update_id":11,"message":{"chat":{"id":55,"type":"private"},"from":{"id":55,"first_name":"Ada"},"photo":[{"file_id":"x"}]}},
 {"update_id":12,"edited_message":{"chat":{"id":55,"type":"private"},"from":{"id":55},"text":"edit"}},
 {"update_id":13,"channel_post":{"chat":{"id":-100,"type":"channel"},"text":"post"}},
 {"update_id":14,"message":{"chat":{"id":-200,"type":"group"},"from":{"id":7,"first_name":"Bo"},"text":"in a group"}}]}`
		}
		ups, err := f.client().Updates(ctx, secretToken, 10, 25)
		Expect(err).NotTo(HaveOccurred())
		Expect(ups).To(HaveLen(5))
		u := ups[0]
		Expect(u.Cursor).To(Equal(int64(10)))
		Expect([]string{u.Chat, u.SenderID, u.SenderName, u.SenderUsername, u.Text}).To(Equal([]string{"55", "55", "Ada L", "ada", "hello"}))
		Expect(u.Direct).To(BeTrue())
		Expect(u.Unsupported || u.Skip).To(BeFalse())
		Expect(ups[1].Unsupported).To(BeTrue())
		Expect(ups[2].Skip).To(BeTrue())
		Expect(ups[3].Skip).To(BeTrue())
		Expect(ups[4].Direct).To(BeFalse(), "a group is not a direct chat")
		c := f.called("getUpdates")[0]
		Expect(c.Body).To(And(HaveKeyWithValue("offset", float64(10)), HaveKeyWithValue("timeout", float64(25))))
		Expect(c.Path).To(ContainSubstring("/bot" + secretToken + "/"))
	})

	DescribeTable("types an error and keeps the token out of it",
		func(code int, body string, check func(error) bool) {
			f := newFakeBot()
			f.reply["sendMessage"] = func(map[string]any) (int, string) { return code, body }
			_, err := f.client().Send(ctx, secretToken, 1, "hi")
			Expect(err).To(HaveOccurred())
			Expect(check(err)).To(BeTrue(), err.Error())
			Expect(err.Error()).NotTo(ContainSubstring("SECRET"))
		},
		Entry("unauthorized", 401, `{"ok":false,"error_code":401,"description":"Unauthorized"}`, func(e error) bool { return errors.Is(e, transport.ErrUnauthorized) }),
		Entry("conflict", 409, `{"ok":false,"error_code":409,"description":"Conflict"}`, func(e error) bool { return errors.Is(e, transport.ErrConflict) }),
		Entry("retry", 429, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":7}}`, func(e error) bool {
			var r *transport.RetryAfter
			return errors.As(e, &r) && r.Seconds == 7
		}),
		Entry("other", 400, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`, func(e error) bool { return e != nil }),
	)

	It("keeps the token out of the error of a dead connection", func() {
		dead := &transport.API{BaseURL: "http://127.0.0.1:1", Client: &http.Client{}}
		_, err := dead.Me(ctx, secretToken)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).NotTo(ContainSubstring(secretToken))
	})

	It("speaks getMe, sendMessage, editMessageText and sendChatAction", func() {
		f := newFakeBot()
		f.reply["getMe"] = func(map[string]any) (int, string) {
			return 200, `{"ok":true,"result":{"id":99,"is_bot":true,"username":"helper_bot"}}`
		}
		f.reply["sendMessage"] = func(map[string]any) (int, string) { return 200, `{"ok":true,"result":{"message_id":321}}` }
		c := f.client()
		bot, err := c.Me(ctx, secretToken)
		Expect(err).NotTo(HaveOccurred())
		Expect(bot).To(Equal(transport.Bot{ID: 99, Username: "helper_bot"}))
		id, err := c.Send(ctx, secretToken, -42, "plain text")
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal(int64(321)))
		s := f.called("sendMessage")[0].Body
		Expect(s).To(And(HaveKeyWithValue("chat_id", float64(-42)), HaveKeyWithValue("text", "plain text")))
		Expect(s).NotTo(HaveKey("parse_mode"), "the text of a model goes as plain text")
		Expect(c.Edit(ctx, secretToken, -42, 321, "edited")).To(Succeed())
		Expect(f.called("editMessageText")[0].Body).To(And(HaveKeyWithValue("message_id", float64(321)), HaveKeyWithValue("text", "edited")))
		Expect(c.Typing(ctx, secretToken, 5)).To(Succeed())
		Expect(f.called("sendChatAction")[0].Body).To(And(HaveKeyWithValue("action", "typing"), HaveKeyWithValue("chat_id", float64(5))))
	})

	It("keeps a token with odd characters in one path segment", func() {
		f := newFakeBot()
		f.reply["getMe"] = func(map[string]any) (int, string) { return 200, `{"ok":true,"result":{"id":1,"username":"b"}}` }
		_, err := f.client().Me(ctx, "1/../x?y")
		Expect(err).NotTo(HaveOccurred())
		p := f.called("getMe")[0].Path
		Expect(p).NotTo(ContainSubstring("/../"))
		Expect(strings.Count(p, "/")).To(Equal(2))
	})
})
