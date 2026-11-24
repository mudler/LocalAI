// SPDX-License-Identifier: MIT
package transport_test

import (
	"context"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/addons/slack/slacktest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the history of a thread", func() {
	ctx := context.Background()
	history := func(f *slacktest.Server, limit int, before string) contract.ConnectorResponse {
		r := runtimeOf(f)
		out, err := r.Connector(ctx, contract.ConnectorRequest{Op: "history", Kind: "slack", Token: secret, Chat: "C1", Thread: "100.000001", Limit: limit, Before: before})
		Expect(err).NotTo(HaveOccurred())
		return out
	}
	add := func(f *slacktest.Server, ts string, fields map[string]any) {
		f.AddThreadMessage("C1", "100.000001", ts, fields)
	}

	It("returns the messages oldest first with names, the bot's own marked, and files by name", func() {
		f := fake()
		f.AddUser("U2", "bo", "Bo")
		add(f, "100.000001", map[string]any{"user": "U1", "text": "the deploy <https://x.example|log> failed"})
		add(f, "100.000002", map[string]any{"user": "U2", "text": "retrying", "files": []any{map[string]any{"id": "F1", "name": "out.txt"}}})
		add(f, "100.000003", map[string]any{"user": "UBOT", "text": "Done."})
		add(f, "100.000004", map[string]any{"user": "U1", "text": "<@UBOT> and now?"})
		out := history(f, 30, "100.000004")
		Expect(out.Error).To(BeNil())
		Expect(out.Truncated).To(BeFalse())
		Expect(out.Messages).To(HaveLen(3), "the message named by before is not returned")
		m := out.Messages
		Expect([]string{m[0].SenderName, m[0].SenderUsername, m[0].Text}).To(Equal([]string{"Ada Lovelace", "ada", "the deploy log (https://x.example) failed"}))
		Expect(m[1].SenderName).To(Equal("Bo"))
		Expect([]any{m[1].Media, m[1].MediaNames}).To(Equal([]any{1, []string{"out.txt"}}))
		Expect(m[2].Own).To(BeTrue())
		Expect(m[0].Own).To(BeFalse())
		Expect(m[0].TS.Unix()).To(Equal(int64(100)))
	})

	It("keeps the newest messages within the limit and says that older ones exist", func() {
		f := fake()
		for i := 1; i <= 10; i++ {
			add(f, fmt.Sprintf("100.%06d", i), map[string]any{"user": "U1", "text": fmt.Sprintf("m%d", i)})
		}
		out := history(f, 4, "")
		Expect(out.Truncated).To(BeTrue())
		Expect(out.Messages).To(HaveLen(4))
		Expect(out.Messages[0].Text).To(Equal("m7"))
		Expect(out.Messages[3].Text).To(Equal("m10"))
	})

	It("reads a long thread page by page and looks each person up once", func() {
		f := fake()
		for i := 1; i <= 450; i++ {
			add(f, fmt.Sprintf("100.%06d", i), map[string]any{"user": "U1", "text": fmt.Sprintf("m%d", i)})
		}
		out := history(f, 3, "")
		Expect(out.Messages[2].Text).To(Equal("m450"))
		Expect(f.Calls("conversations.replies")).To(Equal(3))
		Expect(f.Calls("users.info")).To(Equal(1))
	})

	It("refuses a thread that is longer than one history reads, rather than show its middle", func() {
		f := fake()
		for i := 1; i <= 1100; i++ {
			add(f, fmt.Sprintf("100.%06d", i), map[string]any{"user": "U1", "text": "x"})
		}
		out := history(f, 3, "")
		Expect(out.Error).NotTo(BeNil())
		Expect(out.Error.Code).To(Equal(contract.ConnectorFailed))
		Expect(f.Calls("conversations.replies")).To(Equal(5))
	})

	It("falls back to the user ID when the name cannot be read, and skips joins", func() {
		f := fake()
		add(f, "100.000001", map[string]any{"user": "U9", "text": "hello"})
		add(f, "100.000002", map[string]any{"user": "U1", "subtype": "channel_join", "text": "joined"})
		out := history(f, 30, "")
		Expect(out.Messages).To(HaveLen(1))
		Expect([]string{out.Messages[0].SenderID, out.Messages[0].SenderName}).To(Equal([]string{"U9", ""}))
	})

	It("types a missing scope, a missing thread and a rate limit", func() {
		f := fake()
		add(f, "100.000001", map[string]any{"user": "U1", "text": "hello"})
		f.FailOnce("conversations.replies", "missing_scope")
		out := history(f, 30, "")
		Expect(out.Error.Code).To(Equal(contract.ConnectorFailed))
		Expect(out.Error.Message).To(ContainSubstring("missing_scope"))
		f.RateLimitOnce("conversations.replies", 7)
		out = history(f, 30, "")
		Expect(out.Error.Code).To(Equal(contract.ConnectorRateLimited))
		Expect(out.Error.RetryAfter).To(Equal(7))
		Expect(f.Calls("conversations.replies")).To(Equal(2), "a rate limit is not waited out")
		Expect(strings.Count(out.Error.Message, "xoxb")).To(BeZero())
	})
})
