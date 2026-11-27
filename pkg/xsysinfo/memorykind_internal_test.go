package xsysinfo

import (
	es "github.com/mudler/LocalAI/pkg/enginesettings"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ClassifyMemory", func() {
	gpu := func(vendor, name string) GPUMemoryInfo { return GPUMemoryInfo{Vendor: vendor, Name: name} }

	DescribeTable("reads the memory kind from the GPUs",
		func(gpus []GPUMemoryInfo, tegra bool, kind, device string) {
			k, d := ClassifyMemory(gpus, tegra)
			Expect(k).To(Equal(kind))
			Expect(d).To(Equal(device))
		},
		Entry("no GPU", nil, false, MemoryKindSystem, ""),
		Entry("a GeForce card", []GPUMemoryInfo{gpu(VendorNVIDIA, "NVIDIA GeForce RTX 5090")}, false, MemoryKindDiscrete, "NVIDIA GeForce RTX 5090"),
		Entry("a GB10 (DGX Spark)", []GPUMemoryInfo{gpu(VendorNVIDIA, "NVIDIA GB10")}, false, MemoryKindUnified, "NVIDIA GB10"),
		Entry("a Jetson Orin", []GPUMemoryInfo{gpu(VendorNVIDIA, "Orin (nvgpu)")}, false, MemoryKindUnified, "Orin (nvgpu)"),
		Entry("a Jetson with no GPU listed", nil, true, MemoryKindUnified, "NVIDIA Jetson"),
		Entry("Apple silicon", []GPUMemoryInfo{gpu(VendorApple, "Apple M4 Max")}, false, MemoryKindUnified, "Apple M4 Max"),
		Entry("Strix Halo by name", []GPUMemoryInfo{gpu(VendorAMD, "AMD Radeon 8060S")}, false, MemoryKindUnified, "AMD Radeon 8060S"),
		Entry("an AMD APU by its GTT pool", []GPUMemoryInfo{{Vendor: VendorAMD, Name: "AMD GPU", SharedMemory: true}}, false, MemoryKindUnified, "AMD GPU"),
		Entry("a Radeon RX card", []GPUMemoryInfo{gpu(VendorAMD, "AMD Radeon RX 7900 XTX")}, false, MemoryKindDiscrete, "AMD Radeon RX 7900 XTX"),
		Entry("Intel integrated Arc", []GPUMemoryInfo{gpu(VendorIntel, "Intel(R) Arc(TM) Graphics")}, false, MemoryKindUnified, "Intel(R) Arc(TM) Graphics"),
		Entry("Intel discrete Arc", []GPUMemoryInfo{gpu(VendorIntel, "Intel(R) Arc(TM) A770 Graphics")}, false, MemoryKindDiscrete, "Intel(R) Arc(TM) A770 Graphics"),
		Entry("a discrete card beside an iGPU wins", []GPUMemoryInfo{gpu(VendorAMD, "AMD Radeon 780M"), gpu(VendorNVIDIA, "NVIDIA GeForce RTX 4090")}, false, MemoryKindDiscrete, "NVIDIA GeForce RTX 4090"),
	)
})

var _ = Describe("HostMemoryOf", func() {
	const gib = uint64(1) << 30

	It("uses the smallest discrete GPU, not an integrated one beside it", func() {
		m := HostMemoryOf([]GPUMemoryInfo{
			{Vendor: VendorAMD, Name: "AMD Radeon 780M", TotalVRAM: 2 * gib},
			{Vendor: VendorNVIDIA, Name: "NVIDIA GeForce RTX 4090", TotalVRAM: 24 * gib},
			{Vendor: VendorNVIDIA, Name: "NVIDIA GeForce RTX 4080", TotalVRAM: 16 * gib},
		})
		Expect(m.Kind).To(Equal(MemoryKindDiscrete))
		Expect(m.Device).To(Equal("NVIDIA GeForce RTX 4090"))
		Expect(m.Total).To(Equal(16 * gib))
	})

	It("names the same kinds as the quick setup of the model editor", func() {
		Expect([]string{MemoryKindUnified, MemoryKindDiscrete, MemoryKindSystem}).To(Equal([]string{
			string(es.MemoryUnified), string(es.MemoryDiscrete), string(es.MemorySystem),
		}))
	})
})
