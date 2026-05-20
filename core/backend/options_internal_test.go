package backend

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/config"

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
