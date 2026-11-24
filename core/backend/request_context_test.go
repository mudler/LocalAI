package backend

import (
	"github.com/mudler/LocalAI/core/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("EffectiveRequestContextSize", func() {
	withCtx := func(ctx int, opts ...string) config.ModelConfig {
		c := config.ModelConfig{Options: opts}
		c.ContextSize = &ctx
		return c
	}

	It("is the full context with a single slot", func() {
		Expect(EffectiveRequestContextSize(withCtx(32768))).To(Equal(32768))
	})

	It("is the full context when parallel slots share a unified KV cache", func() {
		// kv_unified is the grpc-server default, so every slot may use all of n_ctx.
		Expect(EffectiveRequestContextSize(withCtx(32768, "parallel:4"))).To(Equal(32768))
		Expect(EffectiveRequestContextSize(withCtx(32768, "parallel:4", "kv_unified:true"))).To(Equal(32768))
	})

	It("splits the context across slots when the KV cache is not unified", func() {
		Expect(EffectiveRequestContextSize(withCtx(32768, "parallel:4", "kv_unified:false"))).To(Equal(8192))
		Expect(EffectiveRequestContextSize(withCtx(32768, "kv_unified:false", "n_parallel:4"))).To(Equal(8192))
	})

	It("pads the per-slot context up to a multiple of 256, as llama.cpp does", func() {
		// 8192/3 = 2730, which llama.cpp pads to 2816.
		Expect(EffectiveRequestContextSize(withCtx(8192, "parallel:3", "unified_kv:false"))).To(Equal(2816))
	})

	It("ignores a parallel value it cannot parse", func() {
		Expect(EffectiveRequestContextSize(withCtx(8192, "parallel:many", "kv_unified:false"))).To(Equal(8192))
	})
})
