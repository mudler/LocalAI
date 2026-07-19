package xsysinfo

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("vendorMatchesAny", func() {
	It("matches a PCI vendor name case-insensitively", func() {
		// ghw renders vendor names as they appear in pci.ids
		// ("NVIDIA Corporation"), while callers ask for the lowercase
		// vendor constants. A case-sensitive comparison misses entirely
		// and only appears to work because ghw's card description also
		// embeds the lowercase kernel driver name.
		Expect(vendorMatchesAny([]string{"NVIDIA Corporation"}, VendorNVIDIA)).To(BeTrue())
		Expect(vendorMatchesAny([]string{"Advanced Micro Devices, Inc. [AMD/ATI]"}, VendorAMD)).To(BeTrue())
		Expect(vendorMatchesAny([]string{"Intel Corporation"}, VendorIntel)).To(BeTrue())
	})

	It("does not match a different vendor", func() {
		Expect(vendorMatchesAny([]string{"Intel Corporation"}, VendorNVIDIA)).To(BeFalse())
	})

	It("finds a match anywhere in the list", func() {
		Expect(vendorMatchesAny([]string{"ASPEED Technology, Inc.", "Intel Corporation"}, VendorIntel)).To(BeTrue())
	})

	It("returns false when no names are available", func() {
		// ghw yields no names at all when it cannot enumerate, which is
		// what happens with no pci.ids database on the host.
		Expect(vendorMatchesAny(nil, VendorNVIDIA)).To(BeFalse())
	})
})
