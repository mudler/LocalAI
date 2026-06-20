package config_test

import (
	. "github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Hardware-driven config defaults", func() {
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

	Describe("ApplyHardwareDefaults", func() {
		It("raises an unset batch to 2048 on Blackwell", func() {
			cfg := &ModelConfig{}
			ApplyHardwareDefaults(cfg, GPU{ComputeCapability: "12.1"})
			Expect(cfg.Batch).To(Equal(BlackwellPhysicalBatch))
		})
		It("leaves batch unset on non-Blackwell", func() {
			cfg := &ModelConfig{}
			ApplyHardwareDefaults(cfg, GPU{ComputeCapability: "9.0"})
			Expect(cfg.Batch).To(Equal(0))
		})
		It("never overrides an explicit batch", func() {
			cfg := &ModelConfig{}
			cfg.Batch = 1024
			ApplyHardwareDefaults(cfg, GPU{ComputeCapability: "12.1"})
			Expect(cfg.Batch).To(Equal(1024))
		})
		It("no-ops on nil", func() {
			Expect(func() { ApplyHardwareDefaults(nil, GPU{ComputeCapability: "12.1"}) }).ToNot(Panic())
		})
	})

	const gib = uint64(1) << 30

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

	Describe("ApplyHardwareDefaults parallel slots", func() {
		It("adds a VRAM-scaled parallel option on a capable GPU", func() {
			cfg := &ModelConfig{}
			ApplyHardwareDefaults(cfg, GPU{ComputeCapability: "12.1", VRAM: 119 * gib})
			Expect(cfg.Options).To(ContainElement("parallel:8"))
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
