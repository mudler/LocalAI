//go:build linux

package gvisor

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ociimage"
)

type entry struct {
	h    tar.Header
	body string
}

func tarOf(entries ...entry) *bytes.Reader {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		e.h.Size = int64(len(e.body))
		if e.h.Typeflag != tar.TypeReg {
			e.h.Size = 0
		}
		Expect(tw.WriteHeader(&e.h)).To(Succeed())
		if e.h.Typeflag == tar.TypeReg {
			_, err := io.WriteString(tw, e.body)
			Expect(err).ToNot(HaveOccurred())
		}
	}
	Expect(tw.Close()).To(Succeed())
	return bytes.NewReader(buf.Bytes())
}

var _ = Describe("unpack", func() {
	It("keeps what the sandbox needs", func() {
		dir := GinkgoT().TempDir()
		// Registered first: the image has a read-only folder, which RemoveAll of
		// the plain temp dir could not empty if a later step failed.
		DeferCleanup(func() { _ = removeAll(dir) })
		err := unpack(tarOf(
			entry{h: tar.Header{Name: "./", Typeflag: tar.TypeDir, Mode: 0o755}},
			entry{h: tar.Header{Name: "opt/", Typeflag: tar.TypeDir, Mode: 0o555}},
			entry{h: tar.Header{Name: "opt/suid", Typeflag: tar.TypeReg, Mode: 0o4755, Uid: 12345, Gid: 12345}, body: "#!/bin/sh\n"},
			entry{h: tar.Header{Name: "opt/sgid", Typeflag: tar.TypeReg, Mode: 0o2755}, body: "x"},
			entry{h: tar.Header{Name: "opt/ro", Typeflag: tar.TypeReg, Mode: 0o444}, body: "ro"},
			entry{h: tar.Header{Name: "opt/link", Typeflag: tar.TypeSymlink, Linkname: "suid", Mode: 0o777}},
			entry{h: tar.Header{Name: "opt/abs-link", Typeflag: tar.TypeSymlink, Linkname: "/usr/bin/env", Mode: 0o777}},
			entry{h: tar.Header{Name: "opt/hard", Typeflag: tar.TypeLink, Linkname: "opt/ro"}},
			entry{h: tar.Header{Name: "opt/fifo", Typeflag: tar.TypeFifo, Mode: 0o600}},
			entry{h: tar.Header{Name: "opt/null", Typeflag: tar.TypeChar, Mode: 0o666, Devmajor: 1, Devminor: 3}},
			entry{h: tar.Header{Name: "tmp/", Typeflag: tar.TypeDir, Mode: 0o1777}},
			entry{h: tar.Header{Name: "late/dir/file", Typeflag: tar.TypeReg, Mode: 0o644}, body: "parent made on the way"},
			entry{h: tar.Header{Name: "mnt/", Typeflag: tar.TypeDir, Mode: 0o755}},
		), dir)
		Expect(err).ToNot(HaveOccurred())
		root := filepath.Join(dir, "rootfs")
		mode := func(p string) os.FileMode {
			st, err := os.Lstat(filepath.Join(root, p))
			Expect(err).ToNot(HaveOccurred())
			return st.Mode()
		}
		m := mode("opt/suid")
		Expect(m&(os.ModeSetuid|os.ModeSetgid)).To(BeZero(), "suid file mode = %v", m)
		Expect(m.Perm()).To(Equal(os.FileMode(0o755)), "suid file mode = %v", m)
		m = mode("opt/sgid")
		Expect(m&(os.ModeSetuid|os.ModeSetgid)).To(BeZero(), "sgid file mode = %v", m)
		m = mode("opt/ro")
		Expect(m.Perm()).To(Equal(os.FileMode(0o444)), "ro mode = %v", m)
		m = mode("opt")
		Expect(m.Perm()).To(Equal(os.FileMode(0o555)), "a read-only folder has mode %v", m)
		m = mode("tmp")
		Expect(m&os.ModeSticky).ToNot(BeZero(), "tmp mode = %v", m)
		Expect(m.Perm()).To(Equal(os.FileMode(0o777)), "tmp mode = %v", m)
		target, _ := os.Readlink(filepath.Join(root, "opt/link"))
		Expect(target).To(Equal("suid"), "link")
		target, _ = os.Readlink(filepath.Join(root, "opt/abs-link"))
		Expect(target).To(Equal("/usr/bin/env"), "absolute link")
		a, _ := os.Stat(filepath.Join(root, "opt/ro"))
		b, _ := os.Stat(filepath.Join(root, "opt/hard"))
		Expect(os.SameFile(a, b)).To(BeTrue(), "the hard link is a copy")
		for _, skipped := range []string{"opt/fifo", "opt/null"} {
			_, err := os.Lstat(filepath.Join(root, skipped))
			Expect(os.IsNotExist(err)).To(BeTrue(), "%s was not skipped: %v", skipped, err)
		}
		got, _ := os.ReadFile(filepath.Join(root, "late/dir/file"))
		Expect(string(got)).To(Equal("parent made on the way"), "late file")
		for _, d := range []string{"mnt/lower", "mnt/upper", "mnt/ctl"} {
			st, err := os.Stat(filepath.Join(root, d))
			Expect(err).ToNot(HaveOccurred(), "mount point %s", d)
			Expect(st.IsDir()).To(BeTrue(), "mount point %s", d)
		}
		// Every file belongs to the user who unpacked it, whatever the tar says.
		for _, p := range []string{"opt/ro", "opt/suid"} {
			st, _ := os.Stat(filepath.Join(root, p))
			uid := st.Sys().(*syscall.Stat_t).Uid
			Expect(int(uid)).To(Equal(os.Getuid()), "%s belongs to uid %d", p, uid)
		}
	})

	// outside is the absolute path of a folder next to the cache.
	DescribeTable("refuses hostile entries",
		func(mk func(outside string) []entry) {
			parent := GinkgoT().TempDir()
			DeferCleanup(func() { _ = removeAll(parent) })
			outside := filepath.Join(parent, "outside")
			Expect(os.Mkdir(outside, 0o755)).To(Succeed())
			sentinel := filepath.Join(outside, "sentinel")
			Expect(os.WriteFile(sentinel, []byte("keep"), 0o644)).To(Succeed())
			dir := filepath.Join(parent, "cache")
			Expect(os.Mkdir(dir, 0o755)).To(Succeed())
			Expect(unpack(tarOf(mk(outside)...), dir)).ToNot(Succeed(), "the hostile image was unpacked")
			// Nothing was written outside the cache folder.
			ents, _ := os.ReadDir(parent)
			Expect(ents).To(HaveLen(2), "the folder next to the cache changed: %v", ents)
			ents, _ = os.ReadDir(outside)
			Expect(ents).To(HaveLen(1), "something was written outside the folder: %v", ents)
			Expect(ents[0].Name()).To(Equal("sentinel"), "something was written outside the folder: %v", ents)
			got, _ := os.ReadFile(sentinel)
			Expect(string(got)).To(Equal("keep"), "sentinel")
			st, err := os.Stat(sentinel)
			Expect(err).ToNot(HaveOccurred())
			Expect(st.Sys().(*syscall.Stat_t).Nlink).To(BeEquivalentTo(1), "the sentinel got a hard link")
			// A failed unpack leaves no folder behind.
			_, err = os.Lstat(filepath.Join(dir, "rootfs"))
			Expect(os.IsNotExist(err)).To(BeTrue(), "the failed unpack left rootfs: %v", err)
		},
		Entry("absolute name", func(outside string) []entry {
			return []entry{{h: tar.Header{Name: outside + "/evil", Typeflag: tar.TypeReg, Mode: 0o644}, body: "x"}}
		}),
		Entry("dot dot name", func(string) []entry {
			return []entry{{h: tar.Header{Name: "../evil", Typeflag: tar.TypeReg, Mode: 0o644}, body: "x"}}
		}),
		Entry("write through a symlink", func(string) []entry {
			return []entry{
				{h: tar.Header{Name: "esc", Typeflag: tar.TypeSymlink, Linkname: "../outside"}},
				{h: tar.Header{Name: "esc/evil", Typeflag: tar.TypeReg, Mode: 0o644}, body: "x"},
			}
		}),
		Entry("write through an absolute symlink", func(outside string) []entry {
			return []entry{
				{h: tar.Header{Name: "esc", Typeflag: tar.TypeSymlink, Linkname: outside}},
				{h: tar.Header{Name: "esc/evil", Typeflag: tar.TypeReg, Mode: 0o644}, body: "x"},
			}
		}),
		Entry("hard link out of the folder", func(string) []entry {
			return []entry{{h: tar.Header{Name: "h", Typeflag: tar.TypeLink, Linkname: "../outside/sentinel"}}}
		}),
		Entry("hard link through a symlinked folder", func(string) []entry {
			return []entry{
				{h: tar.Header{Name: "esc", Typeflag: tar.TypeSymlink, Linkname: "../outside"}},
				{h: tar.Header{Name: "h", Typeflag: tar.TypeLink, Linkname: "esc/sentinel"}},
			}
		}),
		Entry("folder through a symlink", func(string) []entry {
			return []entry{
				{h: tar.Header{Name: "esc", Typeflag: tar.TypeSymlink, Linkname: "../outside"}},
				{h: tar.Header{Name: "esc/sub/", Typeflag: tar.TypeDir, Mode: 0o755}},
			}
		}),
		Entry("folder replacing a file", func(string) []entry {
			return []entry{
				{h: tar.Header{Name: "f", Typeflag: tar.TypeReg, Mode: 0o644}, body: "x"},
				{h: tar.Header{Name: "f/", Typeflag: tar.TypeDir, Mode: 0o755}},
			}
		}),
		Entry("very long name", func(string) []entry {
			return []entry{{h: tar.Header{Name: strings.Repeat("a", 300), Typeflag: tar.TypeReg, Mode: 0o644}, body: "x"}}
		}),
	)

	It("lets a file replace a folder", func() {
		dir := GinkgoT().TempDir()
		DeferCleanup(func() { _ = removeAll(dir) })
		err := unpack(tarOf(
			entry{h: tar.Header{Name: "d/", Typeflag: tar.TypeDir, Mode: 0o755}},
			entry{h: tar.Header{Name: "d/inner", Typeflag: tar.TypeReg, Mode: 0o644}, body: "x"},
			entry{h: tar.Header{Name: "d", Typeflag: tar.TypeReg, Mode: 0o644}, body: "now a file"},
		), dir)
		Expect(err).ToNot(HaveOccurred())
		got, _ := os.ReadFile(filepath.Join(dir, "rootfs", "d"))
		Expect(string(got)).To(Equal("now a file"))
	})

	// A folder entry that a later entry replaces by a symlink: the final
	// folder modes must not follow the link.
	Describe("does not chmod through a replaced folder", func() {
		It("handles a link to a file in the root", func() {
			dir := GinkgoT().TempDir()
			DeferCleanup(func() { _ = removeAll(dir) })
			err := unpack(tarOf(
				entry{h: tar.Header{Name: "target", Typeflag: tar.TypeReg, Mode: 0o644}, body: "x"},
				entry{h: tar.Header{Name: "d/", Typeflag: tar.TypeDir, Mode: 0o500}},
				entry{h: tar.Header{Name: "d", Typeflag: tar.TypeSymlink, Linkname: "target"}},
			), dir)
			Expect(err).ToNot(HaveOccurred())
			st, err := os.Stat(filepath.Join(dir, "rootfs", "target"))
			Expect(err).ToNot(HaveOccurred())
			Expect(st.Mode().Perm()).To(Equal(os.FileMode(0o644)), "target = %v", st)
		})
		It("handles a dangling link", func() {
			dir := GinkgoT().TempDir()
			DeferCleanup(func() { _ = removeAll(dir) })
			err := unpack(tarOf(
				entry{h: tar.Header{Name: "d/", Typeflag: tar.TypeDir, Mode: 0o500}},
				entry{h: tar.Header{Name: "d", Typeflag: tar.TypeSymlink, Linkname: "missing"}},
			), dir)
			Expect(err).ToNot(HaveOccurred())
			target, _ := os.Readlink(filepath.Join(dir, "rootfs", "d"))
			Expect(target).To(Equal("missing"), "d ->")
		})
	})

	It("leaves a removable folder after a failed unpack", func() {
		parent := GinkgoT().TempDir()
		DeferCleanup(func() { _ = ociimage.RemoveTree(parent) })
		dir := filepath.Join(parent, "cache")
		Expect(os.Mkdir(dir, 0o755)).To(Succeed())
		// "mnt" is a file, so the step after the entries fails, with a
		// read-only folder already unpacked.
		err := unpack(tarOf(
			entry{h: tar.Header{Name: "opt/", Typeflag: tar.TypeDir, Mode: 0o555}},
			entry{h: tar.Header{Name: "opt/f", Typeflag: tar.TypeReg, Mode: 0o444}, body: "x"},
			entry{h: tar.Header{Name: "mnt", Typeflag: tar.TypeReg, Mode: 0o644}, body: "x"},
		), dir)
		Expect(err).To(HaveOccurred(), "unpack worked")
		Expect(os.RemoveAll(dir)).To(Succeed(), "the cache folder cannot be removed")
	})

	It("lets RemoveTree remove an unpacked read-only folder", func() {
		parent := GinkgoT().TempDir()
		DeferCleanup(func() { _ = ociimage.RemoveTree(parent) })
		dir := filepath.Join(parent, "cache")
		Expect(os.Mkdir(dir, 0o755)).To(Succeed())
		err := unpack(tarOf(
			entry{h: tar.Header{Name: "opt/", Typeflag: tar.TypeDir, Mode: 0o555}},
			entry{h: tar.Header{Name: "opt/deep/", Typeflag: tar.TypeDir, Mode: 0o000}},
		), dir)
		Expect(err).ToNot(HaveOccurred())
		Expect(ociimage.RemoveTree(dir)).To(Succeed())
		_, err = os.Lstat(dir)
		Expect(os.IsNotExist(err)).To(BeTrue(), "dir still there: %v", err)
	})

	It("stops at the entry limit", func() {
		old := maxUnpackEntries
		maxUnpackEntries = 3
		DeferCleanup(func() { maxUnpackEntries = old })
		var es []entry
		for _, n := range []string{"a", "b", "c", "d"} {
			es = append(es, entry{h: tar.Header{Name: n, Typeflag: tar.TypeReg, Mode: 0o644}})
		}
		err := unpack(tarOf(es...), GinkgoT().TempDir())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("more than 3 entries"))
		Expect(unpack(tarOf(es[:3]...), GinkgoT().TempDir())).To(Succeed(), "3 entries refused")
	})

	It("stops at the size limit", func() {
		old := maxUnpackBytes
		maxUnpackBytes = 8
		DeferCleanup(func() { maxUnpackBytes = old })
		err := unpack(tarOf(entry{h: tar.Header{Name: "big", Typeflag: tar.TypeReg, Mode: 0o644}, body: strings.Repeat("x", 100)}), GinkgoT().TempDir())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("larger than 8 bytes"))
	})
})

var _ = Describe("image", func() {
	It("needs a reference", func() {
		b := &Backend{opts: Options{CacheDir: GinkgoT().TempDir()}}
		for _, ref := range []string{"", "debian:trixie-slim", "oci-layout:/some/dir"} {
			_, err := b.image(context.Background(), ref)
			Expect(err).To(HaveOccurred(), "%q was accepted", ref)
		}
	})
})
