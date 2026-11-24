package config

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config/harnessctx"
)

// These specs cover the step-down for hosts with no VRAM ceiling: a model that
// runs from system memory must not take more than about 60% of it, but the
// window does not drop below the chat minimum while the model fits in memory.

var _ = Describe("Auto context on a host with no VRAM ceiling", func() {
	const gib = uint64(1) << 30

	var origVRAM, origRAM func() uint64

	BeforeEach(func() {
		GinkgoT().Setenv("LOCALAI_DISABLE_GUESSING", "false")
		origVRAM, origRAM = perDeviceVRAM, systemTotalRAM
		perDeviceVRAM = func() uint64 { return 0 }
	})
	AfterEach(func() { perDeviceVRAM, systemTotalRAM = origVRAM, origRAM })

	host := func(ramGiB uint64) { systemTotalRAM = func() uint64 { return ramGiB * gib } }

	// footprint is the weights plus a KV cache of kv bytes per token.
	footprint := func(weights uint64, kv uint64) func(int) uint64 {
		return func(ctx int) uint64 { return weights + uint64(ctx)*kv }
	}

	// An 8B model at 4-bit weights (about 5.5 GiB) with grouped-query attention
	// has a KV cache of 128 KiB per token: 4 GiB at 32768. A 70B-class model
	// (about 40 GiB) has 320 KiB per token: 10 GiB at 32768.
	var (
		model8B  = footprint(5*gib+gib/2, 128<<10)
		model70B = footprint(40*gib, 320<<10)
	)

	DescribeTable("the window picked from total memory",
		func(ramGiB uint64, fp func(int) uint64, want int) {
			host(ramGiB)
			Expect(autoContextSize(nil, fp, 131072, harnessctx.RecommendedContext)).To(Equal(want))
		},
		// 16 GiB: the 60% budget is 9.6 GiB and 32768 needs 9.5.
		Entry("8B on 16 GB keeps the recommended window", uint64(16), model8B, 32768),
		Entry("8B on 64 GB keeps the recommended window", uint64(64), model8B, 32768),
		// 8 GiB: budget 4.8 GiB, so nothing fits the budget, but the model with
		// 16384 (7.5 GiB) still fits in memory, so the chat minimum holds.
		Entry("8B on 8 GB keeps the chat minimum", uint64(8), model8B, harnessctx.MinContext),
		// 64 GiB: budget 38.4 GiB, the model alone takes 40. 16384 needs 45 and
		// still fits in total memory.
		Entry("70B on 64 GB keeps the chat minimum", uint64(64), model70B, harnessctx.MinContext),
		Entry("70B on 128 GB keeps the recommended window", uint64(128), model70B, 32768),
		// The model does not even fit in memory with 16384: use the lower steps,
		// down to the floor.
		Entry("70B on 8 GB falls to the floor", uint64(8), model70B, DefaultContextSize),
		Entry("70B on 16 GB falls to the floor", uint64(16), model70B, DefaultContextSize),
		// Between the budget and the total: a middle step when it fits the budget.
		// 12 GiB: budget 7.2 GiB; 24576 needs 8.5, 16384 needs 7.5, both over
		// the budget, so 16384 stays by the minimum rule.
		Entry("8B on 12 GB keeps the chat minimum", uint64(12), model8B, harnessctx.MinContext),
		// Small model on a small host: 1 GiB of weights, 64 KiB per token.
		// 6 GiB: budget 3.6 GiB; 32768 needs 3, fits.
		Entry("a small model on 6 GB keeps the recommended window", uint64(6), footprint(gib, 64<<10), 32768),
		// 4 GiB model on 6 GiB with 64 KiB per token: 24576 needs 5.5, 16384
		// needs 5.0, over the 3.6 budget; the minimum rule keeps 16384.
		Entry("a mid model on 6 GB keeps the chat minimum", uint64(6), footprint(4*gib, 64<<10), harnessctx.MinContext),
		// Below the minimum the lower steps use the budget again: 5 GiB of
		// weights, 256 KiB per token on 8 GiB. 16384 needs 9 (over 8 total); 8192
		// needs 7 (over the 4.8 budget); 6144 needs 6.5; 4096 needs 6: floor.
		Entry("a model that cannot hold the minimum goes to the floor", uint64(8), footprint(5*gib, 256<<10), DefaultContextSize),
		// With roomy lower steps: 2 GiB weights, 512 KiB per token on 8 GiB.
		// 16384 needs 10 (over 8 total); 8192 needs 6 (over 4.8); 6144 needs 5
		// (over); 4096 needs 4 (fits).
		Entry("a heavy KV cache steps down to the first lower step in budget", uint64(8), footprint(2*gib, 512<<10), 4096),
		// 9 GiB: budget 5.4 GiB; 16384 needs 10 (over 9 total); 8192 needs 6
		// (over the budget); 6144 needs 5 (fits).
		Entry("a heavy KV cache on 9 GB steps to 6144", uint64(9), footprint(2*gib, 512<<10), 6144),
		Entry("a heavy KV cache on 12 GB still keeps the chat minimum", uint64(12), footprint(2*gib, 512<<10), harnessctx.MinContext),
	)

	It("keeps the base cap when total memory is unknown", func() {
		systemTotalRAM = func() uint64 { return 0 }
		Expect(autoContextSize(nil, model70B, 131072, harnessctx.RecommendedContext)).To(Equal(32768))
	})

	It("keeps the base cap when the estimate is unknown", func() {
		host(8)
		Expect(autoContextSize(nil, func(int) uint64 { return 0 }, 131072, harnessctx.RecommendedContext)).To(Equal(32768))
		Expect(autoContextSize(nil, nil, 131072, harnessctx.RecommendedContext)).To(Equal(32768))
	})

	It("does not raise a model above its trained window", func() {
		host(128)
		Expect(autoContextSize(nil, model8B, 4096, harnessctx.RecommendedContext)).To(Equal(4096))
	})

	It("ignores system memory when a VRAM ceiling is known", func() {
		host(8)
		perDeviceVRAM = func() uint64 { return 80 * gib }
		Expect(autoContextSize(footprint(gib, 1<<10), model70B, 131072, harnessctx.RecommendedContext)).To(Equal(32768))
	})

	Describe("through the defaults path", func() {
		build := func(arch string, trained int, ram func(int) uint64) ggufHeader {
			h := ggufHeaderFor(arch, trained, func(int) uint64 { return 0 })
			h.ramFootprints = map[int]uint64{}
			for _, c := range append([]int{min(trained, harnessctx.RecommendedContext), min(trained, DefaultAutoContextSize)}, contextFitCandidates...) {
				h.ramFootprints[c] = ram(c)
			}
			return h
		}

		It("steps a chat model down on a small host and records it as automatic", func() {
			host(8)
			cfg := &ModelConfig{}
			guessGGUFFromFile(cfg, build("llama", 131072, model70B), 0)
			Expect(cfg.ContextSize).To(HaveValue(Equal(DefaultContextSize)))
			Expect(cfg.ContextAutoSized).To(BeTrue())
		})

		It("never overrides a value somebody set", func() {
			host(8)
			explicit := 32768
			cfg := &ModelConfig{LLMConfig: LLMConfig{ContextSize: &explicit}}
			guessGGUFFromFile(cfg, build("llama", 131072, model70B), 0)
			Expect(cfg.ContextSize).To(HaveValue(Equal(32768)))
			Expect(cfg.ContextAutoSized).To(BeFalse())
		})

		It("never overrides the global default either", func() {
			host(8)
			cfg := &ModelConfig{}
			guessGGUFFromFile(cfg, build("llama", 131072, model70B), 12288)
			Expect(cfg.ContextSize).To(BeNil())
		})

		It("keeps the conservative window of a model that does not serve chats", func() {
			host(64)
			cfg := &ModelConfig{}
			guessGGUFFromFile(cfg, build("nomic-bert", 131072, model8B), 0)
			Expect(cfg.ContextSize).To(HaveValue(Equal(DefaultAutoContextSize)))
		})

		It("still sizes a chat model to the recommended window on a roomy host", func() {
			host(64)
			cfg := &ModelConfig{}
			guessGGUFFromFile(cfg, build("llama", 131072, model8B), 0)
			Expect(cfg.ContextSize).To(HaveValue(Equal(harnessctx.RecommendedContext)))
		})
	})
})
