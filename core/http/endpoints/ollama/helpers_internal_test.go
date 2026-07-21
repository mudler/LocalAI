package ollama

import (
	"math"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These specs pin the num_ctx handling added for issue #11022: a client-supplied
// options.num_ctx flows unmodified into cfg.ContextSize and is later cast to
// int32 before it reaches the backend, so an out-of-range value would silently
// wrap into a negative context size. applyOllamaOptions must cap it instead.
var _ = Describe("applyOllamaOptions num_ctx clamping (issue #11022)", func() {
	It("caps num_ctx at math.MaxInt32 so the later int32 cast cannot wrap negative", func() {
		cfg := &config.ModelConfig{}
		applyOllamaOptions(&schema.OllamaOptions{NumCtx: math.MaxInt32 + 1}, cfg)

		Expect(cfg.ContextSize).ToNot(BeNil())
		Expect(*cfg.ContextSize).To(Equal(math.MaxInt32))
		Expect(int32(*cfg.ContextSize)).To(BeNumerically(">", 0),
			"capped context size must stay positive after the int32 cast")
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
