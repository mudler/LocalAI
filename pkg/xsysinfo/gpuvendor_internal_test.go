package xsysinfo

import (
	"github.com/jaypipes/ghw/pkg/gpu"
	"github.com/jaypipes/ghw/pkg/pci"
	"github.com/jaypipes/pcidb"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func card(vendorID, vendorName string) *gpu.GraphicsCard {
	return &gpu.GraphicsCard{
		DeviceInfo: &pci.Device{
			Vendor: &pcidb.Vendor{ID: vendorID, Name: vendorName},
		},
	}
}

var _ = Describe("vendorFromGHW", func() {
	It("identifies the vendor from the numeric PCI ID", func() {
		Expect(vendorFromGHW([]*gpu.GraphicsCard{card("10de", "NVIDIA Corporation")})).To(Equal(VendorNVIDIA))
		Expect(vendorFromGHW([]*gpu.GraphicsCard{card("1002", "Advanced Micro Devices, Inc. [AMD/ATI]")})).To(Equal(VendorAMD))
		Expect(vendorFromGHW([]*gpu.GraphicsCard{card("8086", "Intel Corporation")})).To(Equal(VendorIntel))
	})

	It("identifies the vendor even when it is missing from the pci.ids database", func() {
		// ghw reads the vendor ID from the kernel modalias and only the
		// name from pci.ids, so a card absent from an outdated database
		// still carries a usable ID.
		Expect(vendorFromGHW([]*gpu.GraphicsCard{card("8086", "unknown")})).To(Equal(VendorIntel))
	})

	It("falls back to the vendor name when no usable ID is present", func() {
		Expect(vendorFromGHW([]*gpu.GraphicsCard{card("", "NVIDIA Corporation")})).To(Equal(VendorNVIDIA))
	})

	It("prefers a discrete NVIDIA GPU over an integrated Intel one regardless of enumeration order", func() {
		cards := []*gpu.GraphicsCard{card("8086", "Intel Corporation"), card("10de", "NVIDIA Corporation")}
		Expect(vendorFromGHW(cards)).To(Equal(VendorNVIDIA))
	})

	It("prefers AMD over Intel", func() {
		cards := []*gpu.GraphicsCard{card("8086", "Intel Corporation"), card("1002", "AMD/ATI")}
		Expect(vendorFromGHW(cards)).To(Equal(VendorAMD))
	})

	It("returns empty for a device that is not a known GPU vendor", func() {
		// QEMU virtual VGA (1234:1111) and ASPEED BMC adapters land here.
		Expect(vendorFromGHW([]*gpu.GraphicsCard{card("1234", "unknown")})).To(BeEmpty())
	})

	It("tolerates cards with no device information at all", func() {
		Expect(vendorFromGHW([]*gpu.GraphicsCard{{}, nil})).To(BeEmpty())
		Expect(vendorFromGHW(nil)).To(BeEmpty())
	})
})
