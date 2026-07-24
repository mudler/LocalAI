package templates_test

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/mudler/LocalAI/core/templates"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("RenderConversationForEmbedding", func() {
	var evaluator *Evaluator

	BeforeEach(func() {
		evaluator = NewEvaluator("")
	})

	Context("model without chat templates (frozen fallback)", func() {
		It("renders the exact role-prefixed golden", func() {
			messages := []schema.Message{
				{Role: "system", StringContent: "You are terse."},
				{Role: "user", StringContent: "What is the capital of France?"},
				{Role: "tool", StringContent: "Paris"},
			}
			got := evaluator.RenderConversationForEmbedding(schema.OpenAIRequest{}, messages, &config.ModelConfig{})
			// GOLDEN: the fallback rendering defines the embedding space for
			// template-less models — stored vectors embed this exact string.
			// If this assertion ever fails, the change invalidates every
			// previously stored conversation vector. Do not update the
			// expectation; fix the code.
			Expect(got).To(Equal("system: You are terse.\nuser: What is the capital of France?\ntool: Paris"))
		})

		It("skips messages with empty text content", func() {
			messages := []schema.Message{
				{Role: "user", StringContent: "first"},
				{Role: "assistant", StringContent: ""},
				{Role: "user", StringContent: "second"},
			}
			got := evaluator.RenderConversationForEmbedding(schema.OpenAIRequest{}, messages, &config.ModelConfig{})
			Expect(got).To(Equal("user: first\nuser: second"))
		})

		It("embeds the text parts of a content-part message and ignores parked media", func() {
			// The request middleware decodes multi-part content into
			// StringContent (text) and StringImages/... (media); only the
			// text reaches the embedding.
			messages := []schema.Message{
				{
					Role:          "user",
					StringContent: "describe the image",
					StringImages:  []string{"aGVsbG8="},
				},
			}
			got := evaluator.RenderConversationForEmbedding(schema.OpenAIRequest{}, messages, &config.ModelConfig{})
			Expect(got).To(Equal("user: describe the image"))
		})
	})

	Context("model with chat templates", func() {
		It("renders through TemplateMessages so embeddings match the chat prompt", func() {
			cfg := &config.ModelConfig{
				TemplateConfig: config.TemplateConfig{
					Chat:        "{{.Input}} <end>",
					ChatMessage: "[{{.RoleName}}] {{.Content}}",
				},
			}
			messages := []schema.Message{
				{Role: "system", StringContent: "sys"},
				{Role: "user", StringContent: "hi"},
			}
			got := evaluator.RenderConversationForEmbedding(schema.OpenAIRequest{}, messages, cfg)
			Expect(got).To(Equal("[system] sys\n[user] hi <end>"))
		})

		It("keeps the fallback when only one of the two templates is set", func() {
			cfg := &config.ModelConfig{
				TemplateConfig: config.TemplateConfig{
					ChatMessage: "[{{.RoleName}}] {{.Content}}",
				},
			}
			messages := []schema.Message{{Role: "user", StringContent: "hi"}}
			got := evaluator.RenderConversationForEmbedding(schema.OpenAIRequest{}, messages, cfg)
			Expect(got).To(Equal("user: hi"))
		})
	})
})
