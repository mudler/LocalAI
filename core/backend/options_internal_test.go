package backend

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/reasoning"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("grpcModelOpts EngineArgs", func() {
	It("serialises engine_args as JSON preserving nested values", func() {
		threads := 1
		cfg := config.ModelConfig{
			Threads: &threads,
			LLMConfig: config.LLMConfig{
				EngineArgs: map[string]any{
					"data_parallel_size":     8,
					"enable_expert_parallel": true,
					"speculative_config": map[string]any{
						"method":                 "ngram",
						"num_speculative_tokens": 4,
					},
				},
			},
		}

		opts := grpcModelOpts(cfg, "/tmp/models")
		Expect(opts.EngineArgs).NotTo(BeEmpty())

		var round map[string]any
		Expect(json.Unmarshal([]byte(opts.EngineArgs), &round)).To(Succeed())
		Expect(round["data_parallel_size"]).To(BeEquivalentTo(8))
		Expect(round["enable_expert_parallel"]).To(BeTrue())
		Expect(round["speculative_config"]).To(HaveKeyWithValue("method", "ngram"))
	})

	It("leaves EngineArgs empty when unset", func() {
		threads := 1
		opts := grpcModelOpts(config.ModelConfig{Threads: &threads}, "/tmp/models")
		Expect(opts.EngineArgs).To(BeEmpty())
	})
})

// Guards the DisableReasoning -> enable_thinking metadata conversion that the
// per-request reasoning_effort feature (issue #10072) relies on: the request
// merge sets ReasoningConfig.DisableReasoning, and gRPCPredictOpts is where it
// becomes the gRPC PredictOptions.Metadata the backend reads.
var _ = Describe("gRPCPredictOpts enable_thinking metadata", func() {
	// withReasoning builds a fully-defaulted config (gRPCPredictOpts dereferences
	// many pointer fields) and overrides only the reasoning toggle.
	withReasoning := func(disable *bool) config.ModelConfig {
		cfg := config.ModelConfig{}
		cfg.SetDefaults()
		cfg.ReasoningConfig = reasoning.Config{DisableReasoning: disable}
		return cfg
	}
	disabled := true
	enabled := false

	It("emits enable_thinking=false when reasoning is disabled", func() {
		opts := gRPCPredictOpts(withReasoning(&disabled), "/tmp/models")
		Expect(opts.Metadata).To(HaveKeyWithValue("enable_thinking", "false"))
	})

	It("emits enable_thinking=true when reasoning is enabled", func() {
		opts := gRPCPredictOpts(withReasoning(&enabled), "/tmp/models")
		Expect(opts.Metadata).To(HaveKeyWithValue("enable_thinking", "true"))
	})

	It("omits enable_thinking when reasoning is unset", func() {
		opts := gRPCPredictOpts(withReasoning(nil), "/tmp/models")
		Expect(opts.Metadata).ToNot(HaveKey("enable_thinking"))
	})
})

// Guards forwarding the effective reasoning_effort into PredictOptions.Metadata,
// where the backend passes it to the jinja chat template (chat_template_kwargs)
// so models like gpt-oss / LFM2.5 honor it.
var _ = Describe("gRPCPredictOpts reasoning_effort metadata", func() {
	withEffort := func(effort string) config.ModelConfig {
		cfg := config.ModelConfig{}
		cfg.SetDefaults()
		cfg.ReasoningEffort = effort
		return cfg
	}

	It("forwards reasoning_effort when set", func() {
		opts := gRPCPredictOpts(withEffort("none"), "/tmp/models")
		Expect(opts.Metadata).To(HaveKeyWithValue("reasoning_effort", "none"))
	})

	It("omits reasoning_effort when empty", func() {
		opts := gRPCPredictOpts(withEffort(""), "/tmp/models")
		Expect(opts.Metadata).ToNot(HaveKey("reasoning_effort"))
	})
})

var _ = Describe("grpcModelOpts NBatch", func() {
	scoreUsecase := config.FLAG_SCORE
	threads := 1
	ctx := 4096

	It("defaults to 512 for an ordinary model", func() {
		cfg := config.ModelConfig{Threads: &threads, LLMConfig: config.LLMConfig{ContextSize: &ctx}}
		opts := grpcModelOpts(cfg, "/tmp/models")
		Expect(opts.NBatch).To(BeEquivalentTo(512))
	})

	It("sizes the batch to the context window for score models", func() {
		// Score models decode the whole prompt+candidate in one
		// llama_decode; n_batch must cover it or the backend aborts.
		cfg := config.ModelConfig{Threads: &threads, LLMConfig: config.LLMConfig{ContextSize: &ctx}, KnownUsecases: &scoreUsecase}
		opts := grpcModelOpts(cfg, "/tmp/models")
		Expect(opts.NBatch).To(BeEquivalentTo(4096))
	})

	It("keeps an explicit batch over the score default", func() {
		cfg := config.ModelConfig{Threads: &threads, LLMConfig: config.LLMConfig{ContextSize: &ctx}, KnownUsecases: &scoreUsecase}
		cfg.Batch = 1024
		opts := grpcModelOpts(cfg, "/tmp/models")
		Expect(opts.NBatch).To(BeEquivalentTo(1024))
	})

	It("sizes the batch to the context window for embedding models", func() {
		// Embedding/rerank pool over the whole sequence in one physical batch
		// (n_ubatch); without this the input is capped at the 512 default and
		// the backend returns "input is too large to process".
		embeddings := true
		cfg := config.ModelConfig{Threads: &threads, LLMConfig: config.LLMConfig{ContextSize: &ctx}}
		cfg.Embeddings = &embeddings
		opts := grpcModelOpts(cfg, "/tmp/models")
		Expect(opts.NBatch).To(BeEquivalentTo(4096))
	})

	It("sizes the batch to the context window for rerank models", func() {
		reranking := true
		cfg := config.ModelConfig{Threads: &threads, LLMConfig: config.LLMConfig{ContextSize: &ctx}}
		cfg.Reranking = &reranking
		opts := grpcModelOpts(cfg, "/tmp/models")
		Expect(opts.NBatch).To(BeEquivalentTo(4096))
	})

	It("sizes the batch to the context window for token-classification (NER) models", func() {
		// The privacy-filter regression: a token_classify model sets
		// embeddings:true but declares known_usecases:[token_classify], which
		// is authoritative and suppresses the embeddings usecase guess — so
		// HasUsecases(FLAG_EMBEDDINGS) is false. Without sizing the batch to
		// the context the NER encoder loads at 512, shrinking the exact-pass
		// window and tripping the GGML_ASSERT on longer inputs.
		tokenClassify := config.FLAG_TOKEN_CLASSIFY
		embeddings := true
		cfg := config.ModelConfig{Threads: &threads, LLMConfig: config.LLMConfig{ContextSize: &ctx}, KnownUsecases: &tokenClassify}
		cfg.Embeddings = &embeddings
		opts := grpcModelOpts(cfg, "/tmp/models")
		Expect(opts.NBatch).To(BeEquivalentTo(4096))
	})

	It("sizes the batch to the effective context for a token_classify model with no explicit context_size", func() {
		// Mirrors the shipped gallery config (no batch, no context_size): the
		// backend defaults n_ctx to 4096, so n_batch must follow.
		tokenClassify := config.FLAG_TOKEN_CLASSIFY
		embeddings := true
		cfg := config.ModelConfig{Threads: &threads, KnownUsecases: &tokenClassify}
		cfg.Embeddings = &embeddings
		Expect(cfg.ContextSize).To(BeNil())
		opts := grpcModelOpts(cfg, "/tmp/models")
		Expect(opts.NBatch).To(BeEquivalentTo(4096))
		Expect(opts.ContextSize).To(BeEquivalentTo(4096))
	})

	It("does not raise the batch when a score model's context is below the default", func() {
		small := 256
		cfg := config.ModelConfig{Threads: &threads, LLMConfig: config.LLMConfig{ContextSize: &small}, KnownUsecases: &scoreUsecase}
		opts := grpcModelOpts(cfg, "/tmp/models")
		Expect(opts.NBatch).To(BeEquivalentTo(512))
	})

	It("sizes the batch to the effective 4096 default for a score model with no explicit context_size", func() {
		// The crash case: the backend defaults n_ctx to 4096, so n_batch must
		// follow even when context_size is unset — otherwise n_batch stays 512
		// against a 4096 window and the score decode hits the GGML_ASSERT.
		cfg := config.ModelConfig{Threads: &threads, KnownUsecases: &scoreUsecase}
		Expect(cfg.ContextSize).To(BeNil())
		opts := grpcModelOpts(cfg, "/tmp/models")
		Expect(opts.NBatch).To(BeEquivalentTo(4096))
		Expect(opts.ContextSize).To(BeEquivalentTo(4096), "n_batch must match the effective n_ctx the backend receives")
	})
})
