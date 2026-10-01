package agentpool

import (
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAGI/core/conversations"
	coreTypes "github.com/mudler/LocalAGI/core/types"
	"github.com/sashabaranov/go-openai"
)

func jobMessages(opts []coreTypes.JobOption) []openai.ChatCompletionMessage {
	return coreTypes.NewJob(opts...).ConversationHistory
}

var _ = Describe("web chat history", func() {
	It("sends only the message on the first turn", func() {
		tracker := conversations.NewConversationTracker[string](time.Hour)
		msgs := jobMessages(chatJobOptions(tracker, "draft an offer"))
		Expect(msgs).To(HaveLen(1))
		Expect(msgs[0].Role).To(Equal("user"))
		Expect(msgs[0].Content).To(Equal("draft an offer"))
	})

	It("carries the previous answer into the follow-up", func() {
		tracker := conversations.NewConversationTracker[string](time.Hour)
		chatJobOptions(tracker, "draft an offer")
		rememberChatReply(tracker, &coreTypes.JobResult{Response: "offer AG-1: item 3, 15 days"})

		msgs := jobMessages(chatJobOptions(tracker, "add two days to item 3"))
		Expect(msgs).To(HaveLen(3))
		Expect(msgs[0].Content).To(Equal("draft an offer"))
		Expect(msgs[1].Role).To(Equal("assistant"))
		Expect(msgs[1].Content).To(ContainSubstring("15 days"))
		Expect(msgs[2].Content).To(Equal("add two days to item 3"))
	})

	It("does not remember failed or empty runs", func() {
		tracker := conversations.NewConversationTracker[string](time.Hour)
		chatJobOptions(tracker, "first")
		rememberChatReply(tracker, nil)
		rememberChatReply(tracker, &coreTypes.JobResult{Error: errors.New("boom")})
		rememberChatReply(tracker, &coreTypes.JobResult{Response: "  "})

		msgs := jobMessages(chatJobOptions(tracker, "second"))
		for _, m := range msgs {
			Expect(m.Role).To(Equal("user"))
		}
	})

	It("starts over after the conversation expired", func() {
		tracker := conversations.NewConversationTracker[string](10 * time.Millisecond)
		chatJobOptions(tracker, "old")
		rememberChatReply(tracker, &coreTypes.JobResult{Response: "old answer"})
		time.Sleep(30 * time.Millisecond)

		msgs := jobMessages(chatJobOptions(tracker, "new"))
		Expect(msgs).To(HaveLen(1))
		Expect(msgs[0].Content).To(Equal("new"))
	})

	It("falls back to the bare message without a tracker", func() {
		Expect(jobMessages(chatJobOptions(nil, "hi"))).To(HaveLen(1))
	})
})
