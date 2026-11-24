package xsysinfo

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const nvidiaRTX5070TiJSON = `{
  "devices": [
    {
      "online": [
        {
          "CL_DEVICE_NAME": "NVIDIA GeForce RTX 5070 Ti",
          "CL_DEVICE_VENDOR": "NVIDIA Corporation",
          "CL_DEVICE_VENDOR_ID": 4318,
          "CL_DEVICE_TYPE": {"raw": 4, "type": ["CL_DEVICE_TYPE_GPU"]},
          "CL_DEVICE_HOST_UNIFIED_MEMORY": false,
          "CL_DEVICE_GLOBAL_MEM_SIZE": 16609378304,
          "CL_DEVICE_PCI_BUS_INFO_KHR": "PCI-E, 0000:01:00.0",
          "CL_DEVICE_PCI_BUS_ID_NV": 1,
          "CL_DEVICE_PCI_SLOT_ID_NV": 0,
          "CL_DEVICE_PCI_DOMAIN_ID_NV": 0
        }
      ]
    }
  ]
}`

// intelArcPlusIGPUJSON exercises the HOST_UNIFIED_MEMORY=true filter:
// the iGPU sibling on the same Intel platform must be dropped to
// avoid double-counting system RAM as VRAM.
const intelArcPlusIGPUJSON = `{
  "devices": [
    {
      "online": [
        {
          "CL_DEVICE_NAME": "Intel(R) Arc(TM) A770 Graphics",
          "CL_DEVICE_VENDOR": "Intel(R) Corporation",
          "CL_DEVICE_VENDOR_ID": 32902,
          "CL_DEVICE_TYPE": {"raw": 4, "type": ["CL_DEVICE_TYPE_GPU"]},
          "CL_DEVICE_HOST_UNIFIED_MEMORY": false,
          "CL_DEVICE_GLOBAL_MEM_SIZE": 16225243136,
          "CL_DEVICE_PCI_BUS_INFO_KHR": "0000:03:00.0"
        },
        {
          "CL_DEVICE_NAME": "Intel(R) UHD Graphics 770",
          "CL_DEVICE_VENDOR": "Intel(R) Corporation",
          "CL_DEVICE_VENDOR_ID": 32902,
          "CL_DEVICE_TYPE": {"raw": 4, "type": ["CL_DEVICE_TYPE_GPU"]},
          "CL_DEVICE_HOST_UNIFIED_MEMORY": true,
          "CL_DEVICE_GLOBAL_MEM_SIZE": 25000000000,
          "CL_DEVICE_PCI_BUS_INFO_KHR": "0000:00:02.0"
        }
      ]
    }
  ]
}`

// dualICDSameDeviceJSON exercises BDF dedup when two ICDs enumerate
// the same physical device with different reported sizes (POCL caps
// at 4 GiB for legacy alloc-size compatibility).
const dualICDSameDeviceJSON = `{
  "devices": [
    {
      "online": [
        {
          "CL_DEVICE_NAME": "Intel(R) Arc(TM) A770 Graphics",
          "CL_DEVICE_VENDOR_ID": 32902,
          "CL_DEVICE_TYPE": {"raw": 4, "type": ["CL_DEVICE_TYPE_GPU"]},
          "CL_DEVICE_HOST_UNIFIED_MEMORY": false,
          "CL_DEVICE_GLOBAL_MEM_SIZE": 16225243136,
          "CL_DEVICE_PCI_BUS_INFO_KHR": "0000:03:00.0"
        }
      ]
    },
    {
      "online": [
        {
          "CL_DEVICE_NAME": "pthread-Arc-A770",
          "CL_DEVICE_VENDOR_ID": 32902,
          "CL_DEVICE_TYPE": {"raw": 4, "type": ["CL_DEVICE_TYPE_GPU"]},
          "CL_DEVICE_HOST_UNIFIED_MEMORY": false,
          "CL_DEVICE_GLOBAL_MEM_SIZE": 4294967296,
          "CL_DEVICE_PCI_BUS_INFO_KHR": "0000:03:00.0"
        }
      ]
    }
  ]
}`

// cpuOnlyJSON: a POCL-only host. Filtered by CL_DEVICE_TYPE — without
// this guard CPU memory would be mistakenly reported as VRAM.
const cpuOnlyJSON = `{
  "devices": [
    {
      "online": [
        {
          "CL_DEVICE_NAME": "pthread-x86_64",
          "CL_DEVICE_VENDOR": "GenuineIntel",
          "CL_DEVICE_VENDOR_ID": 32902,
          "CL_DEVICE_TYPE": {"raw": 2, "type": ["CL_DEVICE_TYPE_CPU"]},
          "CL_DEVICE_HOST_UNIFIED_MEMORY": true,
          "CL_DEVICE_GLOBAL_MEM_SIZE": 33324494848
        }
      ]
    }
  ]
}`

// noBDFJSON: an ICD that reports no PCI fields at all. Device is
// still counted but doesn't participate in dedup.
const noBDFJSON = `{
  "devices": [
    {
      "online": [
        {
          "CL_DEVICE_NAME": "Some Accelerator GPU",
          "CL_DEVICE_VENDOR_ID": 0,
          "CL_DEVICE_TYPE": {"raw": 4, "type": ["CL_DEVICE_TYPE_GPU"]},
          "CL_DEVICE_HOST_UNIFIED_MEMORY": false,
          "CL_DEVICE_GLOBAL_MEM_SIZE": 8589934592
        }
      ]
    }
  ]
}`

var _ = Describe("parseCLInfoJSON", func() {
	DescribeTable("classifies and dedups clinfo devices",
		func(input string, wantCount int, want []GPUMemoryInfo) {
			got := parseCLInfoJSON([]byte(input))
			Expect(got).To(HaveLen(wantCount))
			for i, w := range want {
				Expect(got[i].Name).To(Equal(w.Name))
				Expect(got[i].Vendor).To(Equal(w.Vendor))
				Expect(got[i].TotalVRAM).To(Equal(w.TotalVRAM))
			}
		},
		Entry("empty object returns nothing", `{}`, 0, nil),
		Entry("malformed JSON returns nothing without panicking", `{not valid`, 0, nil),
		Entry("CPU-only platform is filtered out", cpuOnlyJSON, 0, nil),
		Entry("NVIDIA dGPU is recognised by vendor ID and BDF",
			nvidiaRTX5070TiJSON, 1, []GPUMemoryInfo{{
				Name:      "NVIDIA GeForce RTX 5070 Ti",
				Vendor:    VendorNVIDIA,
				TotalVRAM: 16609378304,
			}}),
		Entry("Intel Arc with iGPU sibling: iGPU dropped, Arc reported",
			intelArcPlusIGPUJSON, 1, []GPUMemoryInfo{{
				Name:      "Intel(R) Arc(TM) A770 Graphics",
				Vendor:    VendorIntel,
				TotalVRAM: 16225243136,
			}}),
		Entry("Dual ICD enumerating same Arc: deduped, larger size wins",
			dualICDSameDeviceJSON, 1, []GPUMemoryInfo{{
				Name:      "Intel(R) Arc(TM) A770 Graphics",
				Vendor:    VendorIntel,
				TotalVRAM: 16225243136, // not the POCL 4 GiB cap
			}}),
		Entry("Device without PCI info is still counted",
			noBDFJSON, 1, []GPUMemoryInfo{{
				Name:      "Some Accelerator GPU",
				Vendor:    VendorUnknown,
				TotalVRAM: 8589934592,
			}}),
	)
})

var _ = Describe("normalizeBDF", func() {
	DescribeTable("canonicalises PCI bus addresses",
		func(in, want string) {
			Expect(normalizeBDF(in)).To(Equal(want))
		},
		Entry("already canonical", "0000:03:00.0", "0000:03:00.0"),
		Entry("missing domain", "03:00.0", "0000:03:00.0"),
		Entry("uppercase hex", "AB:CD.0", "0000:ab:cd.0"),
	)
})

var _ = Describe("clinfoBDF", func() {
	It("synthesises a canonical BDF from NVIDIA pre-KHR integer fields", func() {
		// Older NVIDIA OpenCL exposes BDF via three integer fields instead
		// of the KHR string; the synthesised result must be canonical.
		d := clinfoDevice{
			PCIBusNV:    1,
			PCISlotNV:   0,
			PCIDomainNV: 0,
		}
		Expect(clinfoBDF(d)).To(Equal("0000:01:00.0"))
	})
})
