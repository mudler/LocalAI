package xsysinfo

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Fixture from issue #12058 / follow-up on Strix Halo (gfx1151): BIOS UMA
// carve-out reports 512 MiB VRAM while usable HIP memory lives in the
// ~124 GiB GTT pool. rocm-smi --showmeminfo all --csv exposes both.
const strixHaloAllCSV = `device,VRAM Total Memory (B),VRAM Total Used Memory (B),VIS_VRAM Total Memory (B),VIS_VRAM Total Used Memory (B),GTT Total Memory (B),GTT Total Used Memory (B)
card0,536870912,175677440,536870912,175677440,133143986176,18661376
`

// Discrete dGPU: GTT is a host aperture and must NOT be folded into
// reported VRAM (preserves dedicated-GPU accounting).
const discreteRX7900AllCSV = `device,VRAM Total Memory (B),VRAM Total Used Memory (B),VIS_VRAM Total Memory (B),VIS_VRAM Total Used Memory (B),GTT Total Memory (B),GTT Total Used Memory (B)
card0,25769803776,1073741824,268435456,0,34359738368,1048576
`

// Legacy vram-only CSV (pre-all / older rocm-smi) with GPU[N] labels.
const legacyVRAMOnlyCSV = `device,VRAM Total Memory (B),VRAM Total Used Memory (B)
GPU[0],17179869184,2147483648
`

var _ = Describe("parseRocmSMIMemInfoCSV", func() {
	It("folds GTT into totals for AMD shared-memory APUs (#12058)", func() {
		got := parseRocmSMIMemInfoCSV(strixHaloAllCSV)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Vendor).To(Equal(VendorAMD))
		// 512 MiB VRAM + 124 GiB GTT
		Expect(got[0].TotalVRAM).To(Equal(uint64(536870912 + 133143986176)))
		Expect(got[0].UsedVRAM).To(Equal(uint64(175677440 + 18661376)))
		Expect(got[0].FreeVRAM).To(Equal(got[0].TotalVRAM - got[0].UsedVRAM))
		// Must not stop at the 512 MiB UMA carve-out alone.
		Expect(got[0].TotalVRAM).To(BeNumerically(">", uint64(536870912)))
	})

	It("keeps dedicated dGPU totals on VRAM only", func() {
		got := parseRocmSMIMemInfoCSV(discreteRX7900AllCSV)
		Expect(got).To(HaveLen(1))
		Expect(got[0].TotalVRAM).To(Equal(uint64(25769803776)))
		Expect(got[0].UsedVRAM).To(Equal(uint64(1073741824)))
		Expect(got[0].FreeVRAM).To(Equal(uint64(25769803776 - 1073741824)))
	})

	It("still parses legacy vram-only CSV", func() {
		got := parseRocmSMIMemInfoCSV(legacyVRAMOnlyCSV)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Index).To(Equal(0))
		Expect(got[0].TotalVRAM).To(Equal(uint64(17179869184)))
		Expect(got[0].UsedVRAM).To(Equal(uint64(2147483648)))
	})
})

var _ = Describe("amdSharedMemoryAPU", func() {
	DescribeTable("detects UMA carve-out + large GTT",
		func(vram, gtt uint64, want bool) {
			Expect(amdSharedMemoryAPU(vram, gtt)).To(Equal(want))
		},
		Entry("Strix Halo 512MiB + 124GiB", uint64(536870912), uint64(133143986176), true),
		Entry("discrete 24GiB + 32GiB GTT aperture", uint64(25769803776), uint64(34359738368), false),
		Entry("missing GTT", uint64(16<<30), uint64(0), false),
		Entry("zero VRAM", uint64(0), uint64(64<<30), false),
	)
})
