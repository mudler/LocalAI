package config

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Single-instance path: SetDefaults applies hardware defaults from the local
// GPU. The detection seam (localGPU) is injected so the path is deterministic
// without a real GPU.
var _ = Describe("SetDefaults hardware defaults (single-instance)", func() {
	const gib = uint64(1) << 30

	var orig func() GPU
	BeforeEach(func() { orig = localGPU })
	AfterEach(func() { localGPU = orig })

	It("sets the physical batch on a local Blackwell GPU with headroom", func() {
		localGPU = func() GPU { return GPU{ComputeCapability: "12.1", VRAM: 119 * gib} }
		cfg := &ModelConfig{}
		cfg.SetDefaults()
		Expect(cfg.Batch).To(Equal(BlackwellPhysicalBatch))
	})

	It("leaves batch unset when a large context would overflow the device", func() {
		// Regression guard for issue #10485: 16 GiB consumer Blackwell + ~200k ctx.
		localGPU = func() GPU { return GPU{ComputeCapability: "12.0", VRAM: 16 * gib} }
		ctx := 204800
		cfg := &ModelConfig{LLMConfig: LLMConfig{ContextSize: &ctx}}
		cfg.SetDefaults()
		Expect(cfg.Batch).To(Equal(0))
	})

	It("leaves batch unset on a non-Blackwell local GPU", func() {
		localGPU = func() GPU { return GPU{ComputeCapability: "8.9", VRAM: 119 * gib} }
		cfg := &ModelConfig{}
		cfg.SetDefaults()
		Expect(cfg.Batch).To(Equal(0))
	})

	It("never overrides an explicit batch", func() {
		localGPU = func() GPU { return GPU{ComputeCapability: "12.1", VRAM: 119 * gib} }
		cfg := &ModelConfig{}
		cfg.Batch = 1024
		cfg.SetDefaults()
		Expect(cfg.Batch).To(Equal(1024))
	})
})

// SinglePassBatchForContext is the VRAM-aware cap for the single-pass
// (embedding/score/rerank) batch — the compute buffer scales ~ n_ubatch * n_ctx
// and must fit a single device, so a large context can't take the full context
// as its batch (issue #10485).
var _ = Describe("SinglePassBatchForContext", func() {
	const gib = uint64(1) << 30

	It("returns the default when the context is at or below the default batch", func() {
		Expect(SinglePassBatchForContext(GPU{VRAM: 119 * gib}, DefaultPhysicalBatch)).To(Equal(DefaultPhysicalBatch))
		Expect(SinglePassBatchForContext(GPU{VRAM: 119 * gib}, 256)).To(Equal(DefaultPhysicalBatch))
	})

	It("returns the full context when the compute buffer fits ample VRAM", func() {
		// 4096 ctx on 119 GiB: the compute buffer is tiny, so the batch covers
		// the whole context (single-pass pooling in one physical batch).
		Expect(SinglePassBatchForContext(GPU{VRAM: 119 * gib}, 4096)).To(Equal(4096))
	})

	It("caps below the context when a large context would overflow the VRAM headroom", func() {
		batch := SinglePassBatchForContext(GPU{VRAM: 20 * gib}, 40960)
		Expect(batch).To(BeNumerically(">=", DefaultPhysicalBatch))
		Expect(batch).To(BeNumerically("<", 40960))
		// The compute buffer for the capped batch must fit VRAM/headroom.
		Expect(uint64(batch) * 40960 * computeBufferBytesPerCell).To(BeNumerically("<=", (20*gib)/blackwellBatchHeadroomDivisor))
	})

	It("never caps below the default batch even when VRAM is very tight", func() {
		Expect(SinglePassBatchForContext(GPU{VRAM: 1 * gib}, 40960)).To(Equal(DefaultPhysicalBatch))
	})

	It("returns the full context (unclamped) when per-device VRAM is unknown", func() {
		// Unknown VRAM (CPU / detection gap) preserves the original single-pass
		// behavior — the cap is a downward safety that only engages when VRAM is
		// known. Clamping here would over-trim score/embed/rerank inputs.
		Expect(SinglePassBatchForContext(GPU{VRAM: 0}, 40960)).To(Equal(40960))
	})
})
