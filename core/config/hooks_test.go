package config_test

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"

	. "github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"

	gguf "github.com/gpustack/gguf-parser-go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// GGUF metadata value type tags (see github.com/gpustack/gguf-parser-go).
const (
	ggufTypeUint32 uint32 = 4
	ggufTypeString uint32 = 8
	ggufTypeArray  uint32 = 9
)

// writeTestGGUF emits a minimal but valid little-endian GGUF v3 header carrying
// the scalar metadata the llama-cpp hook guesses from plus a large string vocab
// array (tokenizer.ggml.tokens). The big array is exactly what SkipLargeMetadata
// + UseMMap are expected to avoid reading element-by-element, so it must survive a
// round-trip through the real hook without corrupting the guessed defaults.
func writeTestGGUF(path, chatTemplate string, vocab int, ctxTrain uint32) error {
	wStr := func(b *bytes.Buffer, s string) {
		binary.Write(b, binary.LittleEndian, uint64(len(s)))
		b.WriteString(s)
	}
	kvStr := func(b *bytes.Buffer, k, v string) {
		wStr(b, k)
		binary.Write(b, binary.LittleEndian, ggufTypeString)
		wStr(b, v)
	}
	kvU32 := func(b *bytes.Buffer, k string, v uint32) {
		wStr(b, k)
		binary.Write(b, binary.LittleEndian, ggufTypeUint32)
		binary.Write(b, binary.LittleEndian, v)
	}

	var meta bytes.Buffer
	kvStr(&meta, "general.architecture", "llama")
	kvStr(&meta, "general.name", "ReproModel")
	kvU32(&meta, "llama.context_length", ctxTrain)
	kvU32(&meta, "llama.attention.head_count", 32)
	kvU32(&meta, "llama.feed_forward_length", 11008)
	kvU32(&meta, "llama.block_count", 32)
	kvU32(&meta, "tokenizer.ggml.bos_token_id", 1)
	kvStr(&meta, "tokenizer.chat_template", chatTemplate)

	// large array value — the one the optimization skips reading
	wStr(&meta, "tokenizer.ggml.tokens")
	binary.Write(&meta, binary.LittleEndian, ggufTypeArray)
	binary.Write(&meta, binary.LittleEndian, ggufTypeString)
	binary.Write(&meta, binary.LittleEndian, uint64(vocab))
	for i := 0; i < vocab; i++ {
		wStr(&meta, "token")
	}

	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, gguf.GGUFMagicGGUFLe)
	binary.Write(&out, binary.LittleEndian, uint32(3)) // version
	binary.Write(&out, binary.LittleEndian, uint64(0)) // tensor count
	binary.Write(&out, binary.LittleEndian, uint64(9)) // metadata kv count
	out.Write(meta.Bytes())

	return os.WriteFile(path, out.Bytes(), 0o644)
}

var _ = Describe("Backend hooks and parser defaults", func() {
	Context("MatchParserDefaults", func() {
		It("matches Qwen3 family", func() {
			parsers := MatchParserDefaults("Qwen/Qwen3-8B")
			Expect(parsers).NotTo(BeNil())
			Expect(parsers["tool_parser"]).To(Equal("hermes"))
			Expect(parsers["reasoning_parser"]).To(Equal("qwen3"))
		})

		It("matches Qwen3.5 with longest-prefix-first", func() {
			parsers := MatchParserDefaults("Qwen/Qwen3.5-9B")
			Expect(parsers).NotTo(BeNil())
			Expect(parsers["tool_parser"]).To(Equal("qwen3_xml"))
		})

		It("matches Llama-3.3 not Llama-3.2", func() {
			parsers := MatchParserDefaults("meta/Llama-3.3-70B-Instruct")
			Expect(parsers).NotTo(BeNil())
			Expect(parsers["tool_parser"]).To(Equal("llama3_json"))
		})

		It("matches deepseek-r1", func() {
			parsers := MatchParserDefaults("deepseek-ai/DeepSeek-R1")
			Expect(parsers).NotTo(BeNil())
			Expect(parsers["reasoning_parser"]).To(Equal("deepseek_r1"))
			Expect(parsers["tool_parser"]).To(Equal("deepseek_v3"))
		})

		It("returns nil for unknown families", func() {
			Expect(MatchParserDefaults("acme/unknown-model-xyz")).To(BeNil())
		})
	})

	Context("Backend hook registration and execution", func() {
		It("runs registered hook for a backend", func() {
			called := false
			RegisterBackendHook("test-backend-hook", func(cfg *ModelConfig, modelPath string) {
				called = true
				cfg.Description = "modified-by-hook"
			})

			cfg := &ModelConfig{
				Backend: "test-backend-hook",
			}
			// Use the public Prepare path indirectly is heavy; instead exercise via vllmDefaults
			// path, but here just call RegisterBackendHook + we know runBackendHooks is internal.
			// Verify by leveraging Prepare on a fresh ModelConfig with no model path.
			cfg.PredictionOptions = schema.PredictionOptions{}

			// Trigger via Prepare with empty options; this calls runBackendHooks internally.
			cfg.SetDefaults()
			Expect(called).To(BeTrue())
			Expect(cfg.Description).To(Equal("modified-by-hook"))
		})
	})

	Context("vllmDefaults hook", func() {
		It("auto-sets parsers for known model families on vllm backend", func() {
			cfg := &ModelConfig{
				Backend: "vllm",
				PredictionOptions: schema.PredictionOptions{
					BasicModelRequest: schema.BasicModelRequest{
						Model: "Qwen/Qwen3-8B",
					},
				},
			}
			cfg.SetDefaults()

			foundTool := false
			foundReasoning := false
			for _, opt := range cfg.Options {
				if opt == "tool_parser:hermes" {
					foundTool = true
				}
				if opt == "reasoning_parser:qwen3" {
					foundReasoning = true
				}
			}
			Expect(foundTool).To(BeTrue())
			Expect(foundReasoning).To(BeTrue())
		})

		It("does not override user-set tool_parser", func() {
			cfg := &ModelConfig{
				Backend: "vllm",
				Options: []string{"tool_parser:custom"},
				PredictionOptions: schema.PredictionOptions{
					BasicModelRequest: schema.BasicModelRequest{
						Model: "Qwen/Qwen3-8B",
					},
				},
			}
			cfg.SetDefaults()

			count := 0
			for _, opt := range cfg.Options {
				if len(opt) >= len("tool_parser:") && opt[:len("tool_parser:")] == "tool_parser:" {
					count++
				}
			}
			Expect(count).To(Equal(1))
		})

		It("seeds production engine_args defaults", func() {
			cfg := &ModelConfig{Backend: "vllm"}
			cfg.SetDefaults()

			Expect(cfg.EngineArgs).NotTo(BeNil())
			Expect(cfg.EngineArgs["enable_prefix_caching"]).To(Equal(true))
			Expect(cfg.EngineArgs["enable_chunked_prefill"]).To(Equal(true))
		})

		It("does not override user-set engine_args", func() {
			cfg := &ModelConfig{
				Backend: "vllm",
				LLMConfig: LLMConfig{
					EngineArgs: map[string]any{
						"enable_prefix_caching": false,
					},
				},
			}
			cfg.SetDefaults()

			Expect(cfg.EngineArgs["enable_prefix_caching"]).To(Equal(false))
			// chunked_prefill is still seeded since user didn't set it
			Expect(cfg.EngineArgs["enable_chunked_prefill"]).To(Equal(true))
		})
	})

	Context("llamaCppDefaults GGUF guessing", func() {
		// Regression coverage for https://github.com/mudler/LocalAI/issues/9790:
		// the hook reads GGUF headers with SkipLargeMetadata + UseMMap to avoid
		// pulling the whole tokenizer vocab off (slow) disk on every startup. This
		// verifies that skipping the vocab array still yields the correct guessed
		// defaults from the remaining scalar metadata.
		const chatTemplate = "{{ bos_token }}{% for m in messages %}{{ m.content }}{% endfor %}"

		It("guesses defaults from a GGUF whose large vocab is skipped", func() {
			dir := GinkgoT().TempDir()
			modelFile := "repro.gguf"
			Expect(writeTestGGUF(filepath.Join(dir, modelFile), chatTemplate, 50000, 4096)).To(Succeed())

			// A pre-set context size short-circuits the GGUF run-estimate, which
			// needs full tensor info this header-only fixture deliberately omits;
			// the metadata-reading path the optimization touches is unaffected.
			ctxSize := 4096
			cfg := &ModelConfig{
				Backend: "llama-cpp",
				LLMConfig: LLMConfig{ContextSize: &ctxSize},
				PredictionOptions: schema.PredictionOptions{
					BasicModelRequest: schema.BasicModelRequest{Model: modelFile},
				},
			}
			cfg.SetDefaults(ModelPath(dir))

			// chat_template is a scalar string, not part of the skipped array,
			// so it must be captured verbatim.
			Expect(cfg.GetModelTemplate()).To(Equal(chatTemplate))
			// scalar-derived defaults are still applied
			Expect(cfg.ContextSize).NotTo(BeNil())
			Expect(cfg.NGPULayers).NotTo(BeNil())
			Expect(cfg.TemplateConfig.UseTokenizerTemplate).To(BeTrue())
			Expect(cfg.KnownUsecaseStrings).To(ContainElement("FLAG_CHAT"))
		})

		It("resolves context_size=-1 to the model's trained maximum context", func() {
			dir := GinkgoT().TempDir()
			modelFile := "automax.gguf"
			// A distinctive trained max proves we read metadata, not the 4096 default.
			Expect(writeTestGGUF(filepath.Join(dir, modelFile), chatTemplate, 100, 131072)).To(Succeed())

			neg := -1
			cfg := &ModelConfig{
				Backend:   "llama-cpp",
				LLMConfig: LLMConfig{ContextSize: &neg},
				PredictionOptions: schema.PredictionOptions{
					BasicModelRequest: schema.BasicModelRequest{Model: modelFile},
				},
			}
			cfg.SetDefaults(ModelPath(dir))

			Expect(cfg.ContextSize).NotTo(BeNil())
			Expect(*cfg.ContextSize).To(Equal(131072))
		})

		It("falls back to the default context size when the GGUF is unreadable", func() {
			dir := GinkgoT().TempDir()
			Expect(os.WriteFile(filepath.Join(dir, "bad.gguf"), []byte("not a gguf"), 0o644)).To(Succeed())

			cfg := &ModelConfig{
				Backend: "llama-cpp",
				PredictionOptions: schema.PredictionOptions{
					BasicModelRequest: schema.BasicModelRequest{Model: "bad.gguf"},
				},
			}
			cfg.SetDefaults(ModelPath(dir))

			// An unreadable/unparseable GGUF (e.g. a quant type the parser does
			// not know, such as NVFP4) yields no estimate, so the hook must fall
			// back to DefaultContextSize rather than a tiny, surprising value.
			Expect(cfg.ContextSize).NotTo(BeNil())
			Expect(*cfg.ContextSize).To(Equal(DefaultContextSize))
		})

		It("falls back to the default when context_size=-1 but the GGUF is unreadable", func() {
			dir := GinkgoT().TempDir()
			Expect(os.WriteFile(filepath.Join(dir, "bad.gguf"), []byte("not a gguf"), 0o644)).To(Succeed())

			neg := -1
			cfg := &ModelConfig{
				Backend:   "llama-cpp",
				LLMConfig: LLMConfig{ContextSize: &neg},
				PredictionOptions: schema.PredictionOptions{
					BasicModelRequest: schema.BasicModelRequest{Model: "bad.gguf"},
				},
			}
			cfg.SetDefaults(ModelPath(dir))

			Expect(cfg.ContextSize).NotTo(BeNil())
			Expect(*cfg.ContextSize).To(Equal(DefaultContextSize))
		})
	})

	Context("PromptCacheAll default", func() {
		It("defaults to true when omitted from YAML", func() {
			cfg := &ModelConfig{}
			cfg.SetDefaults()

			Expect(cfg.PromptCacheAll).NotTo(BeNil())
			Expect(*cfg.PromptCacheAll).To(BeTrue())
		})

		It("preserves an explicit false from YAML", func() {
			falseV := false
			cfg := &ModelConfig{
				LLMConfig: LLMConfig{PromptCacheAll: &falseV},
			}
			cfg.SetDefaults()

			Expect(cfg.PromptCacheAll).NotTo(BeNil())
			Expect(*cfg.PromptCacheAll).To(BeFalse())
		})

		It("preserves an explicit true from YAML", func() {
			trueV := true
			cfg := &ModelConfig{
				LLMConfig: LLMConfig{PromptCacheAll: &trueV},
			}
			cfg.SetDefaults()

			Expect(cfg.PromptCacheAll).NotTo(BeNil())
			Expect(*cfg.PromptCacheAll).To(BeTrue())
		})
	})
})
