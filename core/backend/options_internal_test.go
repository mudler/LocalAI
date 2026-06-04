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
