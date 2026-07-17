package oci

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// buildTar assembles an in-memory tar carrying a directory, a regular file and
// a relative symlink pointing at that file, mirroring the layout of a backend
// image (e.g. libcublas.so -> libcublas.so.12).
func buildTar() []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	Expect(tw.WriteHeader(&tar.Header{
		Name:     "lib/",
		Typeflag: tar.TypeDir,
		Mode:     0755,
	})).To(Succeed())

	content := []byte("real library bytes")
	Expect(tw.WriteHeader(&tar.Header{
		Name:     "lib/libcublas.so.12",
		Typeflag: tar.TypeReg,
		Mode:     0644,
		Size:     int64(len(content)),
	})).To(Succeed())
	_, err := tw.Write(content)
	Expect(err).NotTo(HaveOccurred())

	Expect(tw.WriteHeader(&tar.Header{
		Name:     "lib/libcublas.so",
		Typeflag: tar.TypeSymlink,
		Linkname: "libcublas.so.12",
		Mode:     0777,
	})).To(Succeed())

	Expect(tw.Close()).To(Succeed())
	return buf.Bytes()
}

var _ = Describe("Tar extraction fallback for link-less filesystems", func() {
	Describe("isLinkUnsupportedError", func() {
		It("recognises filesystem link-unsupported errors", func() {
			Expect(isLinkUnsupportedError(syscall.ENOTSUP)).To(BeTrue())
			Expect(isLinkUnsupportedError(syscall.EOPNOTSUPP)).To(BeTrue())
			Expect(isLinkUnsupportedError(syscall.EPERM)).To(BeTrue())
			Expect(isLinkUnsupportedError(&os.LinkError{
				Op:  "symlink",
				Old: "libcublas.so.12",
				New: "/backends/lib/libcublas.so",
				Err: syscall.ENOTSUP,
			})).To(BeTrue())
		})

		It("does not misclassify unrelated errors", func() {
			Expect(isLinkUnsupportedError(os.ErrNotExist)).To(BeFalse())
			Expect(isLinkUnsupportedError(syscall.ENOSPC)).To(BeFalse())
		})
	})

	Describe("safeJoin", func() {
		It("keeps entries inside the root", func() {
			root := "/tmp/extract-root"
			p, err := safeJoin(root, "lib/libcublas.so")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).To(Equal(filepath.Join(root, "lib/libcublas.so")))
		})

		It("rejects path traversal entries", func() {
			_, err := safeJoin("/tmp/extract-root", "../../etc/passwd")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("extractTarCopyingLinks", func() {
		It("preserves symlinks when the filesystem supports them", func() {
			dir := GinkgoT().TempDir()
			Expect(extractTarCopyingLinks(bytes.NewReader(buildTar()), dir)).To(Succeed())

			linkPath := filepath.Join(dir, "lib", "libcublas.so")
			fi, err := os.Lstat(linkPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(fi.Mode() & os.ModeSymlink).NotTo(BeZero())

			data, err := os.ReadFile(linkPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(Equal("real library bytes"))
		})

		It("copies the target when symlink creation is unsupported", func() {
			// Simulate a CIFS/SMB mount: symlink() reports ENOTSUP.
			origSymlink := symlink
			symlink = func(string, string) error { return syscall.ENOTSUP }
			DeferCleanup(func() { symlink = origSymlink })

			dir := GinkgoT().TempDir()
			Expect(extractTarCopyingLinks(bytes.NewReader(buildTar()), dir)).To(Succeed())

			linkPath := filepath.Join(dir, "lib", "libcublas.so")
			fi, err := os.Lstat(linkPath)
			Expect(err).NotTo(HaveOccurred())
			// The entry must now be a real, regular file (a copy), not a symlink.
			Expect(fi.Mode() & os.ModeSymlink).To(BeZero())
			Expect(fi.Mode().IsRegular()).To(BeTrue())

			data, err := os.ReadFile(linkPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(Equal("real library bytes"))
		})
	})
})
