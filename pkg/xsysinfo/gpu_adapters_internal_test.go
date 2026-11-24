package xsysinfo

import (
	"github.com/mudler/LocalAI/pkg/vrambudget"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const gib = 1024 * 1024 * 1024

var _ = Describe("OS adapter GPU detection", func() {
	Describe("parseAdapterCounterInstance", func() {
		It("reads the LUID of a counter instance", func() {
			luid, ok := parseAdapterCounterInstance("luid_0x00000000_0x0000D1B5_phys_0")
			Expect(ok).To(BeTrue())
			Expect(luid).To(Equal(luidKey(0xD1B5, 0)))
		})
		It("keeps the high part of the LUID", func() {
			luid, ok := parseAdapterCounterInstance("luid_0x00000001_0x00000002_phys_1")
			Expect(ok).To(BeTrue())
			Expect(luid).To(Equal(uint64(1)<<32 | 2))
		})
		It("rejects other names", func() {
			for _, name := range []string{"", "_Total", "luid_zz_0x1_phys_0", "luid_0x1"} {
				_, ok := parseAdapterCounterInstance(name)
				Expect(ok).To(BeFalse(), name)
			}
		})
	})

	Describe("adapterGPUInfos", func() {
		It("reports a discrete GPU with its dedicated memory and usage", func() {
			adapters := []osAdapter{{Name: "Radeon", VendorID: 0x1002, LUID: 7, DedicatedVideoMemory: 16 * gib}}
			usage := map[uint64]osAdapterUsage{7: {Dedicated: 4 * gib}}
			infos := adapterGPUInfos(adapters, usage)
			Expect(infos).To(HaveLen(1))
			Expect(infos[0].Vendor).To(Equal(VendorAMD))
			Expect(infos[0].TotalVRAM).To(BeEquivalentTo(16 * gib))
			Expect(infos[0].UsedVRAM).To(BeEquivalentTo(4 * gib))
			Expect(infos[0].FreeVRAM).To(BeEquivalentTo(12 * gib))
			Expect(infos[0].UsagePercent).To(BeNumerically("~", 25, 0.01))
		})

		It("uses shared memory for an integrated GPU", func() {
			adapters := []osAdapter{{Name: "Iris Xe", VendorID: 0x8086, LUID: 3, DedicatedVideoMemory: 128 << 20, SharedSystemMemory: 8 * gib}}
			usage := map[uint64]osAdapterUsage{3: {Dedicated: 10, Shared: 1 * gib}}
			infos := adapterGPUInfos(adapters, usage)
			Expect(infos).To(HaveLen(1))
			Expect(infos[0].Vendor).To(Equal(VendorIntel))
			Expect(infos[0].TotalVRAM).To(BeEquivalentTo(8 * gib))
			Expect(infos[0].UsedVRAM).To(BeEquivalentTo(1 * gib))
		})

		It("skips software and unknown adapters", func() {
			adapters := []osAdapter{
				{Name: "Microsoft Basic Render Driver", VendorID: 0x1414, SharedSystemMemory: 8 * gib, Software: true},
				{Name: "Unknown", VendorID: 0x1234, DedicatedVideoMemory: 8 * gib},
			}
			Expect(adapterGPUInfos(adapters, nil)).To(BeEmpty())
		})

		It("marks the usage unknown, not free as total, when the counters are missing", func() {
			adapters := []osAdapter{{Name: "RTX", VendorID: 0x10de, DedicatedVideoMemory: 8 * gib}}
			infos := adapterGPUInfos(adapters, nil)
			Expect(infos).To(HaveLen(1))
			Expect(infos[0].TotalVRAM).To(BeEquivalentTo(8 * gib))
			Expect(infos[0].UsageUnknown).To(BeTrue())
			Expect(infos[0].UsedVRAM).To(BeZero())
			Expect(infos[0].FreeVRAM).To(BeZero())
			Expect(infos[0].UsagePercent).To(BeZero())
		})

		It("marks the usage unknown for an adapter the counters do not list", func() {
			adapters := []osAdapter{
				{VendorID: 0x10de, LUID: 1, DedicatedVideoMemory: 8 * gib},
				{VendorID: 0x1002, LUID: 2, DedicatedVideoMemory: 4 * gib},
			}
			infos := adapterGPUInfos(adapters, map[uint64]osAdapterUsage{1: {Dedicated: 2 * gib}})
			Expect(infos).To(HaveLen(2))
			Expect(infos[0].UsageUnknown).To(BeFalse())
			Expect(infos[0].FreeVRAM).To(BeEquivalentTo(6 * gib))
			Expect(infos[1].UsageUnknown).To(BeTrue())
			Expect(infos[1].FreeVRAM).To(BeZero())
		})

		It("caps a usage above the total", func() {
			adapters := []osAdapter{{VendorID: 0x10de, LUID: 1, DedicatedVideoMemory: 2 * gib}}
			infos := adapterGPUInfos(adapters, map[uint64]osAdapterUsage{1: {Dedicated: 3 * gib}})
			Expect(infos[0].UsedVRAM).To(BeEquivalentTo(2 * gib))
			Expect(infos[0].FreeVRAM).To(BeZero())
		})
	})

	Describe("mergeOSAdapters", func() {
		It("keeps the vendor tool entries and adds the other vendors", func() {
			smi := []GPUMemoryInfo{{Index: 0, Vendor: VendorNVIDIA, Name: "from nvidia-smi"}}
			osGPUs := []GPUMemoryInfo{
				{Vendor: VendorNVIDIA, Name: "from DXGI"},
				{Vendor: VendorIntel, Name: "iGPU"},
			}
			merged := mergeOSAdapters(smi, osGPUs)
			Expect(merged).To(HaveLen(2))
			Expect(merged[0].Name).To(Equal("from nvidia-smi"))
			Expect(merged[1].Name).To(Equal("iGPU"))
			Expect(merged[1].Index).To(Equal(1))
		})
	})

	Describe("vendorFromAdapterInfos", func() {
		It("prefers NVIDIA, then AMD, then Intel", func() {
			Expect(vendorFromAdapterInfos([]GPUMemoryInfo{{Vendor: VendorIntel}, {Vendor: VendorAMD}})).To(Equal(VendorAMD))
			Expect(vendorFromAdapterInfos(nil)).To(BeEmpty())
		})
	})
})

var _ = Describe("aggregateGPUInfo", func() {
	It("keeps an unknown usage unknown, also under a VRAM budget", func() {
		budget, err := vrambudget.Parse("50%")
		Expect(err).ToNot(HaveOccurred())
		gpus := []GPUMemoryInfo{
			{TotalVRAM: 8 * gib, UsedVRAM: 2 * gib, FreeVRAM: 6 * gib, UsagePercent: 25},
			{TotalVRAM: 8 * gib, UsageUnknown: true},
		}

		aggregate := aggregateGPUInfo(gpus, budget)

		Expect(aggregate.UsageUnknown).To(BeTrue())
		Expect(aggregate.TotalVRAM).To(BeEquivalentTo(8 * gib))
		Expect(aggregate.FreeVRAM).To(BeZero())
		Expect(aggregate.UsedVRAM).To(BeZero())
		// Not 100: the memory reclaimer must not read unknown as full.
		Expect(aggregate.UsagePercent).To(BeZero())
	})

	It("sums known usages as before", func() {
		gpus := []GPUMemoryInfo{
			{TotalVRAM: 8 * gib, UsedVRAM: 2 * gib, FreeVRAM: 6 * gib},
			{TotalVRAM: 8 * gib, UsedVRAM: 6 * gib, FreeVRAM: 2 * gib},
		}

		aggregate := aggregateGPUInfo(gpus, vrambudget.Budget{})

		Expect(aggregate.UsageUnknown).To(BeFalse())
		Expect(aggregate.FreeVRAM).To(BeEquivalentTo(8 * gib))
		Expect(aggregate.UsagePercent).To(BeNumerically("==", 50))
	})
})
