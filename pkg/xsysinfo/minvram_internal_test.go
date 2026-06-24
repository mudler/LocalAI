package xsysinfo

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("minNonZeroVRAM", func() {
	const gib = uint64(1) << 30

	It("returns the smallest device on a multi-GPU host", func() {
		// Two unequal cards (e.g. RTX 5070 Ti + 5060 Ti, both 16 GiB, or a
		// mixed pair): the smallest device is the per-card allocation ceiling.
		infos := []GPUMemoryInfo{
			{TotalVRAM: 16 * gib},
			{TotalVRAM: 12 * gib},
		}
		Expect(minNonZeroVRAM(infos)).To(Equal(12 * gib))
	})

	It("ignores devices that report zero VRAM", func() {
		infos := []GPUMemoryInfo{
			{TotalVRAM: 0},
			{TotalVRAM: 24 * gib},
		}
		Expect(minNonZeroVRAM(infos)).To(Equal(24 * gib))
	})

	It("returns the single device's VRAM on a one-GPU host", func() {
		Expect(minNonZeroVRAM([]GPUMemoryInfo{{TotalVRAM: 16 * gib}})).To(Equal(16 * gib))
	})

	It("returns 0 when no device reports VRAM", func() {
		Expect(minNonZeroVRAM([]GPUMemoryInfo{{TotalVRAM: 0}})).To(BeZero())
		Expect(minNonZeroVRAM(nil)).To(BeZero())
	})
})
