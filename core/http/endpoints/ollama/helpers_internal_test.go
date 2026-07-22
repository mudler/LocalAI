package ollama

import (
	"math"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These specs pin the num_ctx handling for issue #11022. /api/chat and
// /api/generate share applyOllamaOptions, so exercising the helper covers both
// endpoints. A client-supplied options.num_ctx must not be able to *raise* the
// model/hardware-derived context ceiling already in cfg.ContextSize (that value
// drives KV-cache allocation, so an oversized request is a DoS), and it must
// stay int32-safe because ContextSize is cast to int32 before it reaches the
// backend, where an out-of-range value would silently wrap negative.
var _ = Describe("applyOllamaOptions num_ctx clamping (issue #11022)", func() {
	It("caps num_ctx at math.MaxInt32 so the later int32 cast cannot wrap negative", func() {
		cfg := &config.ModelConfig{}
		applyOllamaOptions(&schema.OllamaOptions{NumCtx: math.MaxInt32 + 1}, cfg)

		Expect(cfg.ContextSize).ToNot(BeNil())
		Expect(*cfg.ContextSize).To(Equal(math.MaxInt32))
		Expect(int32(*cfg.ContextSize)).To(BeNumerically(">", 0),
			"capped context size must stay positive after the int32 cast")
	})

	It("does not let an oversized num_ctx raise an existing context ceiling", func() {
		for _, ceiling := range []int{4096, 8192} {
			existing := ceiling
			cfg := &config.ModelConfig{ContextSize: &existing}
			applyOllamaOptions(&schema.OllamaOptions{NumCtx: 2000000000}, cfg)

			Expect(cfg.ContextSize).ToNot(BeNil())
			Expect(*cfg.ContextSize).To(Equal(ceiling),
				"an in-range but oversized num_ctx must be clamped to the server ceiling, not replace it")
		}
	})

	It("still honors a num_ctx smaller than the existing ceiling", func() {
		existing := 8192
		cfg := &config.ModelConfig{ContextSize: &existing}
		applyOllamaOptions(&schema.OllamaOptions{NumCtx: 2048}, cfg)

		Expect(cfg.ContextSize).ToNot(BeNil())
		Expect(*cfg.ContextSize).To(Equal(2048))
	})

	It("passes an in-range num_ctx through unchanged", func() {
		cfg := &config.ModelConfig{}
		applyOllamaOptions(&schema.OllamaOptions{NumCtx: 4096}, cfg)

		Expect(cfg.ContextSize).ToNot(BeNil())
		Expect(*cfg.ContextSize).To(Equal(4096))
	})

	It("leaves ContextSize untouched when num_ctx is not set", func() {
		cfg := &config.ModelConfig{}
		applyOllamaOptions(&schema.OllamaOptions{}, cfg)

		Expect(cfg.ContextSize).To(BeNil())
	})
})