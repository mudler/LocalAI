package sandbox

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LookPath", func() {
	It("ignores the process PATH", func() {
		GinkgoT().Setenv("PATH", "")
		p, err := LookPath("sh")
		Expect(err).NotTo(HaveOccurred(), "LookPath(sh) with no PATH")
		Expect(filepath.IsAbs(p)).To(BeTrue(), "path %q is not absolute", p)
		_, err = LookPath("no-such-program-for-the-test")
		Expect(err).To(HaveOccurred(), "a missing program was found")
	})

	It("searches the extra directories and refuses plain files", func() {
		GinkgoT().Setenv("PATH", "")
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "tool"), []byte("#!/bin/sh\n"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "data"), []byte("x"), 0o644)).To(Succeed())
		p, err := LookPath("tool", dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(Equal(filepath.Join(dir, "tool")))
		_, err = LookPath("data", dir)
		Expect(err).To(HaveOccurred(), "a file without the executable bit was found")
		p, err = LookPath(filepath.Join(dir, "tool"))
		Expect(err).NotTo(HaveOccurred(), "a path with a slash")
		Expect(p).To(Equal(filepath.Join(dir, "tool")))
	})
})
