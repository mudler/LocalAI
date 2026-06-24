package middleware

import (
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("routerConfigFingerprint", func() {
	rc := config.RouterConfig{Classifier: "score", ClassifierModel: "arch-router"}
	ctx4096 := 4096
	ctx8192 := 8192

	// Regression: the score classifier bakes context_size into its token
	// budget at build time, and the built classifier is cached by this
	// fingerprint. If context_size weren't hashed, editing it and reloading
	// would return a classifier carrying the stale budget.
	It("changes when the classifier model's context_size changes", func() {
		cfgA := &config.ModelConfig{LLMConfig: config.LLMConfig{ContextSize: &ctx4096}}
		cfgB := &config.ModelConfig{LLMConfig: config.LLMConfig{ContextSize: &ctx8192}}
		Expect(routerConfigFingerprint(rc, cfgA)).NotTo(Equal(routerConfigFingerprint(rc, cfgB)))
	})

	It("is stable for identical classifier configs", func() {
		cfgA := &config.ModelConfig{LLMConfig: config.LLMConfig{ContextSize: &ctx4096}}
		cfgB := &config.ModelConfig{LLMConfig: config.LLMConfig{ContextSize: &ctx4096}}
		Expect(routerConfigFingerprint(rc, cfgA)).To(Equal(routerConfigFingerprint(rc, cfgB)))
	})
})

var _ = Describe("routing probe extraction and trimming", func() {
	Describe("OpenAIProbeFromRequest", func() {
		It("keeps a short conversation intact, newline-terminated per message", func() {
			req := &schema.OpenAIRequest{Messages: []schema.Message{
				{Role: "user", Content: "first"},
				{Role: "assistant", Content: "second"},
				{Role: "user", Content: "third"},
			}}
			Expect(OpenAIProbeFromRequest(req).Prompt).To(Equal("first\nsecond\nthird\n"))
		})

		It("flattens text blocks and skips image-only messages", func() {
			req := &schema.OpenAIRequest{Messages: []schema.Message{
				{Role: "user", Content: []any{
					map[string]any{"type": "text", "text": "describe this"},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:..."}},
				}},
				{Role: "user", Content: []any{
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:..."}},
				}},
			}}
			// Second message contributes no text, so it neither adds a blank
			// line nor a stray newline.
			Expect(OpenAIProbeFromRequest(req).Prompt).To(Equal("describe this\n"))
		})

		It("carries the full conversation untrimmed — trimming is each classifier's job", func() {
			// The middleware no longer caps the probe by a fixed rune budget;
			// every turn reaches the Probe and each classifier trims to its own
			// model's context (see modelTokenTrim / promptTrimmer).
			block := strings.Repeat("x", 999)
			msgs := make([]schema.Message, 0, 20)
			msgs = append(msgs, schema.Message{Role: "user", Content: "OLDEST" + strings.Repeat("o", 994)})
			for range 18 {
				msgs = append(msgs, schema.Message{Role: "user", Content: block})
			}
			msgs = append(msgs, schema.Message{Role: "user", Content: "NEWEST" + strings.Repeat("n", 994)})

			probe := OpenAIProbeFromRequest(&schema.OpenAIRequest{Messages: msgs})
			Expect(probe.Prompt).To(ContainSubstring("OLDEST"), "no turn is dropped at probe-build time")
			Expect(probe.Prompt).To(ContainSubstring("NEWEST"))
			// Messages preserves the per-turn split the classifier trims from.
			Expect(probe.Messages).To(HaveLen(20))
			Expect(probe.Messages[0]).To(ContainSubstring("OLDEST"))
			Expect(probe.Messages[19]).To(ContainSubstring("NEWEST"))
		})
	})

	Describe("AnthropicProbe", func() {
		It("extracts and trims the same way as the OpenAI path", func() {
			req := &schema.AnthropicRequest{Messages: []schema.AnthropicMessage{
				{Role: "user", Content: "alpha"},
				{Role: "assistant", Content: []any{
					map[string]any{"type": "text", "text": "beta"},
				}},
			}}
			probe, ok := AnthropicProbe(req)
			Expect(ok).To(BeTrue())
			Expect(probe.Prompt).To(Equal("alpha\nbeta\n"))
		})

		It("returns ok=false for a non-Anthropic payload", func() {
			_, ok := AnthropicProbe(&schema.OpenAIRequest{})
			Expect(ok).To(BeFalse())
		})
	})

	Describe("modelTokenTrim", func() {
		tok := func(string) (int, error) { return 1, nil }
		depsFor := func(cfg *config.ModelConfig) ClassifierDeps {
			return ClassifierDeps{
				ModelLookup:  func(string) *config.ModelConfig { return cfg },
				TokenCounter: func(string) func(string) (int, error) { return tok },
			}
		}

		It("still trims to the backend default when context_size is unset", func() {
			// Regression: with the fixed middleware rune cap gone, an unset
			// context_size must NOT disable trimming — otherwise a non-trivial
			// prompt overflows the default 4096 window and every score fails.
			score := config.FLAG_SCORE
			cfg := &config.ModelConfig{KnownUsecases: &score} // FLAG_SCORE → batch follows context
			count, ceiling := modelTokenTrim("classifier", depsFor(cfg))
			Expect(count).NotTo(BeNil())
			Expect(ceiling).To(Equal(4096), "unset context_size falls back to the backend default, not 0")
		})

		It("is bounded by the batch when the batch is smaller than the context", func() {
			// The probe is one decode (n_tokens <= n_batch). A model with a
			// large context but a small batch can only process the batch — the
			// ceiling must follow it, not the context.
			ctx8k := 8192
			cfg := &config.ModelConfig{LLMConfig: config.LLMConfig{ContextSize: &ctx8k}}
			cfg.Batch = 512
			_, ceiling := modelTokenTrim("embedder", depsFor(cfg))
			Expect(ceiling).To(Equal(512), "batch is the binding single-decode limit")
		})

		It("disables trimming only when no tokenizer is available", func() {
			count, ceiling := modelTokenTrim("x", ClassifierDeps{ModelLookup: func(string) *config.ModelConfig { return &config.ModelConfig{} }})
			Expect(count).To(BeNil())
			Expect(ceiling).To(Equal(0))
		})
	})
})
