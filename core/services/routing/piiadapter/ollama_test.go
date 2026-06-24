package piiadapter

import (
	"github.com/mudler/LocalAI/core/schema"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Ollama adapters", func() {
	It("OllamaChat scans and rewrites message content", func() {
		req := &schema.OllamaChatRequest{Messages: []schema.OllamaMessage{
			{Role: "user", Content: "I'm alice@example.com"},
			{Role: "assistant", Content: ""},
		}}
		a := OllamaChat()
		Expect(a.Scan(req)).To(HaveLen(1))
		applyAll(a, req, func(string) string { return "X" })
		Expect(req.Messages[0].Content).To(Equal("X"))
		Expect(req.Messages[1].Content).To(Equal(""))
	})

	It("OllamaGenerate scans Prompt and System", func() {
		req := &schema.OllamaGenerateRequest{Prompt: "ssn 123", System: "be terse"}
		a := OllamaGenerate()
		Expect(a.Scan(req)).To(HaveLen(2))
		applyAll(a, req, func(string) string { return "Y" })
		Expect(req.Prompt).To(Equal("Y"))
		Expect(req.System).To(Equal("Y"))
	})

	It("OllamaEmbed scans string and array Input, skipping non-strings", func() {
		a := OllamaEmbed()

		s := &schema.OllamaEmbedRequest{Input: "secret email"}
		Expect(a.Scan(s)).To(HaveLen(1))
		applyAll(a, s, func(string) string { return "Z" })
		Expect(s.Input).To(Equal("Z"))

		arr := &schema.OllamaEmbedRequest{Input: []any{"a secret", float64(1)}}
		Expect(a.Scan(arr)).To(HaveLen(1))
		applyAll(a, arr, func(string) string { return "Z" })
		got, _ := arr.Input.([]any)
		Expect(got).To(Equal([]any{"Z", float64(1)}))
	})
})
