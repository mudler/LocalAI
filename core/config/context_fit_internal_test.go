package config

import (
	gguf "github.com/gpustack/gguf-parser-go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These specs exercise the auto-derived default context. The detection seams
// (perDeviceVRAM, estimateContextVRAM) are package vars so a deterministic VRAM
// ceiling and footprint can be injected without a real GPU or model file — the
// same pattern hardware_defaults_internal_test.go uses for localGPU.
var _ = Describe("Auto-derived default context (VRAM-aware cap)", func() {
	const gib = uint64(1) << 30

	var (
		origVRAM     func() uint64
		origEstimate func(f *gguf.GGUFFile, ctx int) uint64
	)

	BeforeEach(func() {
		origVRAM = perDeviceVRAM
		origEstimate = estimateContextVRAM
	})
	AfterEach(func() {
		perDeviceVRAM = origVRAM
		estimateContextVRAM = origEstimate
	})

	Context("autoContextSize", func() {
		It("caps a long-context model at DefaultAutoContextSize when VRAM is ample", func() {
			// 1M-context model on an 80 GiB card: we do NOT chase the trained max,
			// we keep the conservative 8k cap (users opt into more via context_size).
			perDeviceVRAM = func() uint64 { return 80 * gib }
			estimateContextVRAM = func(_ *gguf.GGUFFile, _ int) uint64 { return gib } // trivially fits
			Expect(autoContextSize(nil, 1048576)).To(Equal(DefaultAutoContextSize))
		})

		It("keeps a small model's trained window instead of inflating it", func() {
			// trained 4096 < 8192: min() keeps 4096, it is not raised to the cap.
			perDeviceVRAM = func() uint64 { return 80 * gib }
			estimateContextVRAM = func(_ *gguf.GGUFFile, _ int) uint64 { return gib }
			Expect(autoContextSize(nil, 4096)).To(Equal(4096))
		})

		It("steps below the cap when even 8k would not fit a tiny card", func() {
			// A large model on a 2 GiB card where the 8k footprint overflows but a
			// smaller context fits: choose the largest that fits, never below the
			// floor. Footprint grows with context so the walk finds a fit.
			perDeviceVRAM = func() uint64 { return 2 * gib }
			estimateContextVRAM = func(_ *gguf.GGUFFile, ctx int) uint64 {
				return gib + uint64(ctx)*100000
			}
			chosen := autoContextSize(nil, 1048576)
			Expect(chosen).To(BeNumerically("<", DefaultAutoContextSize))
			Expect(chosen).To(BeNumerically(">=", DefaultContextSize))
			// The chosen context's footprint must actually fit the card with headroom.
			Expect(contextFitsVRAM(estimateContextVRAM(nil, chosen), 2*gib)).To(BeTrue())
		})

		It("falls back to the floor when nothing fits", func() {
			// Even DefaultContextSize does not fit: return the floor and let the
			// backend clamp n_gpu_layers to what it can (partial offload) rather
			// than defaulting to a window guaranteed to abort.
			perDeviceVRAM = func() uint64 { return 1 * gib }
			estimateContextVRAM = func(_ *gguf.GGUFFile, _ int) uint64 { return 100 * gib }
			Expect(autoContextSize(nil, 1048576)).To(Equal(DefaultContextSize))
		})

		It("does not clamp when per-device VRAM is unknown", func() {
			// CPU-only / detection gap: no GPU budget to reason about, so we must
			// not regress — keep the conservative base cap regardless of estimate.
			perDeviceVRAM = func() uint64 { return 0 }
			estimateContextVRAM = func(_ *gguf.GGUFFile, _ int) uint64 { return 999 * gib }
			Expect(autoContextSize(nil, 1048576)).To(Equal(DefaultAutoContextSize))
		})
	})

	Context("guessGGUFFromFile", func() {
		It("never overrides an explicitly configured context_size", func() {
			// A fabricated GGUF is enough: the context branch is skipped entirely
			// when the user pinned context_size, so the estimate is never consulted.
			explicit := 262144
			cfg := &ModelConfig{LLMConfig: LLMConfig{ContextSize: &explicit}}
			f := &gguf.GGUFFile{
				Header: gguf.GGUFHeader{
					MetadataKV: gguf.GGUFMetadataKVs{
						{
							Key:       "general.architecture",
							ValueType: gguf.GGUFMetadataValueTypeString,
							Value:     "llama",
						},
					},
				},
			}
			guessGGUFFromFile(cfg, f, 0)
			Expect(cfg.ContextSize).ToNot(BeNil())
			Expect(*cfg.ContextSize).To(Equal(262144))
		})
	})
})
