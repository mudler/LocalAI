package xsysinfo

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// writeSysfsCard lays out a fake /sys/class/drm entry the way the kernel
// does: card<N>/device is a symlink into the PCI device tree, named after
// the card's BDF.
func writeSysfsCard(root, card, bdf string, attrs map[string]string) {
	pciDir := filepath.Join(root, "..", "devices", "pci", bdf)
	ExpectWithOffset(1, os.MkdirAll(pciDir, 0o755)).To(Succeed())
	for name, value := range attrs {
		ExpectWithOffset(1, os.WriteFile(filepath.Join(pciDir, name), []byte(value), 0o644)).To(Succeed())
	}
	cardDir := filepath.Join(root, card)
	ExpectWithOffset(1, os.MkdirAll(cardDir, 0o755)).To(Succeed())
	ExpectWithOffset(1, os.Symlink(pciDir, filepath.Join(cardDir, "device"))).To(Succeed())
}

var _ = Describe("sysfs GPU discovery", func() {
	var root string

	BeforeEach(func() {
		root = filepath.Join(GinkgoT().TempDir(), "class", "drm")
		Expect(os.MkdirAll(root, 0o755)).To(Succeed())
	})

	Describe("scanSysfsGPUs", func() {
		It("identifies an Intel discrete GPU by its PCI vendor ID", func() {
			// Arc A310 (8086:56a6) as reported in issue #10941.
			writeSysfsCard(root, "card1", "0000:03:00.0", map[string]string{
				"vendor": "0x8086\n",
			})

			gpus := scanSysfsGPUs(root)

			Expect(gpus).To(HaveLen(1))
			Expect(gpus[0].Vendor).To(Equal(VendorIntel))
			Expect(gpus[0].Card).To(Equal("card1"))
		})

		It("identifies NVIDIA and AMD GPUs by their PCI vendor IDs", func() {
			writeSysfsCard(root, "card0", "0000:01:00.0", map[string]string{"vendor": "0x10de\n"})
			writeSysfsCard(root, "card1", "0000:02:00.0", map[string]string{"vendor": "0x1002\n"})

			gpus := scanSysfsGPUs(root)

			Expect(gpus).To(HaveLen(2))
			Expect(gpus[0].Vendor).To(Equal(VendorNVIDIA))
			Expect(gpus[1].Vendor).To(Equal(VendorAMD))
		})

		It("skips display connectors and cards with an unrecognised vendor", func() {
			// An ASPEED BMC display adapter sits alongside the Arc on the
			// machine in #10941; it must not be reported as a GPU.
			writeSysfsCard(root, "card0", "0000:04:00.0", map[string]string{"vendor": "0x1a03\n"})
			writeSysfsCard(root, "card1", "0000:03:00.0", map[string]string{"vendor": "0x8086\n"})
			writeSysfsCard(root, "card1-DP-1", "0000:03:00.1", map[string]string{"vendor": "0x8086\n"})

			gpus := scanSysfsGPUs(root)

			Expect(gpus).To(HaveLen(1))
			Expect(gpus[0].Card).To(Equal("card1"))
		})

		It("returns nothing when /sys/class/drm is absent", func() {
			Expect(scanSysfsGPUs(filepath.Join(root, "nonexistent"))).To(BeEmpty())
		})
	})

	Describe("sysfsVendorPriority", func() {
		It("detects Intel when it is the only GPU present", func() {
			writeSysfsCard(root, "card0", "0000:04:00.0", map[string]string{"vendor": "0x1a03\n"})
			writeSysfsCard(root, "card1", "0000:03:00.0", map[string]string{"vendor": "0x8086\n"})

			Expect(sysfsVendorPriority(root)).To(Equal(VendorIntel))
		})

		It("prefers a discrete NVIDIA GPU over an Intel integrated one", func() {
			writeSysfsCard(root, "card0", "0000:00:02.0", map[string]string{"vendor": "0x8086\n"})
			writeSysfsCard(root, "card1", "0000:01:00.0", map[string]string{"vendor": "0x10de\n"})

			Expect(sysfsVendorPriority(root)).To(Equal(VendorNVIDIA))
		})

		It("prefers AMD over Intel", func() {
			writeSysfsCard(root, "card0", "0000:00:02.0", map[string]string{"vendor": "0x8086\n"})
			writeSysfsCard(root, "card1", "0000:01:00.0", map[string]string{"vendor": "0x1002\n"})

			Expect(sysfsVendorPriority(root)).To(Equal(VendorAMD))
		})

		It("returns empty when no known GPU vendor is present", func() {
			writeSysfsCard(root, "card0", "0000:04:00.0", map[string]string{"vendor": "0x1a03\n"})

			Expect(sysfsVendorPriority(root)).To(BeEmpty())
		})
	})

})
