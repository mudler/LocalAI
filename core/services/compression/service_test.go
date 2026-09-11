package compression

import (
	"context"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type wordCounter struct{}

func (wordCounter) CountMessages(messages []schema.Message) (int, error) {
	total := 0
	for _, message := range messages {
		total += len(strings.Fields(message.StringContent))
	}
	return total, nil
}

var _ = Describe("Context compression", func() {
	It("skips when disabled or below the threshold", func() {
		called := false
		service := New(wordCounter{}, SummarizerFunc(func(context.Context, string, []schema.Message, int) (string, int, error) {
			called = true
			return "summary", 1, nil
		}))
		messages := []schema.Message{{Role: "user", StringContent: "one two", Content: "one two"}}

		got, meta, err := service.Transform(context.Background(), config.CompressionConfig{}, 10, "primary", messages)
		Expect(err).NotTo(HaveOccurred())
		Expect(meta).To(BeNil())
		Expect(called).To(BeFalse())
		Expect(got).To(HaveLen(1))
		got, meta, err = service.Transform(context.Background(), config.CompressionConfig{Enabled: true, TriggerAtRatio: .75}, 10, "primary", messages)
		Expect(err).NotTo(HaveOccurred())
		Expect(meta).To(BeNil())
		Expect(called).To(BeFalse())
		Expect(got).To(HaveLen(1))
	})

	It("compresses the head and preserves a tool chain in the tail", func() {
		var compressed []schema.Message
		service := New(wordCounter{}, SummarizerFunc(func(_ context.Context, model string, messages []schema.Message, maxTokens int) (string, int, error) {
			Expect(model).To(Equal("fast"))
			Expect(maxTokens).To(Equal(4))
			compressed = append([]schema.Message(nil), messages...)
			return "facts retained", 2, nil
		}))
		messages := []schema.Message{
			{Role: "system", Content: "system rules", StringContent: "system rules"},
			{Role: "user", Content: "old question with many details", StringContent: "old question with many details"},
			{Role: "assistant", Content: "old answer with many details", StringContent: "old answer with many details"},
			{Role: "assistant", Content: "", StringContent: "call", ToolCalls: []schema.ToolCall{{ID: "c1", Type: "function", FunctionCall: schema.FunctionCall{Name: "lookup", Arguments: `{}`}}}},
			{Role: "tool", ToolCallID: "c1", Content: "tool result", StringContent: "tool result"},
			{Role: "user", Content: "latest question", StringContent: "latest question"},
		}
		policy := config.CompressionConfig{Enabled: true, TriggerAtRatio: .4, KeepTailTokens: 4, MaxSummaryTokens: 4, CompressorModel: "fast"}

		got, meta, err := service.Transform(context.Background(), policy, 20, "primary", messages)
		Expect(err).NotTo(HaveOccurred())
		Expect(compressed).To(HaveLen(4))
		Expect(got).To(HaveLen(3))
		Expect(got[0].Role).To(Equal("system"))
		Expect(got[0].StringContent).To(Equal("system rules"))
		Expect(got[1].StringContent).To(Equal("[COMPRESSED: facts retained]"))
		Expect(compressed[2].ToolCalls[0].ID).To(Equal("c1"))
		Expect(compressed[3].ToolCallID).To(Equal("c1"))
		Expect(got[2].StringContent).To(Equal("latest question"))
		Expect(meta).NotTo(BeNil())
		Expect(*meta).To(Equal(Metadata{OriginalTokens: 17, CompressedTokens: 7, DroppedTurns: 4, Compressor: "fast", SummaryTokens: 2}))
	})

	It("returns overflow when configured to error", func() {
		service := New(wordCounter{}, SummarizerFunc(func(context.Context, string, []schema.Message, int) (string, int, error) {
			return "summary remains much too large", 5, nil
		}))
		messages := []schema.Message{
			{Role: "user", Content: "one two three four five", StringContent: "one two three four five"},
			{Role: "user", Content: "six seven eight nine ten", StringContent: "six seven eight nine ten"},
		}
		policy := config.CompressionConfig{Enabled: true, TriggerAtRatio: .5, KeepTailTokens: 5, MaxSummaryTokens: 5, OnPostCompressionOverflow: "error"}

		_, _, err := service.Transform(context.Background(), policy, 8, "primary", messages)
		Expect(err).To(HaveOccurred())
		Expect(IsOverflow(err)).To(BeTrue())
	})

	It("drops the oldest summary for overflow recovery", func() {
		service := New(wordCounter{}, SummarizerFunc(func(context.Context, string, []schema.Message, int) (string, int, error) {
			return "short summary", 2, nil
		}))
		messages := []schema.Message{
			{Role: "system", Content: "[COMPRESSED: stale summary facts]", StringContent: "[COMPRESSED: stale summary facts]"},
			{Role: "user", Content: "old one two three", StringContent: "old one two three"},
			{Role: "user", Content: "new four five six", StringContent: "new four five six"},
		}
		policy := config.CompressionConfig{Enabled: true, TriggerAtRatio: .5, KeepTailTokens: 4, MaxSummaryTokens: 2, OnPostCompressionOverflow: "drop_oldest_summary"}

		got, meta, err := service.Transform(context.Background(), policy, 6, "primary", messages)
		Expect(err).NotTo(HaveOccurred())
		Expect(meta).NotTo(BeNil())
		Expect(meta.OverflowRecoveries).To(Equal(2))
		for _, message := range got {
			Expect(message.StringContent).NotTo(Equal("[COMPRESSED: stale summary facts]"))
		}
	})

	It("never summarizes the only message", func() {
		called := false
		service := New(wordCounter{}, SummarizerFunc(func(context.Context, string, []schema.Message, int) (string, int, error) {
			called = true
			return "", 0, nil
		}))
		messages := []schema.Message{{Role: "user", Content: "one two three four five", StringContent: "one two three four five"}}

		_, _, err := service.Transform(context.Background(), config.CompressionConfig{Enabled: true, TriggerAtRatio: .5}, 4, "primary", messages)
		Expect(err).To(HaveOccurred())
		Expect(IsOverflow(err)).To(BeTrue())
		Expect(called).To(BeFalse())
	})

	It("keeps a single message that fits the hard limit", func() {
		called := false
		service := New(wordCounter{}, SummarizerFunc(func(context.Context, string, []schema.Message, int) (string, int, error) {
			called = true
			return "", 0, nil
		}))
		messages := []schema.Message{{Role: "user", Content: "one two three four five", StringContent: "one two three four five"}}

		got, meta, err := service.Transform(context.Background(), config.CompressionConfig{Enabled: true, TriggerAtRatio: .5}, 6, "primary", messages)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(messages))
		Expect(meta).To(BeNil())
		Expect(called).To(BeFalse())
	})

	It("retains the newest message beyond the tail budget", func() {
		service := New(wordCounter{}, SummarizerFunc(func(context.Context, string, []schema.Message, int) (string, int, error) {
			return "short", 1, nil
		}))
		messages := []schema.Message{
			{Role: "user", Content: "old one two three", StringContent: "old one two three"},
			{Role: "user", Content: "active four five six seven", StringContent: "active four five six seven"},
		}

		got, _, err := service.Transform(context.Background(), config.CompressionConfig{Enabled: true, TriggerAtRatio: .5, KeepTailTokens: 1}, 10, "primary", messages)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(2))
		Expect(got[1].StringContent).To(Equal("active four five six seven"))
	})

	It("does not recursively summarize an existing summary", func() {
		service := New(wordCounter{}, SummarizerFunc(func(_ context.Context, _ string, messages []schema.Message, _ int) (string, int, error) {
			for _, message := range messages {
				Expect(message.StringContent).NotTo(HavePrefix(summaryPrefix))
			}
			return "new summary", 2, nil
		}))
		messages := []schema.Message{
			{Role: "system", Content: "safety rules", StringContent: "safety rules"},
			{Role: "system", Content: "[COMPRESSED: old facts]", StringContent: "[COMPRESSED: old facts]"},
			{Role: "user", Content: "old question details", StringContent: "old question details"},
			{Role: "user", Content: "active question", StringContent: "active question"},
		}

		got, _, err := service.Transform(context.Background(), config.CompressionConfig{Enabled: true, TriggerAtRatio: .5, KeepTailTokens: 2}, 12, "primary", messages)
		Expect(err).NotTo(HaveOccurred())
		Expect(got[0].StringContent).To(Equal("safety rules"))
		Expect(got[1].StringContent).To(Equal("[COMPRESSED: old facts]"))
	})
})
