package gallery_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
)

var _ = Describe("SweepSwapLeftovers", func() {
	var dir string
	build := func(name, version string) {
		GinkgoHelper()
		Expect(os.MkdirAll(filepath.Join(dir, name), 0o750)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, name, "version"), []byte(version), 0o600)).To(Succeed())
	}
	read := func(name string) string {
		GinkgoHelper()
		b, err := os.ReadFile(filepath.Join(dir, name, "version"))
		Expect(err).NotTo(HaveOccurred())
		return string(b)
	}

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
	})

	It("removes the staged build of an update that a crash stopped", func() {
		build("e2e-slow", "v1")
		Expect(os.MkdirAll(filepath.Join(dir, "e2e-slow.upgrade-tmp"), 0o750)).To(Succeed())
		build("piper.install-tmp", "v2")
		removed, restored := gallery.SweepSwapLeftovers(&system.SystemState{Backend: system.Backend{BackendsPath: dir}})
		Expect(removed).To(ConsistOf("e2e-slow.upgrade-tmp", "piper.install-tmp"))
		Expect(restored).To(BeEmpty())
		Expect(filepath.Join(dir, "e2e-slow.upgrade-tmp")).NotTo(BeAnExistingFile())
		Expect(filepath.Join(dir, "piper.install-tmp")).NotTo(BeAnExistingFile())
		Expect(read("e2e-slow")).To(Equal("v1"))
	})

	It("puts back a previous build whose backend directory is missing", func() {
		build("kokoro.backup", "v1")
		build("whisper.install-backup", "v1")
		_, restored := gallery.SweepSwapLeftovers(&system.SystemState{Backend: system.Backend{BackendsPath: dir}})
		Expect(restored).To(ConsistOf("kokoro", "whisper"))
		Expect(read("kokoro")).To(Equal("v1"))
		Expect(read("whisper")).To(Equal("v1"))
		Expect(filepath.Join(dir, "kokoro.backup")).NotTo(BeAnExistingFile())
	})

	It("never touches a previous build next to its backend, nor a failed build", func() {
		build("kokoro", "v2")
		build("kokoro.backup", "v1")
		build("piper.failed", "v2")
		removed, restored := gallery.SweepSwapLeftovers(&system.SystemState{Backend: system.Backend{BackendsPath: dir}})
		Expect(removed).To(BeEmpty())
		Expect(restored).To(BeEmpty())
		Expect(read("kokoro")).To(Equal("v2"))
		Expect(read("kokoro.backup")).To(Equal("v1"))
		Expect(read("piper.failed")).To(Equal("v2"))
	})

	It("removes only the staged builds, never a backend or a previous build", func() {
		build("piper", "v1")
		build("piper.install-tmp", "v2")
		build("piper.install-backup", "v0")
		build("kokoro", "v2")
		build("kokoro.upgrade-tmp", "v3")
		build("kokoro.backup", "v1")
		build("whisper.failed", "v2")
		build(".install-tmp", "x")
		build(".upgrade-tmp", "x")
		removed, restored := gallery.SweepSwapLeftovers(&system.SystemState{Backend: system.Backend{BackendsPath: dir}})
		Expect(removed).To(ConsistOf("piper.install-tmp", "kokoro.upgrade-tmp"))
		Expect(restored).To(BeEmpty())
		for name, version := range map[string]string{
			"piper": "v1", "piper.install-backup": "v0", "kokoro": "v2",
			"kokoro.backup": "v1", "whisper.failed": "v2",
			".install-tmp": "x", ".upgrade-tmp": "x",
		} {
			Expect(read(name)).To(Equal(version), name)
		}
	})

	It("does nothing without a backends directory", func() {
		removed, restored := gallery.SweepSwapLeftovers(&system.SystemState{Backend: system.Backend{BackendsPath: filepath.Join(dir, "missing")}})
		Expect(removed).To(BeEmpty())
		Expect(restored).To(BeEmpty())
	})
})
