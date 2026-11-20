package xsysinfo

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("readAMDSysfsGPUMemory", func() {
	var root string

	BeforeEach(func() {
		root = filepath.Join(GinkgoT().TempDir(), "class", "drm")
		Expect(os.MkdirAll(root, 0o755)).To(Succeed())
	})

	// Values read inside a ROCm 7+ container on Strix Halo (gfx1151),
	// where rocm-smi is no longer shipped.
	strixHalo := map[string]string{
		"vendor":              "0x1002\n",
		"mem_info_vram_total": "536870912\n",
		"mem_info_vram_used":  "240300032\n",
		"mem_info_gtt_total":  "128849018880\n",
		"mem_info_gtt_used":   "33055911936\n",
	}

	It("reports a shared-memory APU with GTT folded into totals", func() {
		writeSysfsCard(root, "card0", "0000:c5:00.0", strixHalo)

		got := readAMDSysfsGPUMemory(root)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Vendor).To(Equal(VendorAMD))
		Expect(got[0].Index).To(Equal(0))
		Expect(got[0].TotalVRAM).To(Equal(uint64(536870912 + 128849018880)))
		Expect(got[0].UsedVRAM).To(Equal(uint64(240300032 + 33055911936)))
		Expect(got[0].FreeVRAM).To(Equal(got[0].TotalVRAM - got[0].UsedVRAM))
		Expect(got[0].UsagePercent).To(BeNumerically(">", 0))
	})

	It("keeps a discrete card on VRAM only", func() {
		writeSysfsCard(root, "card1", "0000:03:00.0", map[string]string{
			"vendor":              "0x1002\n",
			"mem_info_vram_total": "25769803776\n",
			"mem_info_vram_used":  "1073741824\n",
			"mem_info_gtt_total":  "34359738368\n",
			"mem_info_gtt_used":   "1048576\n",
		})

		got := readAMDSysfsGPUMemory(root)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Index).To(Equal(1))
		Expect(got[0].TotalVRAM).To(Equal(uint64(25769803776)))
		Expect(got[0].UsedVRAM).To(Equal(uint64(1073741824)))
	})

	It("ignores non-AMD cards and connector entries", func() {
		writeSysfsCard(root, "card0", "0000:c5:00.0", strixHalo)
		// A connector hanging off the same device must not count twice.
		Expect(os.MkdirAll(filepath.Join(root, "card0-DP-1"), 0o755)).To(Succeed())
		Expect(os.Symlink(filepath.Join(root, "..", "devices", "pci", "0000:c5:00.0"),
			filepath.Join(root, "card0-DP-1", "device"))).To(Succeed())
		// An NVIDIA card exposing the same attribute names is not ours.
		nv := map[string]string{}
		for k, v := range strixHalo {
			nv[k] = v
		}
		nv["vendor"] = "0x10de\n"
		writeSysfsCard(root, "card1", "0000:01:00.0", nv)

		got := readAMDSysfsGPUMemory(root)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Index).To(Equal(0))
	})

	It("reports nothing for an AMD card without mem_info attributes", func() {
		writeSysfsCard(root, "card0", "0000:c5:00.0", map[string]string{
			"vendor": "0x1002\n",
		})
		Expect(readAMDSysfsGPUMemory(root)).To(BeEmpty())
	})

	It("reports nothing when the DRM tree is missing", func() {
		Expect(readAMDSysfsGPUMemory(filepath.Join(root, "nope"))).To(BeEmpty())
	})
})

var _ = Describe("AMD sysfs GPU names", func() {
	var root string

	BeforeEach(func() {
		root = filepath.Join(GinkgoT().TempDir(), "class", "drm")
		Expect(os.MkdirAll(root, 0o755)).To(Succeed())
		original := listVulkanDevices
		DeferCleanup(func() { listVulkanDevices = original })
		listVulkanDevices = func() []vulkanDevice { return nil }
	})

	card := func(name, bdf, product string) {
		attrs := map[string]string{
			"vendor":              "0x1002\n",
			"mem_info_vram_total": "25769803776\n",
			"mem_info_vram_used":  "1073741824\n",
		}
		if product != "" {
			attrs["product_name"] = product + "\n"
		}
		writeSysfsCard(root, name, bdf, attrs)
	}

	It("uses the product_name attribute and skips vulkaninfo", func() {
		listVulkanDevices = func() []vulkanDevice {
			Fail("vulkaninfo must not run when every card has a product_name")
			return nil
		}
		card("card0", "0000:03:00.0", "AMD Radeon PRO W7900")

		got := readAMDSysfsGPUMemory(root)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Name).To(Equal("AMD Radeon PRO W7900"))
		Expect(got[0].TotalVRAM).To(Equal(uint64(25769803776)))
		Expect(got[0].UsedVRAM).To(Equal(uint64(1073741824)))
	})

	It("takes the Vulkan name by PCI address when product_name is absent", func() {
		card("card0", "0000:03:00.0", "")
		listVulkanDevices = func() []vulkanDevice {
			return []vulkanDevice{
				{name: "NVIDIA GeForce RTX 4090", vendorID: 0x10de, busAddr: "0000:01:00.0"},
				{name: "AMD Radeon RX 7900 XTX (RADV NAVI31)", vendorID: 0x1002, busAddr: "0000:03:00.0"},
			}
		}

		got := readAMDSysfsGPUMemory(root)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Name).To(Equal("AMD Radeon RX 7900 XTX (RADV NAVI31)"))
		Expect(got[0].TotalVRAM).To(Equal(uint64(25769803776)))
		Expect(got[0].UsedVRAM).To(Equal(uint64(1073741824)))
	})

	It("falls back to a numbered name when nothing names the card", func() {
		card("card0", "0000:03:00.0", "")
		card("card1", "0000:04:00.0", "")

		got := readAMDSysfsGPUMemory(root)
		Expect(got).To(HaveLen(2))
		Expect(got[0].Name).To(Equal("AMD GPU 0"))
		Expect(got[1].Name).To(Equal("AMD GPU 1"))
		Expect(got[1].TotalVRAM).To(Equal(uint64(25769803776)))
	})

	It("keeps two cards apart when Vulkan lists them in another order", func() {
		card("card0", "0000:03:00.0", "")
		card("card1", "0000:04:00.0", "")
		listVulkanDevices = func() []vulkanDevice {
			return []vulkanDevice{
				{name: "Second", vendorID: 0x1002, busAddr: "0000:04:00.0"},
				{name: "First", vendorID: 0x1002, busAddr: "0000:03:00.0"},
			}
		}

		got := readAMDSysfsGPUMemory(root)
		Expect(got).To(HaveLen(2))
		Expect(got[0].Name).To(Equal("First"))
		Expect(got[1].Name).To(Equal("Second"))
	})

	It("pairs by order only when Vulkan reports no addresses and counts agree", func() {
		card("card0", "0000:03:00.0", "")
		card("card1", "0000:04:00.0", "")
		listVulkanDevices = func() []vulkanDevice {
			return []vulkanDevice{{name: "A", vendorID: 0x1002}, {name: "B", vendorID: 0x1002}}
		}
		got := readAMDSysfsGPUMemory(root)
		Expect([]string{got[0].Name, got[1].Name}).To(Equal([]string{"A", "B"}))

		listVulkanDevices = func() []vulkanDevice {
			return []vulkanDevice{{name: "A", vendorID: 0x1002}}
		}
		got = readAMDSysfsGPUMemory(root)
		Expect([]string{got[0].Name, got[1].Name}).To(Equal([]string{"AMD GPU 0", "AMD GPU 1"}))
	})

	It("parses vulkaninfo devices and drops CPU renderers", func() {
		text := `GPU0:
VkPhysicalDeviceProperties:
	vendorID   = 0x1002
	deviceType = PHYSICAL_DEVICE_TYPE_DISCRETE_GPU
	deviceName = AMD Radeon RX 7900 XTX (RADV NAVI31)
VkPhysicalDevicePCIBusInfoPropertiesEXT:
	pciDomain   = 0x0000
	pciBus      = 0x03
	pciDevice   = 0x00
	pciFunction = 0x0
GPU1:
VkPhysicalDeviceProperties:
	vendorID   = 0x10005
	deviceType = PHYSICAL_DEVICE_TYPE_CPU
	deviceName = llvmpipe (LLVM 17.0.6, 256 bits)
`
		devs := parseVulkanDevices(strings.NewReader(text))
		Expect(devs).To(Equal([]vulkanDevice{{
			name: "AMD Radeon RX 7900 XTX (RADV NAVI31)", vendorID: 0x1002, busAddr: "0000:03:00.0",
		}}))
	})
})

var _ = Describe("AMD sysfs GPU names from pci.ids", func() {
	var root, idsFile string

	const fakeIDs = `# list of known PCI ids
# comment line

1000  Broadcom
	1586  Not An AMD Device
1002  Advanced Micro Devices, Inc. [AMD/ATI]
	1586  Strix Halo [Radeon Graphics / Radeon 8050S Graphics / Radeon 8060S Graphics]
		2014 801d  Subsystem line that must be skipped
	744c  Navi 31 [Radeon RX 7900 XT/7900 XTX/7900 GRE/7900M]
	7777  Plain Chip
	9999  AMD Instinct Fake
1003  Next Vendor
	abcd  Wrong Vendor Device
`

	BeforeEach(func() {
		root = filepath.Join(GinkgoT().TempDir(), "class", "drm")
		Expect(os.MkdirAll(root, 0o755)).To(Succeed())
		idsFile = filepath.Join(GinkgoT().TempDir(), "pci.ids")
		Expect(os.WriteFile(idsFile, []byte(fakeIDs), 0o644)).To(Succeed())
		origIDs, origVulkan := pciIDsPaths, listVulkanDevices
		DeferCleanup(func() { pciIDsPaths, listVulkanDevices = origIDs, origVulkan })
		pciIDsPaths = []string{idsFile}
		listVulkanDevices = func() []vulkanDevice { return nil }
	})

	card := func(device, product string) {
		attrs := map[string]string{
			"vendor":              "0x1002\n",
			"device":              device + "\n",
			"mem_info_vram_total": "536870912\n",
			"mem_info_vram_used":  "240300032\n",
		}
		if product != "" {
			attrs["product_name"] = product + "\n"
		}
		writeSysfsCard(root, "card0", "0000:c5:00.0", attrs)
	}

	name := func() string {
		got := readAMDSysfsGPUMemory(root)
		Expect(got).To(HaveLen(1))
		Expect(got[0].TotalVRAM).To(Equal(uint64(536870912)))
		Expect(got[0].UsedVRAM).To(Equal(uint64(240300032)))
		return got[0].Name
	}

	It("keeps the bracketed marketing text", func() {
		card("0x1586", "")
		Expect(name()).To(Equal("AMD Radeon Graphics / Radeon 8050S Graphics / Radeon 8060S Graphics"))
	})

	It("uses the bare name, prefixed, when there are no brackets", func() {
		card("0x7777", "")
		Expect(name()).To(Equal("AMD Plain Chip"))
	})

	It("does not double an existing AMD prefix", func() {
		card("0x9999", "")
		Expect(name()).To(Equal("AMD Instinct Fake"))
	})

	It("falls back when the device is absent from the AMD block", func() {
		card("0xabcd", "")
		Expect(name()).To(Equal("AMD GPU 0"))
	})

	It("falls back when the vendor is absent", func() {
		Expect(os.WriteFile(idsFile, []byte("1000  Broadcom\n\t1586  X\n"), 0o644)).To(Succeed())
		card("0x1586", "")
		Expect(name()).To(Equal("AMD GPU 0"))
	})

	It("falls back silently when no pci.ids file exists", func() {
		pciIDsPaths = []string{filepath.Join(filepath.Dir(idsFile), "missing")}
		card("0x1586", "")
		Expect(name()).To(Equal("AMD GPU 0"))
	})

	It("does not read a subsystem line as a device", func() {
		Expect(os.WriteFile(idsFile, []byte("1002  AMD\n\t1111  Real\n\t\t1586  Sub Only\n"), 0o644)).To(Succeed())
		card("0x1586", "")
		Expect(name()).To(Equal("AMD GPU 0"))
	})

	It("reads a gzipped database", func() {
		gz := filepath.Join(filepath.Dir(idsFile), "pci.ids.gz")
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		_, err := zw.Write([]byte(fakeIDs))
		Expect(err).NotTo(HaveOccurred())
		Expect(zw.Close()).To(Succeed())
		Expect(os.WriteFile(gz, buf.Bytes(), 0o644)).To(Succeed())
		pciIDsPaths = []string{gz}
		card("0x7777", "")
		Expect(name()).To(Equal("AMD Plain Chip"))
	})

	It("lets product_name win over pci.ids", func() {
		card("0x1586", "Board Name")
		Expect(name()).To(Equal("Board Name"))
	})

	It("lets a Vulkan name win over pci.ids", func() {
		card("0x1586", "")
		listVulkanDevices = func() []vulkanDevice {
			return []vulkanDevice{{name: "Vulkan Name", vendorID: 0x1002, busAddr: "0000:c5:00.0"}}
		}
		Expect(name()).To(Equal("Vulkan Name"))
	})
})
