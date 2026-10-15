package piiadapter

import (
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/pii"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// applyAll feeds every scanned span back through Apply with the text
// transformed by fn — the shape the middleware uses (scan, redact, apply).
func applyAll(a pii.Adapter, parsed any, fn func(string) string) {
	scanned := a.Scan(parsed)
	updates := make([]pii.ScannedText, 0, len(scanned))
	for _, s := range scanned {
		updates = append(updates, pii.ScannedText{Index: s.Index, Text: fn(s.Text)})
	}
	a.Apply(parsed, updates)
}

var _ = Describe("OpenAICompletion adapter", func() {
	a := OpenAICompletion()

	It("scans and rewrites a string prompt", func() {
		req := &schema.OpenAIRequest{}
		req.Prompt = "contact alice@example.com"
		got := a.Scan(req)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Text).To(Equal("contact alice@example.com"))
		applyAll(a, req, func(string) string { return "REDACTED" })
		Expect(req.Prompt).To(Equal("REDACTED"))
	})

	It("scans array prompt elements and skips non-strings (token ids)", func() {
		req := &schema.OpenAIRequest{}
		req.Prompt = []any{"first secret", float64(42), "second secret"}
		got := a.Scan(req)
		Expect(got).To(HaveLen(2))
		applyAll(a, req, func(s string) string { return "[X]" })
		arr, _ := req.Prompt.([]any)
		Expect(arr).To(Equal([]any{"[X]", float64(42), "[X]"}))
	})

	It("scans Input and Instruction (the edit/embeddings shape)", func() {
		req := &schema.OpenAIRequest{Instruction: "fix the SSN 123-45-6789"}
		req.Input = "my email is bob@example.com"
		got := a.Scan(req)
		Expect(got).To(HaveLen(2))
		applyAll(a, req, func(string) string { return "*" })
		Expect(req.Input).To(Equal("*"))
		Expect(req.Instruction).To(Equal("*"))
	})

	Context("structured content objects", func() {
		image := map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://media.example.invalid/a.png"}}
		newReq := func() *schema.OpenAIRequest {
			req := &schema.OpenAIRequest{}
			req.Input = []any{
				"plain secret",
				map[string]any{"content": []any{
					map[string]any{"type": "text", "text": "object secret"},
					image,
					map[string]any{"type": "text", "text": "second secret"},
				}},
			}
			return req
		}

		It("scans text parts exactly like strings and leaves media alone", func() {
			req := newReq()
			got := a.Scan(req)
			texts := []string{}
			for _, g := range got {
				texts = append(texts, g.Text)
			}
			Expect(texts).To(ConsistOf("plain secret", "object secret", "second secret"))
			applyAll(a, req, func(string) string { return "[X]" })
			arr := req.Input.([]any)
			Expect(arr[0]).To(Equal("[X]"))
			parts := arr[1].(map[string]any)["content"].([]any)
			Expect(parts[0].(map[string]any)["text"]).To(Equal("[X]"))
			Expect(parts[1]).To(Equal(image))
			Expect(parts[2].(map[string]any)["text"]).To(Equal("[X]"))
		})

		It("scans a single content object given without an array", func() {
			req := &schema.OpenAIRequest{}
			req.Input = map[string]any{"content": []any{map[string]any{"type": "text", "text": "solo secret"}}}
			Expect(a.Scan(req)).To(HaveLen(1))
			applyAll(a, req, func(string) string { return "[X]" })
			parts := req.Input.(map[string]any)["content"].([]any)
			Expect(parts[0].(map[string]any)["text"]).To(Equal("[X]"))
		})
	})

	It("returns nothing for an empty / non-matching request", func() {
		Expect(a.Scan(&schema.OpenAIRequest{})).To(BeEmpty())
		Expect(a.Scan(nil)).To(BeNil())
	})
})
