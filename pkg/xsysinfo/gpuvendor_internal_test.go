package xsysinfo

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("vendorFromNames", func() {
	It("matches a vendor name reported by the PCI database", func() {
		Expect(vendorFromNames([]string{"Intel Corporation"})).To(Equal(VendorIntel))
		Expect(vendorFromNames([]string{"NVIDIA Corporation"})).To(Equal(VendorNVIDIA))
		Expect(vendorFromNames([]string{"Advanced Micro Devices, Inc. [AMD/ATI]"})).To(Equal(VendorAMD))
	})

	It("prefers a discrete NVIDIA GPU over an integrated Intel one regardless of enumeration order", func() {
		// ghw lists cards by DRM index, so an integrated display adapter
		// commonly comes first. Returning "intel" for a machine with an
		// NVIDIA card would pick the wrong backend.
		Expect(vendorFromNames([]string{"Intel Corporation", "NVIDIA Corporation"})).To(Equal(VendorNVIDIA))
	})

	It("prefers AMD over Intel", func() {
		Expect(vendorFromNames([]string{"Intel Corporation", "Advanced Micro Devices, Inc. [AMD/ATI]"})).To(Equal(VendorAMD))
	})

	It("returns empty for vendor names it does not recognise", func() {
		// ghw reports "unknown" when the device is absent from pci.ids.
		Expect(vendorFromNames([]string{"unknown", "ASPEED Technology, Inc."})).To(BeEmpty())
	})

	It("returns empty when no vendor names are available", func() {
		Expect(vendorFromNames(nil)).To(BeEmpty())
	})
})
