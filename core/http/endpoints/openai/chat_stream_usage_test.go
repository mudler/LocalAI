package openai

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/functions"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These tests pin LocalAI's streaming chunks to the OpenAI spec for the
// `usage` field. The regression that motivated them (issue #8546) was that
// LocalAI emitted `"usage":{...zeros...}` on every chunk, which made the
// official OpenAI Node SDK consumers (Continue, Kilo Code, Roo Code, Zed,
// IntelliJ Continue) drop every content chunk via the filter at
// continuedev/continue packages/openai-adapters/src/apis/OpenAI.ts:275-288.
//
// Per OpenAI's chat-completion streaming contract:
//   - intermediate chunks MUST NOT carry a `usage` field
//   - usage is only delivered when the request opts in via
//     `stream_options.include_usage: true`, on a final extra chunk whose
//     `choices` is an empty array.

var _ = Describe("streaming usage spec compliance", func() {
	Describe("OpenAIResponse JSON shape", func() {
		It("does not emit a 'usage' key when Usage is unset", func() {
			// A typical intermediate token chunk: no Usage populated.
			content := "hello"
			resp := schema.OpenAIResponse{
				ID:      "req-1",
				Created: 1,
				Model:   "m",
				Object:  "chat.completion.chunk",
				Choices: []schema.Choice{{
					Index: 0,
					Delta: &schema.Message{Content: &content},
				}},
			}
			data, err := json.Marshal(resp)
			Expect(err).ToNot(HaveOccurred())

			var raw map[string]any
			Expect(json.Unmarshal(data, &raw)).To(Succeed())
			_, present := raw["usage"]
			Expect(present).To(BeFalse(),
				"intermediate chunk must not include a 'usage' key; got: %s", string(data))
		})

		It("emits the usage object when Usage is explicitly set", func() {
			usage := &schema.OpenAIUsage{PromptTokens: 11, CompletionTokens: 22, TotalTokens: 33}
			resp := schema.OpenAIResponse{
				ID:      "req-1",
				Created: 1,
				Model:   "m",
				Object:  "chat.completion.chunk",
				Usage:   usage,
			}
			data, err := json.Marshal(resp)
			Expect(err).ToNot(HaveOccurred())

			var raw map[string]any
			Expect(json.Unmarshal(data, &raw)).To(Succeed())
			u, ok := raw["usage"].(map[string]any)
			Expect(ok).To(BeTrue(), "expected 'usage' object, got: %s", string(data))
			Expect(u["prompt_tokens"]).To(BeNumerically("==", 11))
			Expect(u["completion_tokens"]).To(BeNumerically("==", 22))
			Expect(u["total_tokens"]).To(BeNumerically("==", 33))
		})
	})

	Describe("buildNoActionFinalChunks", func() {
		It("returns chunks with no Usage embedded", func() {
			// Whatever the caller is doing, helpers must not bake usage
			// into intermediate or final delta chunks. The usage trailer
			// (when requested via include_usage) is emitted separately.
			chunks := buildNoActionFinalChunks(
				"req-1", "m", 1,
				false, false,
				"hi", "",
			)
			Expect(chunks).ToNot(BeEmpty())
			for i, ch := range chunks {
				Expect(ch.Usage).To(BeNil(),
					"chunk[%d] must not carry Usage; got %+v", i, ch.Usage)
			}
		})

		It("returns chunks with no Usage when only trailing reasoning needs delivery", func() {
			chunks := buildNoActionFinalChunks(
				"req-1", "m", 1,
				true, false,
				"", "autoparser late reasoning",
			)
			Expect(chunks).ToNot(BeEmpty())
			for i, ch := range chunks {
				Expect(ch.Usage).To(BeNil(),
					"chunk[%d] must not carry Usage; got %+v", i, ch.Usage)
			}
		})
	})

	Describe("buildDeferredToolCallChunks", func() {
		It("returns chunks with no Usage embedded", func() {
			calls := []functions.FuncCallResults{{
				Name: "do_thing", Arguments: `{"x":1}`,
			}}
			chunks := buildDeferredToolCallChunks(
				"req-1", "m", 1, calls, 0,
				false, "", false, "",
			)
			Expect(chunks).ToNot(BeEmpty())
			for i, ch := range chunks {
				Expect(ch.Usage).To(BeNil(),
					"chunk[%d] must not carry Usage; got %+v", i, ch.Usage)
			}
		})
	})

	Describe("streamUsageTrailerJSON", func() {
		It("produces JSON matching the OpenAI spec for the trailer chunk", func() {
			// Trailing usage chunk shape (OpenAI streaming spec):
			//   {"id":"...","object":"chat.completion.chunk","created":...,
			//    "model":"...","choices":[],"usage":{...}}
			usage := schema.OpenAIUsage{
				PromptTokens: 18, CompletionTokens: 14, TotalTokens: 32,
			}
			data := streamUsageTrailerJSON("req-1", "m", 1, usage)

			var raw map[string]any
			Expect(json.Unmarshal(data, &raw)).To(Succeed(),
				"trailer must be valid JSON, got: %s", string(data))

			Expect(raw["id"]).To(Equal("req-1"))
			Expect(raw["model"]).To(Equal("m"))
			Expect(raw["object"]).To(Equal("chat.completion.chunk"))
			Expect(raw["created"]).To(BeNumerically("==", 1))

			// `choices` MUST be present as an empty array (not absent, not null).
			rawChoices, present := raw["choices"]
			Expect(present).To(BeTrue(), "choices key must be present, got: %s", string(data))
			choicesArr, ok := rawChoices.([]any)
			Expect(ok).To(BeTrue(), "choices must serialize as an array, got: %s", string(data))
			Expect(choicesArr).To(BeEmpty(), "choices must be empty in usage trailer, got: %s", string(data))

			// `usage` MUST be present and non-null with the populated counts.
			u, ok := raw["usage"].(map[string]any)
			Expect(ok).To(BeTrue(), "usage object must be present, got: %s", string(data))
			Expect(u["prompt_tokens"]).To(BeNumerically("==", 18))
			Expect(u["completion_tokens"]).To(BeNumerically("==", 14))
			Expect(u["total_tokens"]).To(BeNumerically("==", 32))
		})
	})

	Describe("OpenAIRequest.StreamOptions", func() {
		It("parses stream_options.include_usage=true", func() {
			body := []byte(`{
                "model": "m",
                "stream": true,
                "stream_options": {"include_usage": true},
                "messages": []
            }`)
			var req schema.OpenAIRequest
			Expect(json.Unmarshal(body, &req)).To(Succeed())
			Expect(req.StreamOptions).ToNot(BeNil())
			Expect(req.StreamOptions.IncludeUsage).To(BeTrue())
		})

		It("defaults IncludeUsage to false when stream_options is absent", func() {
			body := []byte(`{"model":"m","stream":true,"messages":[]}`)
			var req schema.OpenAIRequest
			Expect(json.Unmarshal(body, &req)).To(Succeed())
			// Either a nil StreamOptions or one with IncludeUsage=false is acceptable.
			if req.StreamOptions != nil {
				Expect(req.StreamOptions.IncludeUsage).To(BeFalse())
			}
		})
	})
})
