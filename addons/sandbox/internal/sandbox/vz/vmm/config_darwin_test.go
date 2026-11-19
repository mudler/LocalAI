//go:build darwin && arm64

package vmm

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The guest of a sandbox must have no network device: the status of the
// backend reports it as isolated by the name of the backend, so this test is
// what ties that claim to the configuration of the machine. It builds the
// configuration without starting a VM. The framework needs the virtualization
// entitlement to validate it, so the test skips on an unsigned binary.
var _ = Describe("the machine configuration", func() {
	It("has no network device and only the devices the backend needs", func() {
		dir := GinkgoT().TempDir()
		file := func(name string, size int64) string {
			p := filepath.Join(dir, name)
			f, err := os.Create(p)
			Expect(err).To(Succeed())
			Expect(f.Truncate(size)).To(Succeed())
			Expect(f.Close()).To(Succeed())
			return p
		}
		lower := filepath.Join(dir, "lower")
		Expect(os.Mkdir(lower, 0o700)).To(Succeed())
		cfg, err := buildConfig(Config{
			Kernel: file("vmlinux", 4096), Cmdline: "console=hvc0", Root: file("root.ext4", 1<<20), Upper: file("upper.ext4", 1<<20),
			Lower: lower, Console: filepath.Join(dir, "console.log"), CPUs: 1, MemoryBytes: 256 << 20,
		})
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "entitlement") {
			Skip("the test binary is not signed with the virtualization entitlement: " + err.Error())
		}
		Expect(err).To(Succeed())
		Expect(cfg.NetworkDevices()).To(BeEmpty(), "the machine has a network device")
		// Two disks, one socket device, one serial port, one share.
		Expect(cfg.StorageDevices()).To(HaveLen(2))
		Expect(cfg.SocketDevices()).To(HaveLen(1))
		Expect(cfg.SerialPorts()).To(HaveLen(1))
		Expect(cfg.DirectorySharingDevices()).To(HaveLen(1))
	})
})
