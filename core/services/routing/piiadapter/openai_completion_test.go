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

	It("returns nothing for an empty / non-matching request", func() {
		Expect(a.Scan(&schema.OpenAIRequest{})).To(BeEmpty())
		Expect(a.Scan(nil)).To(BeNil())
	})
})
