package piiadapter

import (
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/pii"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Anthropic adapter", func() {
	It("scans string content", func() {
		req := &schema.AnthropicRequest{
			Messages: []schema.AnthropicMessage{
				{Role: "user", Content: "hi alice@example.com"},
			},
		}
		got := Anthropic().Scan(req)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Text).To(Equal("hi alice@example.com"))
	})

	It("scans text blocks", func() {
		// AnthropicMessage.Content is `any`. After JSON decode of a real
		// request it is []any of map[string]any blocks, exactly mirroring
		// OpenAI's content-block shape — image blocks must be skipped, text
		// blocks must be scanned.
		req := &schema.AnthropicRequest{
			Messages: []schema.AnthropicMessage{
				{Role: "user", Content: []any{
					map[string]any{"type": "text", "text": "first text"},
					map[string]any{"type": "image", "source": map[string]any{"type": "base64", "data": "..."}},
					map[string]any{"type": "text", "text": "second text"},
				}},
			},
		}
		got := Anthropic().Scan(req)
		Expect(got).To(HaveLen(2))
		Expect(got[0].Text).To(Equal("first text"))
		Expect(got[1].Text).To(Equal("second text"))
	})

	It("Apply mutates string content", func() {
		req := &schema.AnthropicRequest{
			Messages: []schema.AnthropicMessage{
				{Role: "user", Content: "original"},
			},
		}
		adapter := Anthropic()
		got := adapter.Scan(req)
		adapter.Apply(req, []pii.ScannedText{{Index: got[0].Index, Text: "redacted"}})
		Expect(req.Messages[0].Content).To(Equal("redacted"))
	})

	It("Apply mutates text block content", func() {
		req := &schema.AnthropicRequest{
			Messages: []schema.AnthropicMessage{
				{Role: "user", Content: []any{
					map[string]any{"type": "text", "text": "original"},
				}},
			},
		}
		adapter := Anthropic()
		got := adapter.Scan(req)
		adapter.Apply(req, []pii.ScannedText{{Index: got[0].Index, Text: "redacted"}})
		blocks := req.Messages[0].Content.([]any)
		block := blocks[0].(map[string]any)
		Expect(block["text"]).To(Equal("redacted"))
	})
})
