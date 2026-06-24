package oci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type compressedOnlyLayer struct {
	v1.Layer
	digest v1.Hash
}

func (l compressedOnlyLayer) Uncompressed() (io.ReadCloser, error) {
	return nil, errors.New("downloaded layer reopened from source")
}

func (l compressedOnlyLayer) Digest() (v1.Hash, error) { return l.digest, nil }

func buildLayer(entries ...tar.Header) v1.Layer {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, header := range entries {
		content := []byte(header.PAXRecords["content"])
		header.PAXRecords = nil
		header.Size = int64(len(content))
		Expect(tw.WriteHeader(&header)).To(Succeed())
		if len(content) != 0 {
			_, err := tw.Write(content)
			Expect(err).NotTo(HaveOccurred())
		}
	}
	Expect(tw.Close()).To(Succeed())
	Expect(zw.Close()).To(Succeed())
	layer, err := tarball.LayerFromReader(bytes.NewReader(buf.Bytes()))
	Expect(err).NotTo(HaveOccurred())
	digest, _, err := v1.SHA256(bytes.NewReader([]byte{byte(len(entries))}))
	Expect(err).NotTo(HaveOccurred())
	return compressedOnlyLayer{Layer: layer, digest: digest}
}

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

func buildChainedLinkTar() []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	content := []byte("real library bytes")
	Expect(tw.WriteHeader(&tar.Header{
		Name:     "lib/libcublas.so",
		Typeflag: tar.TypeSymlink,
		Linkname: "libcublas.so.12",
		Mode:     0777,
	})).To(Succeed())
	Expect(tw.WriteHeader(&tar.Header{
		Name:     "lib/libcublas.so.12",
		Typeflag: tar.TypeSymlink,
		Linkname: "libcublas.so.12.8.5.5",
		Mode:     0777,
	})).To(Succeed())
	Expect(tw.WriteHeader(&tar.Header{
		Name:     "lib/libcublas.so.12.8.5.5",
		Typeflag: tar.TypeReg,
		Mode:     0644,
		Size:     int64(len(content)),
	})).To(Succeed())
	_, err := tw.Write(content)
	Expect(err).NotTo(HaveOccurred())

	Expect(tw.Close()).To(Succeed())
	return buf.Bytes()
}

var _ = Describe("Tar extraction fallback for link-less filesystems", func() {
	It("downloads a layered image once and preserves whiteouts before copying links", func() {
		base := buildLayer(
			tar.Header{Name: "lib/removed.so", Mode: 0644, PAXRecords: map[string]string{"content": "removed"}},
			tar.Header{Name: "lib/libcublas.so.12", Mode: 0644, PAXRecords: map[string]string{"content": "old library"}},
		)
		top := buildLayer(
			tar.Header{Name: "lib/.wh.removed.so", Mode: 0644},
			tar.Header{Name: "lib/libcublas.so.12", Mode: 0644, PAXRecords: map[string]string{"content": "new library"}},
			tar.Header{Name: "lib/libcublas.so", Typeflag: tar.TypeSymlink, Linkname: "libcublas.so.12", Mode: 0777},
		)
		image, err := mutate.AppendLayers(empty.Image, base, top)
		Expect(err).NotTo(HaveOccurred())

		tmp := GinkgoT().TempDir()
		tarPath := filepath.Join(tmp, "rootfs.tar")
		Expect(DownloadOCIImageTar(context.Background(), image, "test/image", tarPath, nil)).To(Succeed())

		originalSymlink := symlink
		symlink = func(string, string) error { return syscall.ENOTSUP }
		DeferCleanup(func() { symlink = originalSymlink })

		destination := filepath.Join(tmp, "destination")
		Expect(os.Mkdir(destination, 0755)).To(Succeed())
		Expect(ExtractOCIImageFromTar(context.Background(), tarPath, "test/image", destination, nil)).To(Succeed())
		Expect(filepath.Join(destination, "lib", "removed.so")).NotTo(BeAnExistingFile())
		Expect(os.ReadFile(filepath.Join(destination, "lib", "libcublas.so.12"))).To(Equal([]byte("new library")))
		Expect(os.ReadFile(filepath.Join(destination, "lib", "libcublas.so"))).To(Equal([]byte("new library")))
	})

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

		It("materialises chained symlinks regardless of archive order", func() {
			origSymlink := symlink
			symlink = func(string, string) error { return syscall.ENOTSUP }
			DeferCleanup(func() { symlink = origSymlink })

			dir := GinkgoT().TempDir()
			Expect(extractTarCopyingLinks(bytes.NewReader(buildChainedLinkTar()), dir)).To(Succeed())

			Expect(os.ReadFile(filepath.Join(dir, "lib", "libcublas.so"))).To(Equal([]byte("real library bytes")))
			Expect(os.ReadFile(filepath.Join(dir, "lib", "libcublas.so.12"))).To(Equal([]byte("real library bytes")))
		})
	})
})
