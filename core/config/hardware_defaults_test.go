package config_test

import (
	. "github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Hardware-driven config defaults", func() {
	const gib = uint64(1) << 30

	DescribeTable("GPU.IsNVIDIABlackwell (sm_12x consumer family)",
		func(cc string, want bool) {
			Expect(GPU{ComputeCapability: cc}.IsNVIDIABlackwell()).To(Equal(want))
		},
		Entry("GB10 12.1", "12.1", true),
		Entry("RTX 50 12.0", "12.0", true),
		Entry("future 13.0", "13.0", true),
		Entry("Hopper 9.0", "9.0", false),
		Entry("Ada 8.9", "8.9", false),
		Entry("datacenter Blackwell sm_100 10.0", "10.0", false),
		Entry("unknown", "", false),
	)

	Describe("PhysicalBatch / IsManagedPhysicalBatch", func() {
		It("returns the Blackwell batch on Blackwell", func() {
			Expect(PhysicalBatch(GPU{ComputeCapability: "12.1"})).To(Equal(BlackwellPhysicalBatch))
		})
		It("returns the default batch otherwise", func() {
			Expect(PhysicalBatch(GPU{ComputeCapability: "9.0"})).To(Equal(DefaultPhysicalBatch))
			Expect(PhysicalBatch(GPU{})).To(Equal(DefaultPhysicalBatch))
		})
		It("recognizes managed defaults but not explicit values", func() {
			Expect(IsManagedPhysicalBatch(DefaultPhysicalBatch)).To(BeTrue())
			Expect(IsManagedPhysicalBatch(BlackwellPhysicalBatch)).To(BeTrue())
			Expect(IsManagedPhysicalBatch(1024)).To(BeFalse())
		})
	})

	Describe("PhysicalBatchForContext (per-device VRAM headroom)", func() {
		It("raises the batch when the compute buffer fits the device", func() {
			// 16 GiB Blackwell with a small context: the extra scratch is tiny.
			Expect(PhysicalBatchForContext(GPU{ComputeCapability: "12.0", VRAM: 16 * gib}, 8192)).
				To(Equal(BlackwellPhysicalBatch))
		})
		It("keeps the default batch when a large context would overflow one device", func() {
			// The issue #10485 case: 16 GiB consumer Blackwell, ~200k context.
			Expect(PhysicalBatchForContext(GPU{ComputeCapability: "12.0", VRAM: 16 * gib}, 204800)).
				To(Equal(DefaultPhysicalBatch))
		})
		It("still raises the batch on a large unified-memory device (GB10)", func() {
			// GB10 reports system RAM (~119 GiB) as its single device's VRAM.
			Expect(PhysicalBatchForContext(GPU{ComputeCapability: "12.1", VRAM: 119 * gib}, 204800)).
				To(Equal(BlackwellPhysicalBatch))
		})
		It("stays conservative when VRAM is unknown", func() {
			Expect(PhysicalBatchForContext(GPU{ComputeCapability: "12.1"}, 8192)).
				To(Equal(DefaultPhysicalBatch))
		})
		It("never raises the batch on non-Blackwell", func() {
			Expect(PhysicalBatchForContext(GPU{ComputeCapability: "9.0", VRAM: 80 * gib}, 8192)).
				To(Equal(DefaultPhysicalBatch))
		})
	})

	Describe("ApplyHardwareDefaults", func() {
		It("raises an unset batch to 2048 on Blackwell with headroom", func() {
			cfg := &ModelConfig{}
			ApplyHardwareDefaults(cfg, GPU{ComputeCapability: "12.1", VRAM: 119 * gib})
			Expect(cfg.Batch).To(Equal(BlackwellPhysicalBatch))
		})
		It("leaves batch unset when a large context would overflow one device", func() {
			// Regression guard for issue #10485: 16 GiB card + ~200k context.
			ctx := 204800
			cfg := &ModelConfig{LLMConfig: LLMConfig{ContextSize: &ctx}}
			ApplyHardwareDefaults(cfg, GPU{ComputeCapability: "12.0", VRAM: 16 * gib})
			Expect(cfg.Batch).To(Equal(0))
		})
		It("leaves batch unset on non-Blackwell", func() {
			cfg := &ModelConfig{}
			ApplyHardwareDefaults(cfg, GPU{ComputeCapability: "9.0", VRAM: 119 * gib})
			Expect(cfg.Batch).To(Equal(0))
		})
		It("never overrides an explicit batch", func() {
			cfg := &ModelConfig{}
			cfg.Batch = 1024
			ApplyHardwareDefaults(cfg, GPU{ComputeCapability: "12.1", VRAM: 119 * gib})
			Expect(cfg.Batch).To(Equal(1024))
		})
		It("no-ops on nil", func() {
			Expect(func() { ApplyHardwareDefaults(nil, GPU{ComputeCapability: "12.1"}) }).ToNot(Panic())
		})

		It("applies nothing when hardware defaults are disabled via env", func() {
			GinkgoT().Setenv("LOCALAI_DISABLE_HARDWARE_DEFAULTS", "true")
			Expect(HardwareDefaultsDisabled()).To(BeTrue())
			cfg := &ModelConfig{}
			ApplyHardwareDefaults(cfg, GPU{ComputeCapability: "12.1", VRAM: 119 * gib})
			Expect(cfg.Batch).To(Equal(0))
			Expect(cfg.Options).To(BeEmpty())
		})
	})

	DescribeTable("DefaultParallelSlots (by VRAM)",
		func(vramGiB uint64, want int) {
			Expect(DefaultParallelSlots(GPU{VRAM: vramGiB * gib})).To(Equal(want))
		},
		Entry("GB10 119 GiB", uint64(119), 8),
		Entry("48 GiB", uint64(48), 8),
		Entry("24 GiB", uint64(24), 4),
		Entry("8 GiB", uint64(8), 4),
		Entry("6 GiB", uint64(6), 2),
		Entry("2 GiB", uint64(2), 1),
		Entry("unknown 0", uint64(0), 1),
	)

	Describe("ParallelSlotsForContext (per-device VRAM headroom)", func() {
		It("keeps the VRAM-scaled slot count when the context fits the device", func() {
			// 16 GiB card, small context: plenty of room for concurrency.
			Expect(ParallelSlotsForContext(GPU{VRAM: 16 * gib}, 8192)).To(Equal(4))
		})
		It("drops to a single slot when a large context already fills the device", func() {
			// Regression guard for issue #10485: 16 GiB consumer Blackwell, ~200k
			// context. Even with unified KV, the per-slot compute/checkpoint
			// scratch from 4 slots is the straw that overflows the tighter device.
			Expect(ParallelSlotsForContext(GPU{VRAM: 16 * gib}, 204800)).To(Equal(1))
		})
		It("keeps concurrency on a large unified-memory device (GB10)", func() {
			// GB10 reports system RAM (~119 GiB): a 200k context leaves headroom.
			Expect(ParallelSlotsForContext(GPU{VRAM: 119 * gib}, 204800)).To(Equal(8))
		})
		It("keeps concurrency on a big datacenter card with a large context", func() {
			// 80 GiB A100: 200k context is a small fraction, concurrency stays.
			Expect(ParallelSlotsForContext(GPU{VRAM: 80 * gib}, 204800)).To(Equal(8))
		})
		It("stays a single slot on small/unknown VRAM regardless of context", func() {
			Expect(ParallelSlotsForContext(GPU{VRAM: 2 * gib}, 8192)).To(Equal(1))
			Expect(ParallelSlotsForContext(GPU{}, 8192)).To(Equal(1))
		})
	})

	Describe("ApplyHardwareDefaults parallel slots", func() {
		It("adds a VRAM-scaled parallel option on a capable GPU", func() {
			cfg := &ModelConfig{}
			ApplyHardwareDefaults(cfg, GPU{ComputeCapability: "12.1", VRAM: 119 * gib})
			Expect(cfg.Options).To(ContainElement("parallel:8"))
		})
		It("adds no parallel option when a large context already fills one device", func() {
			// Regression guard for issue #10485: 16 GiB card + ~200k context. The
			// model barely fits; defaulting concurrency tips the tighter GPU into
			// CUDA OOM during the final (MTP draft) KV allocation.
			ctx := 204800
			cfg := &ModelConfig{LLMConfig: LLMConfig{ContextSize: &ctx}}
			ApplyHardwareDefaults(cfg, GPU{ComputeCapability: "12.0", VRAM: 16 * gib})
			Expect(cfg.Options).ToNot(ContainElement(ContainSubstring("parallel")))
		})
		It("scales the slot count down with VRAM", func() {
			cfg := &ModelConfig{}
			ApplyHardwareDefaults(cfg, GPU{VRAM: 24 * gib})
			Expect(cfg.Options).To(ContainElement("parallel:4"))
		})
		It("adds no parallel option on small/unknown VRAM", func() {
			cfg := &ModelConfig{}
			ApplyHardwareDefaults(cfg, GPU{VRAM: 2 * gib})
			Expect(cfg.Options).ToNot(ContainElement(ContainSubstring("parallel")))
		})
		It("never overrides an explicit parallel option", func() {
			cfg := &ModelConfig{Options: []string{"parallel:2"}}
			ApplyHardwareDefaults(cfg, GPU{VRAM: 119 * gib})
			Expect(cfg.Options).To(Equal([]string{"parallel:2"}))
		})
	})
})
