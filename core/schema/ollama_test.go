package schema_test

import (
	"encoding/json"

	. "github.com/mudler/LocalAI/core/schema"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OllamaEmbedRequest", func() {

	Context("GetInputStrings", func() {
		It("returns a single string when Input is a string", func() {
			req := OllamaEmbedRequest{Input: "hello world"}

			Expect(req.GetInputStrings()).To(Equal([]string{"hello world"}))
		})

		It("returns a list of strings when Input is a []string", func() {
			req := OllamaEmbedRequest{Input: []string{"hello", "world"}}

			Expect(req.GetInputStrings()).To(Equal([]string{"hello", "world"}))
		})

		It("returns a list of strings when Input is a []any (post JSON unmarshal)", func() {
			req := OllamaEmbedRequest{Input: []any{"hello", "world"}}

			Expect(req.GetInputStrings()).To(Equal([]string{"hello", "world"}))
		})
	})

	Context("JSON unmarshaling (Ollama API compatibility)", func() {
		It("accepts the 'input' field as a single string", func() {
			body := []byte(`{"model": "m", "input": "why is the sky blue?"}`)

			var req OllamaEmbedRequest
			Expect(json.Unmarshal(body, &req)).To(Succeed())

			Expect(req.Model).To(Equal("m"))
			Expect(req.GetInputStrings()).To(Equal([]string{"why is the sky blue?"}))
		})

		It("accepts the 'input' field as an array of strings", func() {
			body := []byte(`{"model": "m", "input": ["why is the sky blue?", "why is the grass green?"]}`)

			var req OllamaEmbedRequest
			Expect(json.Unmarshal(body, &req)).To(Succeed())

			Expect(req.GetInputStrings()).To(Equal([]string{"why is the sky blue?", "why is the grass green?"}))
		})

		// Ollama's embedding endpoint accepts both `input` and `prompt` keys:
		// https://github.com/ollama/ollama/blob/main/docs/api.md#generate-embeddings
		// LocalAI must accept `prompt` so client libraries using that key are not broken.
		// See https://github.com/mudler/LocalAI/issues/9767.
		It("accepts the 'prompt' field as a single string (Ollama compatibility)", func() {
			body := []byte(`{"model": "m", "prompt": "why is the sky blue?"}`)

			var req OllamaEmbedRequest
			Expect(json.Unmarshal(body, &req)).To(Succeed())

			Expect(req.Model).To(Equal("m"))
			Expect(req.GetInputStrings()).To(Equal([]string{"why is the sky blue?"}))
		})

		It("accepts the 'prompt' field as an array of strings (Ollama compatibility)", func() {
			body := []byte(`{"model": "m", "prompt": ["why is the sky blue?", "why is the grass green?"]}`)

			var req OllamaEmbedRequest
			Expect(json.Unmarshal(body, &req)).To(Succeed())

			Expect(req.GetInputStrings()).To(Equal([]string{"why is the sky blue?", "why is the grass green?"}))
		})

		It("prefers 'input' when both 'input' and 'prompt' are provided", func() {
			body := []byte(`{"model": "m", "input": "from input", "prompt": "from prompt"}`)

			var req OllamaEmbedRequest
			Expect(json.Unmarshal(body, &req)).To(Succeed())

			Expect(req.GetInputStrings()).To(Equal([]string{"from input"}))
		})
	})
})
