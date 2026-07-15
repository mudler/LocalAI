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

	// The single-pass batch is now VRAM-aware, so inject a deterministic GPU with
	// ample per-device VRAM: at these small contexts the compute buffer fits
	// easily, so EffectiveBatchSize returns the full context (the pre-#10485
	// behaviour these cases assert). Without injection the value would depend on
	// the CI host's real (often unknown) VRAM.
	const gib = uint64(1) << 30
	var origLocalGPU func() config.GPU
	BeforeEach(func() {
		origLocalGPU = localGPU
		localGPU = func() config.GPU { return config.GPU{VRAM: 119 * gib} }
	})
	AfterEach(func() { localGPU = origLocalGPU })

	It("defaults to 512 for an ordinary model", func() {
		cfg := config.ModelConfig{Threads: &threads, LLMConfig: config.LLMConfig{ContextSize: &ctx}}
		opts := grpcModelOpts(cfg, "/tmp/models")
		Expect(opts.NBatch).To(BeEquivalentTo(512))
		Expect(opts.EnableScore).To(BeFalse())
	})

	It("sizes the batch to the context window for score models", func() {
		// Score models decode the whole prompt+candidate in one
		// llama_decode; n_batch must cover it or the backend aborts.
		cfg := config.ModelConfig{Threads: &threads, LLMConfig: config.LLMConfig{ContextSize: &ctx}, KnownUsecases: &scoreUsecase}
		opts := grpcModelOpts(cfg, "/tmp/models")
		Expect(opts.NBatch).To(BeEquivalentTo(4096))
		Expect(opts.EnableScore).To(BeTrue())
	})

	It("enables score resources for a model with multiple usecases", func() {
		usecases := config.FLAG_CHAT | config.FLAG_SCORE
		cfg := config.ModelConfig{Threads: &threads, LLMConfig: config.LLMConfig{ContextSize: &ctx}, KnownUsecases: &usecases}
		opts := grpcModelOpts(cfg, "/tmp/models")
		Expect(opts.EnableScore).To(BeTrue())
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

// Guards the VRAM-aware cap on the single-pass (embedding/score/rerank) batch:
// a large context must not turn n_ubatch into a multi-GiB compute buffer that
// aborts the load on a device with free VRAM (issue #10485). The GPU is injected
// via the localGPU package var so the cap is deterministic without a real device.
var _ = Describe("EffectiveBatchSize VRAM cap", func() {
	const gib = uint64(1) << 30
	embeddings := config.FLAG_EMBEDDINGS
	threads := 1

	var origLocalGPU func() config.GPU
	BeforeEach(func() { origLocalGPU = localGPU })
	AfterEach(func() { localGPU = origLocalGPU })

	singlePassCfg := func(ctx int) config.ModelConfig {
		return config.ModelConfig{
			Threads:       &threads,
			LLMConfig:     config.LLMConfig{ContextSize: &ctx},
			KnownUsecases: &embeddings,
		}
	}

	It("caps a large embedding context to a batch below the context but at least the default", func() {
		// Reproduces qwen3-embedding-4b: context 40960 on a modest 20 GiB card.
		// Full-context n_ubatch=40960 aborts; the cap must fit the VRAM headroom.
		localGPU = func() config.GPU { return config.GPU{VRAM: 20 * gib} }
		batch := EffectiveBatchSize(singlePassCfg(40960))
		Expect(batch).To(BeNumerically(">=", DefaultBatchSize))
		Expect(batch).To(BeNumerically("<", 40960))
	})

	It("keeps an explicit batch even with a large context and small VRAM", func() {
		localGPU = func() config.GPU { return config.GPU{VRAM: 20 * gib} }
		cfg := singlePassCfg(40960)
		cfg.Batch = 512
		Expect(EffectiveBatchSize(cfg)).To(Equal(512))
	})

	It("returns the full context when per-device VRAM is unknown", func() {
		// Unknown VRAM (CPU / detection gap) preserves the original single-pass
		// behavior: batch follows context. The VRAM cap is a downward safety that
		// only engages when the per-device ceiling is known — clamping here would
		// re-break single-pass pooling and over-trim inputs, with no OOM benefit on
		// CPU where the compute buffer lives in system RAM.
		localGPU = func() config.GPU { return config.GPU{VRAM: 0} }
		Expect(EffectiveBatchSize(singlePassCfg(40960))).To(Equal(40960))
	})

	It("returns the default batch for a non-single-pass model regardless of VRAM", func() {
		localGPU = func() config.GPU { return config.GPU{VRAM: 20 * gib} }
		ctx := 40960
		cfg := config.ModelConfig{Threads: &threads, LLMConfig: config.LLMConfig{ContextSize: &ctx}}
		Expect(EffectiveBatchSize(cfg)).To(Equal(DefaultBatchSize))
	})
})

// Guards the generic chat_template_kwargs forwarding: the model config map plus any
// per-request metadata overrides are merged, coerced, and serialised into the
// backend metadata blob that llama.cpp reads. Client metadata also overrides the
// server-derived standalone enable_thinking key (cross-backend consistency).
var _ = Describe("gRPCPredictOpts chat_template_kwargs metadata", func() {
	baseCfg := func() config.ModelConfig {
		cfg := config.ModelConfig{}
		cfg.SetDefaults()
		return cfg
	}

	It("serialises the config map into the chat_template_kwargs blob", func() {
		cfg := baseCfg()
		cfg.ChatTemplateKwargs = map[string]any{"preserve_thinking": true}
		opts := gRPCPredictOpts(cfg, "/tmp/models")
		Expect(opts.Metadata).To(HaveKey("chat_template_kwargs"))
		var blob map[string]any
		Expect(json.Unmarshal([]byte(opts.Metadata["chat_template_kwargs"]), &blob)).To(Succeed())
		Expect(blob).To(HaveKeyWithValue("preserve_thinking", true))
	})

	It("serialises reasoning_effort into the blob as a JSON string", func() {
		cfg := baseCfg()
		cfg.ReasoningEffort = "high"
		opts := gRPCPredictOpts(cfg, "/tmp/models")
		Expect(opts.Metadata).To(HaveKey("chat_template_kwargs"))
		var blob map[string]any
		Expect(json.Unmarshal([]byte(opts.Metadata["chat_template_kwargs"]), &blob)).To(Succeed())
		// reasoning_effort must remain a string in the blob (jinja templates that
		// key on the level read a string), unlike enable_thinking which is a bool.
		Expect(blob["reasoning_effort"]).To(BeAssignableToTypeOf(""))
		Expect(blob).To(HaveKeyWithValue("reasoning_effort", "high"))
	})

	It("lets client request metadata override the server-derived enable_thinking key", func() {
		cfg := baseCfg()
		disable := true
		cfg.ReasoningConfig = reasoning.Config{DisableReasoning: &disable} // server: enable_thinking=false
		cfg.RequestMetadata = map[string]string{"enable_thinking": "true"} // client overrides
		opts := gRPCPredictOpts(cfg, "/tmp/models")
		// standalone key (Python backends) reflects the client override
		Expect(opts.Metadata).To(HaveKeyWithValue("enable_thinking", "true"))
		// blob (llama.cpp) reflects it too, as a real bool
		var blob map[string]any
		Expect(json.Unmarshal([]byte(opts.Metadata["chat_template_kwargs"]), &blob)).To(Succeed())
		Expect(blob).To(HaveKeyWithValue("enable_thinking", true))
	})

	It("does not let a client clobber the blob via a chat_template_kwargs metadata key", func() {
		cfg := baseCfg()
		cfg.ChatTemplateKwargs = map[string]any{"preserve_thinking": true}
		cfg.RequestMetadata = map[string]string{"chat_template_kwargs": "{\"preserve_thinking\": false}"}
		opts := gRPCPredictOpts(cfg, "/tmp/models")
		var blob map[string]any
		Expect(json.Unmarshal([]byte(opts.Metadata["chat_template_kwargs"]), &blob)).To(Succeed())
		Expect(blob).To(HaveKeyWithValue("preserve_thinking", true))
	})

	It("omits the blob when there is nothing to forward", func() {
		opts := gRPCPredictOpts(baseCfg(), "/tmp/models")
		Expect(opts.Metadata).ToNot(HaveKey("chat_template_kwargs"))
	})
})

var _ = Describe("EffectiveContextSize", func() {
	Context("EffectiveContextSize", func() {
		It("clamps a negative (auto-max sentinel) context size to the default", func() {
			neg := -1
			cfg := config.ModelConfig{LLMConfig: config.LLMConfig{ContextSize: &neg}}
			Expect(EffectiveContextSize(cfg)).To(Equal(DefaultContextSize))
		})

		It("returns an explicit positive context size unchanged", func() {
			ctx := 8192
			cfg := config.ModelConfig{LLMConfig: config.LLMConfig{ContextSize: &ctx}}
			Expect(EffectiveContextSize(cfg)).To(Equal(8192))
		})

		It("falls back to the default when context size is unset", func() {
			cfg := config.ModelConfig{}
			Expect(EffectiveContextSize(cfg)).To(Equal(DefaultContextSize))
		})
	})
})

var _ = Describe("effectiveThreads", func() {
	It("lets a per-model threads value override the app-level --threads", func() {
		one := 1
		cfg := config.ModelConfig{Threads: &one}
		Expect(effectiveThreads(cfg, 10)).To(Equal(1),
			"per-model threads is a real knob, not dead config under --threads")
	})

	It("falls back to the app-level threads when the model sets none", func() {
		Expect(effectiveThreads(config.ModelConfig{}, 10)).To(Equal(10))
		zero := 0
		Expect(effectiveThreads(config.ModelConfig{Threads: &zero}, 10)).To(Equal(10),
			"an explicit threads: 0 means unset, not zero threads")
	})

	It("never resolves to a non-positive thread count", func() {
		Expect(effectiveThreads(config.ModelConfig{}, 0)).To(Equal(1))
	})
})
