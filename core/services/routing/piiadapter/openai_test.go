package piiadapter

import (
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OpenAI adapter", func() {
	It("scans string content", func() {
		req := &schema.OpenAIRequest{
			Messages: []schema.Message{
				{Role: "user", Content: "hello alice@example.com"},
			},
		}
		adapter := OpenAI()
		got := adapter.Scan(req)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Text).To(Equal("hello alice@example.com"))
	})

	It("scans content blocks", func() {
		req := &schema.OpenAIRequest{
			Messages: []schema.Message{
				{Role: "user", Content: []any{
					map[string]any{"type": "text", "text": "block one"},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,xyz"}},
					map[string]any{"type": "text", "text": "block two"},
				}},
			},
		}
		adapter := OpenAI()
		got := adapter.Scan(req)
		Expect(got).To(HaveLen(2))
		Expect(got[0].Text).To(Equal("block one"))
		Expect(got[1].Text).To(Equal("block two"))
	})

	It("Apply mutates string content", func() {
		req := &schema.OpenAIRequest{
			Messages: []schema.Message{
				{Role: "user", Content: "original"},
				{Role: "user", Content: "second"},
			},
		}
		adapter := OpenAI()
		scans := adapter.Scan(req)
		updates := scans
		updates[0].Text = "REDACTED-0"
		updates[1].Text = "REDACTED-1"
		adapter.Apply(req, updates)

		Expect(req.Messages[0].Content.(string)).To(Equal("REDACTED-0"))
		Expect(req.Messages[1].Content.(string)).To(Equal("REDACTED-1"))
	})

	It("Apply mutates content block selectively", func() {
		req := &schema.OpenAIRequest{
			Messages: []schema.Message{
				{Role: "user", Content: []any{
					map[string]any{"type": "text", "text": "before"},
					map[string]any{"type": "text", "text": "untouched"},
				}},
			},
		}
		adapter := OpenAI()
		scans := adapter.Scan(req)
		Expect(scans).To(HaveLen(2))

		// Redact only the first block.
		updates := []struct{ idx int }{{0}}
		scans[updates[0].idx].Text = "AFTER"
		adapter.Apply(req, scans[:1])

		blocks := req.Messages[0].Content.([]any)
		Expect(blocks[0].(map[string]any)["text"]).To(Equal("AFTER"))
		Expect(blocks[1].(map[string]any)["text"]).To(Equal("untouched"))
	})
})

var _ = Describe("encodeIdx/decodeIdx", func() {
	It("round-trips message and block indices", func() {
		cases := []struct{ msg, block int }{
			{0, 0}, {0, 5}, {3, 0}, {3, 12}, {7, -1}, {0, -1},
		}
		for _, c := range cases {
			got := encodeIdx(c.msg, c.block)
			m, b := decodeIdx(got)
			Expect(m).To(Equal(c.msg), "round-trip msg for (%d,%d)", c.msg, c.block)
			Expect(b).To(Equal(c.block), "round-trip block for (%d,%d)", c.msg, c.block)
		}
	})
})
