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

// Several Ollama clients (notably Home Assistant's Python client) encode
// integer parameters as JSON floats (`8192.0`). Stdlib json refuses to
// unmarshal those into `int` fields, so OllamaOptions has a custom
// UnmarshalJSON that accepts both forms. See
// https://github.com/mudler/LocalAI/issues/9837.
var _ = Describe("OllamaOptions JSON unmarshaling", func() {
	It("accepts integer literals for int fields", func() {
		body := []byte(`{"num_ctx": 8192, "num_predict": 256, "top_k": 40, "seed": 7, "repeat_last_n": 64}`)

		var opts OllamaOptions
		Expect(json.Unmarshal(body, &opts)).To(Succeed())

		Expect(opts.NumCtx).To(Equal(8192))
		Expect(opts.NumPredict).NotTo(BeNil())
		Expect(*opts.NumPredict).To(Equal(256))
		Expect(opts.TopK).NotTo(BeNil())
		Expect(*opts.TopK).To(Equal(40))
		Expect(opts.Seed).NotTo(BeNil())
		Expect(*opts.Seed).To(Equal(7))
		Expect(opts.RepeatLastN).To(Equal(64))
	})

	It("accepts float literals for int fields (Home Assistant Ollama client)", func() {
		body := []byte(`{"num_ctx": 8192.0, "num_predict": 256.0, "top_k": 40.0, "seed": 7.0, "repeat_last_n": 64.0}`)

		var opts OllamaOptions
		Expect(json.Unmarshal(body, &opts)).To(Succeed())

		Expect(opts.NumCtx).To(Equal(8192))
		Expect(opts.NumPredict).NotTo(BeNil())
		Expect(*opts.NumPredict).To(Equal(256))
		Expect(opts.TopK).NotTo(BeNil())
		Expect(*opts.TopK).To(Equal(40))
		Expect(opts.Seed).NotTo(BeNil())
		Expect(*opts.Seed).To(Equal(7))
		Expect(opts.RepeatLastN).To(Equal(64))
	})

	It("preserves float fields and stop list", func() {
		body := []byte(`{"temperature": 0.7, "top_p": 0.9, "repeat_penalty": 1.1, "stop": ["<|end|>", "</s>"]}`)

		var opts OllamaOptions
		Expect(json.Unmarshal(body, &opts)).To(Succeed())

		Expect(opts.Temperature).NotTo(BeNil())
		Expect(*opts.Temperature).To(Equal(0.7))
		Expect(opts.TopP).NotTo(BeNil())
		Expect(*opts.TopP).To(Equal(0.9))
		Expect(opts.RepeatPenalty).To(Equal(1.1))
		Expect(opts.Stop).To(Equal([]string{"<|end|>", "</s>"}))
	})

	It("leaves optional int fields nil when absent", func() {
		body := []byte(`{}`)

		var opts OllamaOptions
		Expect(json.Unmarshal(body, &opts)).To(Succeed())

		Expect(opts.NumPredict).To(BeNil())
		Expect(opts.TopK).To(BeNil())
		Expect(opts.Seed).To(BeNil())
		Expect(opts.NumCtx).To(Equal(0))
		Expect(opts.RepeatLastN).To(Equal(0))
	})

	It("accepts nested options on a chat request with float num_ctx", func() {
		// Mirrors the payload Home Assistant sends; reproduces issue #9837.
		body := []byte(`{
			"model": "qwen2",
			"messages": [{"role": "user", "content": "hi"}],
			"options": {"num_ctx": 8192.0, "top_k": 40.0}
		}`)

		var req OllamaChatRequest
		Expect(json.Unmarshal(body, &req)).To(Succeed())

		Expect(req.Options).NotTo(BeNil())
		Expect(req.Options.NumCtx).To(Equal(8192))
		Expect(req.Options.TopK).NotTo(BeNil())
		Expect(*req.Options.TopK).To(Equal(40))
	})

	It("rejects non-numeric values with a clear error", func() {
		body := []byte(`{"num_ctx": "not-a-number"}`)

		var opts OllamaOptions
		err := json.Unmarshal(body, &opts)
		Expect(err).To(HaveOccurred())
	})
})
