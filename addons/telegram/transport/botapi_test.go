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
		Expect(ups[1].Unsupported).To(BeFalse(), "a photo is handed over")
		Expect(ups[1].Media).To(Equal([]string{"photo:x"}))
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
			_, err := f.client().Send(ctx, secretToken, 1, 0, "hi")
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
		id, err := c.Send(ctx, secretToken, -42, 0, "plain text")
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal(int64(321)))
		s := f.called("sendMessage")[0].Body
		Expect(s).To(And(HaveKeyWithValue("chat_id", float64(-42)), HaveKeyWithValue("text", "plain text")))
		Expect(s).NotTo(HaveKey("parse_mode"), "the text of a model goes as plain text")
		Expect(c.Edit(ctx, secretToken, -42, 321, "edited")).To(Succeed())
		Expect(f.called("editMessageText")[0].Body).To(And(HaveKeyWithValue("message_id", float64(321)), HaveKeyWithValue("text", "edited")))
		Expect(c.Typing(ctx, secretToken, 5, 0)).To(Succeed())
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

	It("reads the ID of a message, its reply, its topic and a mention of the bot in a group", func() {
		f := newFakeBot()
		f.reply["getMe"] = func(map[string]any) (int, string) {
			return 200, `{"ok":true,"result":{"id":123456,"username":"helper_bot"}}`
		}
		f.reply["getUpdates"] = func(map[string]any) (int, string) {
			return 200, `{"ok":true,"result":[
 {"update_id":20,"message":{"message_id":501,"chat":{"id":-300,"type":"supergroup"},"from":{"id":7,"first_name":"Bo"},"text":"@Helper_Bot what time is it?","entities":[{"type":"mention","offset":0,"length":11}]}},
 {"update_id":21,"message":{"message_id":502,"chat":{"id":-300,"type":"supergroup"},"from":{"id":7,"first_name":"Bo"},"text":"and tomorrow?","reply_to_message":{"message_id":499,"from":{"id":123456,"is_bot":true}}}},
 {"update_id":22,"message":{"message_id":503,"chat":{"id":-300,"type":"supergroup"},"from":{"id":7,"first_name":"Bo"},"text":"@other_bot hi","entities":[{"type":"mention","offset":0,"length":10}]}},
 {"update_id":23,"message":{"message_id":504,"message_thread_id":77,"is_topic_message":true,"chat":{"id":-300,"type":"supergroup"},"from":{"id":7,"first_name":"Bo"},"text":"in a topic"}},
 {"update_id":24,"message":{"message_id":505,"message_thread_id":504,"chat":{"id":-300,"type":"supergroup"},"from":{"id":7,"first_name":"Bo"},"text":"a reply, not a topic","reply_to_message":{"message_id":504,"from":{"id":7}}}}]}`
		}
		ups, err := f.client().Updates(ctx, secretToken, 20, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(ups).To(HaveLen(5))
		Expect(ups[0].MessageID).To(Equal("501"))
		Expect(ups[0].Mention).To(BeTrue(), "the bot is named, in any case")
		Expect(ups[0].Text).To(Equal("what time is it?"), "the mention of the bot is taken out")
		Expect(ups[1].Mention).To(BeTrue(), "a reply to the bot addresses it")
		Expect(ups[1].ReplyTo).To(Equal("499"))
		Expect(ups[2].Mention).To(BeFalse(), "another bot is named")
		Expect(ups[3].Thread).To(Equal("77"))
		Expect(ups[3].Mention).To(BeFalse())
		Expect(ups[4].Thread).To(BeEmpty(), "a reply chain is not a topic")
		Expect(ups[4].ReplyTo).To(Equal("504"))
		Expect(f.called("getMe")).To(HaveLen(1), "the name of the bot is read once")
		_, err = f.client().Updates(ctx, secretToken, 20, 0)
		Expect(err).NotTo(HaveOccurred())
	})

	It("sends into a topic, and types there", func() {
		f := newFakeBot()
		c := f.client()
		_, err := c.Send(ctx, secretToken, -300, 77, "in the topic")
		Expect(err).NotTo(HaveOccurred())
		Expect(f.called("sendMessage")[0].Body).To(HaveKeyWithValue("message_thread_id", float64(77)))
		Expect(c.Typing(ctx, secretToken, -300, 77)).To(Succeed())
		Expect(f.called("sendChatAction")[0].Body).To(HaveKeyWithValue("message_thread_id", float64(77)))
		_, err = c.Send(ctx, secretToken, 5, 0, "no topic")
		Expect(err).NotTo(HaveOccurred())
		Expect(f.called("sendMessage")[1].Body).NotTo(HaveKey("message_thread_id"))
	})

	It("sends a picture as a photo and anything else as a document", func() {
		f := newFakeBot()
		f.reply["sendPhoto"] = func(map[string]any) (int, string) { return 200, `{"ok":true,"result":{"message_id":61}}` }
		f.reply["sendDocument"] = func(map[string]any) (int, string) { return 200, `{"ok":true,"result":{"message_id":62}}` }
		c := f.client()
		id, err := c.SendFile(ctx, secretToken, 5, 77, "chart.png", "image/png", []byte("png-bytes"))
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal(int64(61)))
		p := f.called("sendPhoto")[0].Body
		Expect(p).To(And(HaveKeyWithValue("chat_id", "5"), HaveKeyWithValue("message_thread_id", "77"), HaveKeyWithValue("_file_field", "photo"), HaveKeyWithValue("_file_name", "chart.png"), HaveKeyWithValue("_file_size", int64(9))))
		id, err = c.SendFile(ctx, secretToken, 5, 0, "report.pdf", "application/pdf", []byte("%PDF"))
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal(int64(62)))
		d := f.called("sendDocument")[0].Body
		Expect(d).To(And(HaveKeyWithValue("_file_field", "document"), HaveKeyWithValue("_file_name", "report.pdf")))
		Expect(d).NotTo(HaveKey("message_thread_id"))
	})

	It("keeps the token out of the error of a refused file", func() {
		f := newFakeBot()
		f.reply["sendDocument"] = func(map[string]any) (int, string) {
			return 400, `{"ok":false,"error_code":400,"description":"Bad Request: file must be non-empty"}`
		}
		_, err := f.client().SendFile(ctx, secretToken, 5, 0, "a.txt", "text/plain", []byte("x"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).NotTo(ContainSubstring("SECRET"))
	})
})
