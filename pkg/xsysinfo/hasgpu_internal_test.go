package xsysinfo

import (
	"github.com/jaypipes/ghw/pkg/gpu"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ghwHasVendor", func() {
	It("matches on the numeric PCI vendor ID", func() {
		cards := []*gpu.GraphicsCard{card("8086", "Intel Corporation")}
		Expect(ghwHasVendor(cards, VendorIntel)).To(BeTrue())
		Expect(ghwHasVendor(cards, VendorNVIDIA)).To(BeFalse())
	})

	It("reports every vendor present, not only the highest-priority one", func() {
		// A hybrid-graphics host has both. Asking "is there an Intel GPU"
		// must not be answered by the discrete NVIDIA card outranking it.
		cards := []*gpu.GraphicsCard{card("8086", "Intel Corporation"), card("10de", "NVIDIA Corporation")}
		Expect(ghwHasVendor(cards, VendorNVIDIA)).To(BeTrue())
		Expect(ghwHasVendor(cards, VendorIntel)).To(BeTrue())
		Expect(ghwHasVendor(cards, VendorAMD)).To(BeFalse())
	})

	It("matches the vendor name case-insensitively when no ID is available", func() {
		// pci.ids spells it "NVIDIA Corporation"; callers pass the
		// lowercase vendor constant.
		Expect(ghwHasVendor([]*gpu.GraphicsCard{card("", "NVIDIA Corporation")}, VendorNVIDIA)).To(BeTrue())
	})

	It("does not match a device that is not a known GPU vendor", func() {
		Expect(ghwHasVendor([]*gpu.GraphicsCard{card("1234", "unknown")}, VendorIntel)).To(BeFalse())
	})

	It("tolerates missing device information", func() {
		Expect(ghwHasVendor([]*gpu.GraphicsCard{{}, nil}, VendorIntel)).To(BeFalse())
		Expect(ghwHasVendor(nil, VendorIntel)).To(BeFalse())
	})
})
