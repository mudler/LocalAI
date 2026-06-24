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
