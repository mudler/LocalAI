package chv

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// attrLayer is a layer with every file kind the conversion must keep: a
// setuid file, a symlink, a hard link, a fifo, a character device and a file
// with an xattr. It also holds a kernel when withKernel is true.
func attrLayer(withKernel bool) v1.Layer {
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
	if withKernel {
		add(&tar.Header{Name: "boot/", Typeflag: tar.TypeDir, Mode: 0o755}, "")
		add(&tar.Header{Name: "boot/vmlinux", Typeflag: tar.TypeReg, Mode: 0o644}, "not a real kernel")
	}
	Expect(tw.Close()).To(Succeed())
	b := buf.Bytes()
	l, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil })
	Expect(err).To(Succeed())
	return l
}

// writeLayout stores img in an OCI layout folder and returns its reference.
func writeLayout(img v1.Image) string {
	GinkgoHelper()
	dir := GinkgoT().TempDir()
	p, err := layout.Write(dir, empty.Index)
	Expect(err).To(Succeed())
	Expect(p.AppendImage(img)).To(Succeed())
	d, err := img.Digest()
	Expect(err).To(Succeed())
	return layoutPrefix + dir + "@" + d.String()
}

func testImage(withKernel bool) v1.Image {
	GinkgoHelper()
	img, err := mutate.AppendLayers(empty.Image, attrLayer(withKernel))
	Expect(err).To(Succeed())
	img, err = mutate.Config(img, v1.Config{Env: []string{"PATH=/usr/bin:/bin", "FROM_IMAGE=1"}})
	Expect(err).To(Succeed())
	return img
}

func debugfsOut(img, cmd string) string {
	GinkgoHelper()
	out, err := exec.Command("debugfs", "-c", "-R", cmd, img).Output()
	Expect(err).To(Succeed(), "debugfs %s", cmd)
	return string(out)
}

// tarImage is an image with one layer made of the entries fill writes.
func tarImage(fill func(tw *tar.Writer)) v1.Image {
	GinkgoHelper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	fill(tw)
	Expect(tw.Close()).To(Succeed())
	b := buf.Bytes()
	l, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil })
	Expect(err).To(Succeed())
	img, err := mutate.AppendLayers(empty.Image, l)
	Expect(err).To(Succeed())
	return img
}

func tarEntry(tw *tar.Writer, h *tar.Header, body string) {
	h.Size = int64(len(body))
	if h.Mode == 0 {
		h.Mode = 0o644
	}
	if err := tw.WriteHeader(h); err != nil {
		panic(err)
	}
	io.WriteString(tw, body)
}

// writeLayoutDir is writeLayout that also returns the folder and the digest.
func writeLayoutDir(img v1.Image) (string, v1.Hash) {
	GinkgoHelper()
	dir := GinkgoT().TempDir()
	p, err := layout.Write(dir, empty.Index)
	Expect(err).To(Succeed())
	Expect(p.AppendImage(img)).To(Succeed())
	d, err := img.Digest()
	Expect(err).To(Succeed())
	return dir, d
}

// assertNoCacheEntry checks that no folder in cache is complete: env.json is
// written last, so its absence is the mark of a refused conversion.
func assertNoCacheEntry(cache string) {
	GinkgoHelper()
	entries, _ := os.ReadDir(cache)
	for _, e := range entries {
		if e.IsDir() {
			_, err := os.Stat(filepath.Join(cache, e.Name(), "env.json"))
			Expect(err).To(HaveOccurred(), "a refused image left a complete cache entry: %s", e.Name())
		}
	}
}

var _ = Describe("image conversion", func() {
	It("keeps file kinds", func() {
		needTools("debugfs")
		b := &Backend{opts: Options{CacheDir: GinkgoT().TempDir()}}
		ref := writeLayout(testImage(true))
		gi, err := b.image(context.Background(), ref)
		Expect(err).To(Succeed())
		Expect(strings.Join(gi.Env, " ")).To(Equal("PATH=/usr/bin:/bin FROM_IMAGE=1"))
		k, _ := os.ReadFile(gi.Kernel)
		Expect(string(k)).To(Equal("not a real kernel"))
		ls := debugfsOut(gi.Root, "ls -p /opt/attrs")
		ents, _, err := parseLS([]byte(ls))
		Expect(err).To(Succeed(), ls)
		byName := map[string]lsEntry{}
		for _, e := range ents {
			byName[e.Name] = e
		}
		for name, mode := range map[string]uint32{"suid": 0o104755, "hard": 0o104755, "link": 0o120777, "fifo": 0o010600, "null": 0o020666, "xattr": 0o100644} {
			Expect(byName[name].Mode).To(Equal(mode), "%s mode = %o, want %o", name, byName[name].Mode, mode)
		}
		Expect(byName["suid"].Ino).To(Equal(byName["hard"].Ino), "the hard link is a copy")
		Expect(ls).To(ContainSubstring("/1000/1000/fifo/"), "owners lost")
		Expect(ls).To(ContainSubstring("/0/0/suid/"), "owners lost")
		Expect(debugfsOut(gi.Root, "stat /opt/attrs/null")).To(ContainSubstring("Device major/minor number: 01:03"), "device")
		ea := debugfsOut(gi.Root, "ea_get -x /opt/attrs/xattr user.demo")
		v, _, err := parseEAGet("user.demo")([]byte(ea))
		Expect(err).To(Succeed(), "xattr = %q", ea)
		Expect(string(v)).To(Equal("hello"), "xattr = %q", ea)
		// A second call uses the cache.
		before, _ := os.Stat(gi.Root)
		gi2, err := b.image(context.Background(), ref)
		Expect(err).To(Succeed())
		after, _ := os.Stat(gi2.Root)
		Expect(before.ModTime().Equal(after.ModTime())).To(BeTrue(), "the image was converted again")
	})

	It("refuses an image without a kernel and bad references", func() {
		b := &Backend{opts: Options{CacheDir: GinkgoT().TempDir()}}
		_, err := b.image(context.Background(), writeLayout(testImage(false)))
		Expect(err).To(MatchError(ContainSubstring("/boot/vmlinux")), "no kernel")
		for _, ref := range []string{"debian:trixie-slim", "example.com/guest:latest", layoutPrefix + "/some/dir", ""} {
			_, err := b.image(context.Background(), ref)
			Expect(err).To(HaveOccurred(), "%q was accepted", ref)
		}
		entries, _ := os.ReadDir(b.opts.CacheDir)
		for _, e := range entries {
			if e.IsDir() {
				_, err := os.Stat(filepath.Join(b.opts.CacheDir, e.Name(), "env.json"))
				Expect(err).To(HaveOccurred(), "a failed conversion left a complete cache entry: %s", e.Name())
			}
		}
	})

	// A hostile layer must not make the host write an unbounded kernel.
	It("refuses a huge kernel", func() {
		old := maxKernelBytes
		maxKernelBytes = 8
		DeferCleanup(func() { maxKernelBytes = old })
		b := &Backend{opts: Options{CacheDir: GinkgoT().TempDir()}}
		_, err := b.image(context.Background(), writeLayout(testImage(true)))
		Expect(err).To(MatchError(ContainSubstring("larger than 8 bytes")), "a kernel over the limit")
		entries, _ := os.ReadDir(b.opts.CacheDir)
		for _, e := range entries {
			_, err := os.Stat(filepath.Join(b.opts.CacheDir, e.Name(), "env.json"))
			Expect(err).To(HaveOccurred(), "a refused kernel left a complete cache entry: %s", e.Name())
		}
	})

	It("refuses a layout with a swapped manifest", func() {
		dir, d := writeLayoutDir(testImage(true))
		other := testImage(false)
		p, err := layout.FromPath(dir)
		Expect(err).To(Succeed())
		Expect(p.WriteImage(other)).To(Succeed()) // the other image's blobs, so only the digest is wrong
		raw, err := other.RawManifest()
		Expect(err).To(Succeed())
		Expect(os.WriteFile(blobPath(dir, d), raw, 0o600)).To(Succeed())
		b := &Backend{opts: Options{CacheDir: GinkgoT().TempDir()}}
		_, err = b.image(context.Background(), layoutPrefix+dir+"@"+d.String())
		Expect(err).To(MatchError(ContainSubstring("digest mismatch")), "a swapped manifest")
		assertNoCacheEntry(b.opts.CacheDir)
	})

	It("refuses a layout with a swapped layer", func() {
		img := testImage(true)
		m, err := img.Manifest()
		Expect(err).To(Succeed())
		dir, d := writeLayoutDir(img)
		Expect(os.WriteFile(blobPath(dir, m.Layers[0].Digest), []byte("swapped layer"), 0o600)).To(Succeed())
		b := &Backend{opts: Options{CacheDir: GinkgoT().TempDir()}}
		_, err = b.image(context.Background(), layoutPrefix+dir+"@"+d.String())
		Expect(err).To(MatchError(ContainSubstring("digest mismatch")), "a swapped layer")
		assertNoCacheEntry(b.opts.CacheDir)
	})

	// Hostile tar entries must not be converted or written outside the image.
	DescribeTable("refuses hostile tar paths",
		func(fill func(tw *tar.Writer)) {
			b := &Backend{opts: Options{CacheDir: GinkgoT().TempDir()}}
			_, err := b.image(context.Background(), writeLayout(tarImage(fill)))
			Expect(err).To(HaveOccurred(), "the hostile image was converted")
			assertNoCacheEntry(b.opts.CacheDir)
		},
		Entry("dot-dot-escape", func(tw *tar.Writer) {
			tarEntry(tw, &tar.Header{Name: "../evil", Typeflag: tar.TypeReg}, "x")
			tarEntry(tw, &tar.Header{Name: "boot/vmlinux", Typeflag: tar.TypeReg}, "K")
		}),
		Entry("absolute-escape", func(tw *tar.Writer) {
			tarEntry(tw, &tar.Header{Name: "/abs/evil", Typeflag: tar.TypeReg}, "x")
			tarEntry(tw, &tar.Header{Name: "boot/vmlinux", Typeflag: tar.TypeReg}, "K")
		}),
		Entry("kernel-symlink", func(tw *tar.Writer) {
			tarEntry(tw, &tar.Header{Name: "boot/vmlinux", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}, "")
		}),
	)

	// Two converters of one digest share one conversion and one cache folder.
	It("shares one conversion between concurrent converters of one digest", func() {
		b := &Backend{opts: Options{CacheDir: GinkgoT().TempDir()}}
		ref := writeLayout(testImage(true))
		var wg sync.WaitGroup
		roots := make([]string, 2)
		errs := make([]error, 2)
		for i := range roots {
			wg.Add(1)
			go func() {
				defer wg.Done()
				gi, err := b.image(context.Background(), ref)
				roots[i], errs[i] = gi.Root, err
			}()
		}
		wg.Wait()
		for i := range roots {
			Expect(errs[i]).To(Succeed(), "converter %d", i)
		}
		Expect(roots[0]).To(Equal(roots[1]), "two folders: %v", roots)
		entries, _ := os.ReadDir(b.opts.CacheDir)
		var dirs []string
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, e.Name())
			}
		}
		Expect(dirs).To(HaveLen(1), "cache folders: %v", dirs)
		folder, _ := os.ReadDir(filepath.Join(b.opts.CacheDir, dirs[0]))
		for _, e := range folder {
			Expect(strings.HasSuffix(e.Name(), ".tmp")).To(BeFalse(), "a temporary file was left: %s", e.Name())
		}
	})
})
