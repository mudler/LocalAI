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
})
