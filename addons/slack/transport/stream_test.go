// SPDX-License-Identifier: MIT
package transport_test

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/addons/slack/slacktest"
	"github.com/mudler/LocalAI/addons/slack/transport"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// visible is what a person reads in the channel: the messages that were
// posted, with their latest text, and without those that were deleted.
func visible(f *slacktest.Server) []string {
	var order []string
	text := map[string]string{}
	for _, p := range f.Posts(botToken) {
		if p.Deleted {
			delete(text, p.TS)
			continue
		}
		if _, ok := text[p.TS]; !ok {
			order = append(order, p.TS)
		}
		text[p.TS] = p.Text
	}
	var out []string
	for _, ts := range order {
		if t, ok := text[ts]; ok {
			out = append(out, t)
		}
	}
	return out
}

var _ = Describe("the answer that replaces a streamed preview", func() {
	ctx := context.Background()
	var waits []time.Duration

	BeforeEach(func() {
		waits = nil
		var mu sync.Mutex
		old := connector.Sleep
		connector.Sleep = func(_ context.Context, d time.Duration) error {
			mu.Lock()
			defer mu.Unlock()
			waits = append(waits, d)
			return nil
		}
		DeferCleanup(func() { connector.Sleep = old })
	})

	// preview posts the preview as the host does, and returns its ID.
	preview := func(r *transport.Runtime, thread string) string {
		out, err := r.Connector(ctx, contract.ConnectorRequest{Op: "send", Kind: "slack", Token: secret, Chat: "C1", Thread: thread, Text: "Working… ▉"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Error).To(BeNil())
		return out.MessageID
	}
	answer := func(r *transport.Runtime, id, text string) contract.ConnectorResponse {
		out, err := r.Connector(ctx, contract.ConnectorRequest{Op: "send", Kind: "slack", Token: secret, Chat: "C1", Thread: "7.7", Text: text, MessageID: id})
		Expect(err).NotTo(HaveOccurred())
		return out
	}

	It("edits the preview in place: one message, without the cursor", func() {
		f := fake()
		r := runtimeOf(f)
		id := preview(r, "7.7")
		out := answer(r, id, "The whole answer.")
		Expect(out.Error).To(BeNil())
		Expect(out.MessageID).To(Equal(id))
		Expect(visible(f)).To(Equal([]string{"The whole answer."}))
	})

	DescribeTable("a rate limited edit waits and tries again, and never posts a second message",
		func(limit func(f *slacktest.Server)) {
			f := fake()
			r := runtimeOf(f)
			id := preview(r, "7.7")
			limit(f)
			Expect(answer(r, id, "Done.").Error).To(BeNil())
			Expect(visible(f)).To(Equal([]string{"Done."}))
			Expect(waits).NotTo(BeEmpty())
		},
		Entry("HTTP 429 with Retry-After", func(f *slacktest.Server) { f.RateLimitOnce("chat.update", 3) }),
		Entry("ratelimited twice", func(f *slacktest.Server) { f.FailTimes("chat.update", "ratelimited", 2) }),
	)

	It("waits as long as Slack asks, up to a bound, and then does not hold the answer", func() {
		f := fake()
		r := runtimeOf(f)
		id := preview(r, "7.7")
		f.RateLimitOnce("chat.update", 3)
		answer(r, id, "Done.")
		Expect(waits).To(Equal([]time.Duration{3 * time.Second}))
		id = preview(r, "8.8")
		f.RateLimitOnce("chat.update", 200)
		out := answer(r, id, "Late.")
		Expect(out.Error).To(BeNil())
		Expect(waits).To(HaveLen(1), "a wait over the bound is not taken")
		Expect(visible(f)).To(Equal([]string{"Done.", "Late."}), "the preview is deleted, the answer stands once")
	})

	DescribeTable("an edit that cannot be made posts the answer and deletes the preview",
		func(code string) {
			f := fake()
			r := runtimeOf(f)
			id := preview(r, "7.7")
			f.FailTimes("chat.update", code, 10)
			out := answer(r, id, "Final answer.")
			Expect(out.Error).To(BeNil())
			Expect(visible(f)).To(Equal([]string{"Final answer."}), "no preview with a cursor stays")
			Expect(out.MessageID).NotTo(Equal(id))
			posts := f.Posts(botToken)
			Expect(posts[len(posts)-1]).To(Equal(slacktest.Post{Token: botToken, Channel: "C1", TS: id, Deleted: true}))
			Expect(posts[1].Thread).To(Equal("7.7"), "the continuation stays in the thread")
		},
		Entry("the message is gone", "message_not_found"),
		Entry("the edit window is closed", "edit_window_closed"),
		Entry("the text is too long", "msg_too_long"),
		Entry("the edit is not allowed", "cant_update_message"),
	)

	It("leaves the answer standing when the preview cannot be deleted either", func() {
		f := fake()
		r := runtimeOf(f)
		id := preview(r, "7.7")
		f.FailTimes("chat.update", "message_not_found", 10)
		f.FailTimes("chat.delete", "cant_delete_message", 10)
		out := answer(r, id, "Final answer.")
		Expect(out.Error).To(BeNil())
		Expect(visible(f)).To(ContainElement("Final answer."))
	})

	It("cuts a long answer only when it exceeds the limit, and replaces the preview with its first part", func() {
		f := fake()
		r := runtimeOf(f)
		id := preview(r, "7.7")
		short := strings.Repeat("word ", 700)
		Expect(len([]rune(short))).To(BeNumerically("<", transport.MaxMessageRunes))
		Expect(answer(r, id, short).Error).To(BeNil())
		Expect(visible(f)).To(HaveLen(1))

		id = preview(r, "9.9")
		long := strings.Repeat("word ", 1700)
		out := answer(r, id, long)
		Expect(out.Error).To(BeNil())
		v := visible(f)
		Expect(v).To(HaveLen(4), "the short answer, then three parts: the first in place of the preview")
		for _, t := range v {
			Expect(t).NotTo(ContainSubstring("▉"))
			Expect(len([]rune(t))).To(BeNumerically("<=", transport.MaxMessageRunes))
		}
		Expect(out.MessageID).NotTo(Equal(id), "the last part is the continuation")
	})

	It("posts every part and deletes the preview when the edit of a long answer fails", func() {
		f := fake()
		r := runtimeOf(f)
		id := preview(r, "7.7")
		f.FailTimes("chat.update", "msg_too_long", 10)
		out := answer(r, id, strings.Repeat("word ", 1700))
		Expect(out.Error).To(BeNil())
		v := visible(f)
		Expect(v).To(HaveLen(3), "the three parts, and no preview")
		Expect(strings.Join(v, "")).NotTo(ContainSubstring("▉"))
	})

	It("deletes a message of the bot on the op delete, and refuses it without a message", func() {
		f := fake()
		r := runtimeOf(f)
		id := preview(r, "7.7")
		out, err := r.Connector(ctx, contract.ConnectorRequest{Op: "delete", Kind: "slack", Token: secret, Chat: "C1", MessageID: id})
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Error).To(BeNil())
		Expect(visible(f)).To(BeEmpty())
		_, err = r.Connector(ctx, contract.ConnectorRequest{Op: "delete", Kind: "slack", Token: secret, Chat: "C1"})
		Expect(err).To(MatchError(contract.ErrRefused))
	})
})

var _ = slacktest.Post{}
