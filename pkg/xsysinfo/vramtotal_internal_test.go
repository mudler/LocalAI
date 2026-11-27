package xsysinfo

import (
	"github.com/jaypipes/ghw/pkg/gpu"
	"github.com/jaypipes/ghw/pkg/memory"
	"github.com/jaypipes/ghw/pkg/pci"
	"github.com/jaypipes/ghw/pkg/topology"
	"github.com/jaypipes/pcidb"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/vrambudget"
)

// ghwCard builds a ghw card the way ghw reports it: the vendor from the PCI
// device, and Node.Memory from the NUMA node the card sits on. That memory is
// system RAM, not the card's VRAM.
func ghwCard(vendorID, vendorName string, numaNodeRAM uint64) *gpu.GraphicsCard {
	return &gpu.GraphicsCard{
		DeviceInfo: &pci.Device{Vendor: &pcidb.Vendor{ID: vendorID, Name: vendorName}},
		Node:       &topology.Node{Memory: &memory.Area{TotalUsableBytes: int64(numaNodeRAM)}},
	}
}

var _ = Describe("VRAM detection on a discrete NVIDIA host (#13531)", func() {
	const gib = uint64(1) << 30

	// The reporter's worker: two 20 GB RTX 4000 Ada cards, nvidia-smi gave
	// no per-device totals, and ghw reported the NUMA node RAM of each card.
	// The two node figures summed to the logged 202325295104 bytes.
	const reportedTotal = uint64(202325295104)
	nvidiaCards := func() []*gpu.GraphicsCard {
		return []*gpu.GraphicsCard{
			ghwCard("10de", "NVIDIA Corporation", reportedTotal/2),
			ghwCard("10de", "NVIDIA Corporation", reportedTotal/2),
		}
	}

	It("does not count the NUMA node RAM of NVIDIA cards as VRAM", func() {
		Expect(totalVRAM(nil, nvidiaCards())).To(BeZero())
		Expect(minGHWCardMemory(nvidiaCards())).To(BeZero())
	})

	It("still uses the nvidia-smi totals when they are there", func() {
		infos := []GPUMemoryInfo{{Vendor: VendorNVIDIA, TotalVRAM: 20 * gib}, {Vendor: VendorNVIDIA, TotalVRAM: 20 * gib}}
		Expect(totalVRAM(infos, nvidiaCards())).To(Equal(40 * gib))
	})

	It("keeps the ghw fallback for vendors whose GPU can share system memory", func() {
		cards := []*gpu.GraphicsCard{ghwCard("8086", "Intel Corporation", 16*gib)}
		Expect(totalVRAM(nil, cards)).To(Equal(16 * gib))
	})

	It("treats an implausible per-device total from a vendor tool as unknown", func() {
		infos := []GPUMemoryInfo{
			{Name: "broken", Vendor: VendorNVIDIA, TotalVRAM: maxPlausibleDeviceVRAM + 1},
			{Name: "ok", Vendor: VendorNVIDIA, TotalVRAM: 20 * gib},
		}
		Expect(totalVRAM(infos, nil)).To(Equal(20 * gib))
		Expect(minNonZeroVRAM(plausibleDevices(infos))).To(Equal(20 * gib))
	})
})

var _ = Describe("VRAM total override (LOCALAI_VRAM_TOTAL)", func() {
	const gb = uint64(1000 * 1000 * 1000)

	AfterEach(func() { SetVRAMTotalOverride(0) })

	It("parses a size and rejects a percentage", func() {
		Expect(SetVRAMTotalFromString("40GB")).To(Succeed())
		Expect(VRAMTotalOverride()).To(Equal(40 * gb))

		Expect(SetVRAMTotalFromString("80%")).ToNot(Succeed())
		Expect(VRAMTotalOverride()).To(BeZero())

		Expect(SetVRAMTotalFromString("")).To(Succeed())
		Expect(VRAMTotalOverride()).To(BeZero())
	})

	It("replaces the detected total and keeps the usage when it is known", func() {
		gpus := []GPUMemoryInfo{{TotalVRAM: 100 * gb, UsedVRAM: 10 * gb, FreeVRAM: 90 * gb}}
		agg := aggregateGPUInfo(gpus, 40*gb, vrambudget.Budget{})
		Expect(agg.TotalVRAM).To(Equal(40 * gb))
		Expect(agg.UsedVRAM).To(Equal(10 * gb))
		Expect(agg.FreeVRAM).To(Equal(30 * gb))
		Expect(agg.UsageUnknown).To(BeFalse())
	})

	It("reports the total with an unknown usage when no device reports memory", func() {
		agg := aggregateGPUInfo(nil, 40*gb, vrambudget.Budget{})
		Expect(agg.TotalVRAM).To(Equal(40 * gb))
		Expect(agg.UsageUnknown).To(BeTrue())
		Expect(agg.FreeVRAM).To(BeZero())
	})

	It("is the total the budget applies to", func() {
		b, err := vrambudget.Parse("50%")
		Expect(err).ToNot(HaveOccurred())
		gpus := []GPUMemoryInfo{{TotalVRAM: 100 * gb, UsedVRAM: 0, FreeVRAM: 100 * gb}}
		agg := aggregateGPUInfo(gpus, 40*gb, b)
		Expect(agg.TotalVRAM).To(Equal(20 * gb))
		Expect(agg.FreeVRAM).To(Equal(20 * gb))
	})

	It("splits the total over the GPUs for the per-device figure", func() {
		Expect(perGPUOverride(40*gb, nil, 2)).To(Equal(20 * gb))
		Expect(perGPUOverride(40*gb, nil, 0)).To(Equal(40 * gb))
		// A device that reports less than its share keeps its own figure.
		infos := []GPUMemoryInfo{{TotalVRAM: 16 * gb}, {TotalVRAM: 24 * gb}}
		Expect(perGPUOverride(48*gb, infos, 2)).To(Equal(16 * gb))
	})
})

var _ = Describe("aggregateGPUInfo with an implausible device", func() {
	It("does not sum it and marks the usage unknown", func() {
		const gib = uint64(1) << 30
		gpus := []GPUMemoryInfo{
			{Name: "broken", TotalVRAM: maxPlausibleDeviceVRAM + 1, FreeVRAM: maxPlausibleDeviceVRAM},
			{Name: "ok", TotalVRAM: 20 * gib, UsedVRAM: 2 * gib, FreeVRAM: 18 * gib},
		}
		agg := aggregateGPUInfo(gpus, 0, vrambudget.Budget{})
		Expect(agg.TotalVRAM).To(Equal(20 * gib))
		Expect(agg.UsageUnknown).To(BeTrue())
		Expect(agg.FreeVRAM).To(BeZero())
	})
})
