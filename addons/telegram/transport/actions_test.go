// SPDX-License-Identifier: MIT
package transport_test

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var _ = Describe("the buttons of a message", func() {
	ctx := context.Background()

	It("sends the actions as one row of inline buttons whose data is the action ID", func() {
		f := newFakeBot()
		f.reply["sendMessage"] = func(map[string]any) (int, string) { return 200, `{"ok":true,"result":{"message_id":9}}` }
		_, err := f.client().Send(ctx, secretToken, 55, 0, "Standup at 09:30", contract.MessageAction{ID: "reminder:r1:snooze", Label: "Snooze 5 min"}, contract.MessageAction{ID: "reminder:r1:skip", Label: "Skip"})
		Expect(err).NotTo(HaveOccurred())
		var body map[string]any
		for _, c := range f.calls {
			if strings.HasSuffix(c.Path, "/sendMessage") {
				body = c.Body
			}
		}
		Expect(body).NotTo(BeNil())
		kb := body["reply_markup"].(map[string]any)["inline_keyboard"].([]any)
		Expect(kb).To(HaveLen(1))
		row := kb[0].([]any)
		Expect(row).To(HaveLen(2))
		Expect(row[0].(map[string]any)["callback_data"]).To(Equal("reminder:r1:snooze"))
		Expect(row[1].(map[string]any)["text"]).To(Equal("Skip"))
	})

	It("sends no keyboard without actions", func() {
		f := newFakeBot()
		f.reply["sendMessage"] = func(map[string]any) (int, string) { return 200, `{"ok":true,"result":{"message_id":9}}` }
		_, err := f.client().Send(ctx, secretToken, 55, 0, "Hello")
		Expect(err).NotTo(HaveOccurred())
		for _, c := range f.calls {
			Expect(c.Body).NotTo(HaveKey("reply_markup"))
		}
	})

	It("reports a tap as an update with the action, and answers the tap", func() {
		f := newFakeBot()
		f.reply["answerCallbackQuery"] = func(map[string]any) (int, string) { return 200, `{"ok":true,"result":true}` }
		f.reply["getUpdates"] = func(body map[string]any) (int, string) {
			return 200, updates(`{"update_id":7,"callback_query":{"id":"cb1","from":{"id":55,"first_name":"Ann"},"message":{"message_id":9,"chat":{"id":55,"type":"private"}},"data":"reminder:r1:skip"}}`,
				`{"update_id":8,"callback_query":{"id":"cb2","from":{"id":55},"data":"x"}}`)
		}
		ups, err := f.client().Updates(ctx, secretToken, 1, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(ups).To(HaveLen(2))
		Expect(ups[0].Action).To(Equal("reminder:r1:skip"))
		Expect(ups[0].Chat).To(Equal("55"))
		Expect(ups[0].SenderID).To(Equal("55"))
		Expect(ups[0].Direct).To(BeTrue())
		Expect(ups[0].ReplyTo).To(Equal("9"))
		Expect(ups[1].Skip).To(BeTrue(), "a tap on a message that is gone")
		answered := 0
		for _, c := range f.calls {
			if strings.HasSuffix(c.Path, "/answerCallbackQuery") {
				answered++
			}
		}
		Expect(answered).To(Equal(2))
	})
})
