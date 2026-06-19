package backend

import (
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("hardware-specific defaults", func() {
	var origDetect func() bool

	BeforeEach(func() {
		origDetect = detectBlackwellGPU
	})
	AfterEach(func() {
		detectBlackwellGPU = origDetect
	})

	Describe("hardwareDefaultBatchSize", func() {
		It("returns the fallback when not Blackwell", func() {
			detectBlackwellGPU = func() bool { return false }
			Expect(hardwareDefaultBatchSize(512)).To(Equal(512))
		})

		It("returns BlackwellBatchSize on Blackwell", func() {
			detectBlackwellGPU = func() bool { return true }
			Expect(hardwareDefaultBatchSize(512)).To(Equal(BlackwellBatchSize))
		})
	})

	Describe("EffectiveBatchSize on Blackwell", func() {
		threads := 1
		ctx := 4096

		It("defaults an unset batch to 2048 on Blackwell", func() {
			detectBlackwellGPU = func() bool { return true }
			cfg := config.ModelConfig{Threads: &threads, LLMConfig: config.LLMConfig{ContextSize: &ctx}}
			opts := grpcModelOpts(cfg, "/tmp/models")
			Expect(opts.NBatch).To(BeEquivalentTo(BlackwellBatchSize))
		})

		It("keeps an explicit batch over the Blackwell default", func() {
			detectBlackwellGPU = func() bool { return true }
			cfg := config.ModelConfig{Threads: &threads, LLMConfig: config.LLMConfig{ContextSize: &ctx}}
			cfg.Batch = 256
			opts := grpcModelOpts(cfg, "/tmp/models")
			Expect(opts.NBatch).To(BeEquivalentTo(256))
		})
	})
})
