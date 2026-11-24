package ext4image

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ext4ro"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ociimage"
)

func TestExt4image(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sandbox guest image conversion")
}

const kernelBody = "not a real kernel"

// guestLayer is a layer with every file kind the conversion must keep, and a
// kernel at boot/vmlinux.
func guestLayer(withKernel bool) v1.Layer {
	GinkgoHelper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	add := func(h *tar.Header, body string) {
		h.Size = int64(len(body))
		Expect(tw.WriteHeader(h)).To(Succeed())
		_, err := io.WriteString(tw, body)
		Expect(err).To(Succeed())
	}
	add(&tar.Header{Name: "opt/", Typeflag: tar.TypeDir, Mode: 0o755}, "")
	add(&tar.Header{Name: "opt/attrs/", Typeflag: tar.TypeDir, Mode: 0o755}, "")
	add(&tar.Header{Name: "opt/attrs/suid", Typeflag: tar.TypeReg, Mode: 0o4755}, "#!/bin/sh\n")
	add(&tar.Header{Name: "opt/attrs/link", Typeflag: tar.TypeSymlink, Linkname: "suid", Mode: 0o777}, "")
	add(&tar.Header{Name: "opt/attrs/hard", Typeflag: tar.TypeLink, Linkname: "opt/attrs/suid"}, "")
	add(&tar.Header{Name: "opt/attrs/fifo", Typeflag: tar.TypeFifo, Mode: 0o600, Uid: 1000, Gid: 1000}, "")
	add(&tar.Header{Name: "opt/attrs/null", Typeflag: tar.TypeChar, Mode: 0o666, Devmajor: 1, Devminor: 3}, "")
	add(&tar.Header{Name: "opt/attrs/xattr", Typeflag: tar.TypeReg, Mode: 0o644, PAXRecords: map[string]string{"SCHILY.xattr.user.demo": "hello"}}, "x\n")
	add(&tar.Header{Name: "opt/attrs/long-" + strings.Repeat("n", 200), Typeflag: tar.TypeReg, Mode: 0o644}, "long\n")
	if withKernel {
		add(&tar.Header{Name: "boot/", Typeflag: tar.TypeDir, Mode: 0o755}, "")
		add(&tar.Header{Name: "boot/vmlinux", Typeflag: tar.TypeReg, Mode: 0o644}, kernelBody)
	}
	Expect(tw.Close()).To(Succeed())
	b := buf.Bytes()
	l, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil })
	Expect(err).To(Succeed())
	return l
}

func guestImage(withKernel bool) v1.Image {
	GinkgoHelper()
	img, err := mutate.AppendLayers(empty.Image, guestLayer(withKernel))
	Expect(err).To(Succeed())
	img, err = mutate.Config(img, v1.Config{Env: []string{"PATH=/usr/bin:/bin", "FROM_IMAGE=1"}})
	Expect(err).To(Succeed())
	return img
}

func layoutRef(img v1.Image) string {
	GinkgoHelper()
	dir := GinkgoT().TempDir()
	p, err := layout.Write(dir, empty.Index)
	Expect(err).To(Succeed())
	Expect(p.AppendImage(img)).To(Succeed())
	d, err := img.Digest()
	Expect(err).To(Succeed())
	return ociimage.LayoutPrefix + dir + "@" + d.String()
}

var _ = Describe("guest image conversion", func() {
	// The conversion is pure Go, so it needs no tool of the host. The reader of
	// the upper image is pure Go too: it reads the root file system that the
	// conversion made, which proves both on any system.
	It("makes a root file system that keeps every file kind, and copies the kernel", func() {
		dir := GinkgoT().TempDir()
		Expect(Convert(guestImage(true), dir, 0)).To(Succeed())
		k, err := os.ReadFile(filepath.Join(dir, "vmlinux"))
		Expect(err).To(Succeed())
		Expect(string(k)).To(Equal(kernelBody))

		f, err := os.Open(filepath.Join(dir, "root.ext4"))
		Expect(err).To(Succeed())
		DeferCleanup(func() { _ = f.Close() })
		st, err := f.Stat()
		Expect(err).To(Succeed())
		fsys, err := ext4ro.Open(f, st.Size())
		Expect(err).To(Succeed())

		byName := func(ino uint64) map[string]ext4ro.DirEntry {
			ents, err := fsys.ReadDir(ino, 1000)
			Expect(err).To(Succeed())
			m := map[string]ext4ro.DirEntry{}
			for _, e := range ents {
				m[e.Name] = e
			}
			return m
		}
		walk := func(path ...string) uint64 {
			ino := uint64(2)
			for _, p := range path {
				e, ok := byName(ino)[p]
				Expect(ok).To(BeTrue(), "no %s", p)
				ino = e.Ino
			}
			return ino
		}
		attrs := byName(walk("opt", "attrs"))
		modes := map[string]uint16{"suid": 0o104755, "hard": 0o104755, "link": 0o120777, "fifo": 0o010600, "null": 0o020666, "xattr": 0o100644}
		for name, mode := range modes {
			in, err := fsys.Stat(attrs[name].Ino)
			Expect(err).To(Succeed())
			Expect(in.Mode).To(Equal(mode), "%s mode = %o, want %o", name, in.Mode, mode)
		}
		Expect(attrs["suid"].Ino).To(Equal(attrs["hard"].Ino), "the hard link is a copy")
		fifo, _ := fsys.Stat(attrs["fifo"].Ino)
		Expect([2]uint32{fifo.UID, fifo.GID}).To(Equal([2]uint32{1000, 1000}), "owner of the fifo")
		suid, _ := fsys.Stat(attrs["suid"].Ino)
		Expect([2]uint32{suid.UID, suid.GID}).To(Equal([2]uint32{0, 0}), "owner of the setuid file")
		null, _ := fsys.Stat(attrs["null"].Ino)
		Expect(null.Rdev).To(Equal(uint32(1<<20|3)), "device numbers")
		target, err := fsys.Readlink(attrs["link"].Ino)
		Expect(err).To(Succeed())
		Expect(target).To(Equal("suid"))
		v, ok, err := fsys.Xattr(attrs["xattr"].Ino, "user.demo")
		Expect(err).To(Succeed())
		Expect(ok).To(BeTrue())
		Expect(string(v)).To(Equal("hello"))
		Expect(attrs).To(HaveKey("long-" + strings.Repeat("n", 200)))
		body, err := fsys.Open(attrs["suid"].Ino)
		Expect(err).To(Succeed())
		got, err := io.ReadAll(io.NewSectionReader(body, 0, suid.Size))
		Expect(err).To(Succeed())
		Expect(string(got)).To(Equal("#!/bin/sh\n"))
	})

	It("refuses an image without a kernel, and a kernel over the limit", func() {
		Expect(Convert(guestImage(false), GinkgoT().TempDir(), 0)).To(MatchError(ContainSubstring("no /boot/vmlinux")))
		Expect(Convert(guestImage(true), GinkgoT().TempDir(), 8)).To(MatchError(ContainSubstring("larger than 8 bytes")))
	})

	It("converts an image once for the cache and returns its environment", func() {
		cache := GinkgoT().TempDir()
		ref := layoutRef(guestImage(true))
		img, err := Load(context.Background(), cache, ref, 0)
		Expect(err).To(Succeed())
		Expect(img.Ref).To(Equal(ref))
		Expect(strings.Join(img.Env, " ")).To(Equal("PATH=/usr/bin:/bin FROM_IMAGE=1"))
		before, err := os.Stat(img.Root)
		Expect(err).To(Succeed())
		again, err := Load(context.Background(), cache, ref, 0)
		Expect(err).To(Succeed())
		after, _ := os.Stat(again.Root)
		Expect(after.ModTime().Equal(before.ModTime())).To(BeTrue(), "the image was converted again")
		Expect(ociimage.Cached(context.Background(), cache, ref)).To(BeTrue())
	})

	It("refuses a reference without a digest", func() {
		_, err := Load(context.Background(), GinkgoT().TempDir(), "debian:trixie-slim", 0)
		Expect(err).To(HaveOccurred())
	})
})
