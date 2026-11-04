package xsysinfo

import (
	"os"
	"path/filepath"

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
