package modeladmin

import (
	"os"
	"path/filepath"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("writeFileAtomic", func() {
	It("writes the file with the requested content and leaves no temp leftovers", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "model.yaml")
		Expect(writeFileAtomic(path, []byte("name: x\n"), 0644)).To(Succeed())

		got, err := os.ReadFile(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(got)).To(Equal("name: x\n"))

		entries, err := os.ReadDir(dir)
		Expect(err).ToNot(HaveOccurred())
		Expect(entries).To(HaveLen(1), "directory should contain only the destination file")
	})

	It("preserves the original file when the rename fails", func() {
		if runtime.GOOS == "windows" {
			Skip("chmod-based read-only directory trick is POSIX-specific")
		}
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "model.yaml")
		Expect(os.WriteFile(path, []byte("original\n"), 0644)).To(Succeed())

		// Make the directory read-only so os.CreateTemp fails — easiest way to
		// force a write error mid-helper without invasive mocking.
		Expect(os.Chmod(dir, 0o500)).To(Succeed())
		DeferCleanup(func() { _ = os.Chmod(dir, 0o700) })

		Expect(writeFileAtomic(path, []byte("new\n"), 0644)).ToNot(Succeed())

		// Restore for the read-back below.
		Expect(os.Chmod(dir, 0o700)).To(Succeed())
		got, err := os.ReadFile(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(got)).To(Equal("original\n"), "original file must not be clobbered")
	})
})
