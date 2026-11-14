// SPDX-License-Identifier: MIT
package recovery

import (
	"bytes"
	"io"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func sparseFixture(size int, offsets []int) (string, string, []byte) {
	return sparseFixtureAt(GinkgoT().TempDir(), size, offsets)
}

func sparseFixtureAt(dir string, size int, offsets []int) (string, string, []byte) {
	src, dst := filepath.Join(dir, "source"), filepath.Join(dir, "copy")
	want := make([]byte, size)
	f, err := os.Create(src)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = f.Close() })
	Expect(f.Truncate(int64(size))).To(Succeed())
	for _, offset := range offsets {
		data := bytes.Repeat([]byte{0x37, 0x00, 0xa5}, min(137, (size-offset)/3))
		_, err = f.WriteAt(data, int64(offset))
		Expect(err).NotTo(HaveOccurred())
		copy(want[offset:], data)
	}
	Expect(f.Close()).To(Succeed())
	return src, dst, want
}

func fallbackCopy(src, dst string, size int64) {
	in, err := os.Open(src)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = in.Close() })
	out, err := os.Create(dst)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = out.Close() })
	// Simulate a fallback after some extents were already copied.
	_, err = out.Write(bytes.Repeat([]byte{0xff}, 128*1024))
	Expect(err).NotTo(HaveOccurred())
	_, err = in.Seek(17, io.SeekStart)
	Expect(err).NotTo(HaveOccurred())
	Expect(copyZeroBlocks(in, out, size)).To(Succeed())
	Expect(out.Truncate(size)).To(Succeed())
	Expect(out.Close()).To(Succeed())
}

var _ = Describe("Sparse file copying", func() {
	DescribeTable("preserves contents and logical size", func(size int, offsets []int) {
		for _, fallback := range []bool{false, true} {
			src, dst, want := sparseFixture(size, offsets)
			if fallback {
				fallbackCopy(src, dst, int64(size))
			} else {
				Expect(copyPlain(src, dst)).To(Succeed())
			}
			got, err := os.ReadFile(dst)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
		}
	},
		Entry("a leading hole", 8*1024*1024+137, []int{8 * 1024 * 1024}),
		Entry("a middle hole", 8*1024*1024+137, []int{0, 8 * 1024 * 1024}),
		Entry("a trailing hole", 8*1024*1024+137, []int{0}),
		Entry("an entirely sparse file", 8*1024*1024+137, []int{}),
		Entry("an empty file", 0, []int{}),
		Entry("a short data file", 411, []int{0}),
		Entry("data across a buffer boundary", 128*1024+137, []int{32*1024 - 17, 64*1024 - 1}),
	)

	It("does not overwrite an existing destination", func() {
		src, dst, _ := sparseFixture(411, []int{0})
		Expect(os.WriteFile(dst, []byte("keep"), 0o600)).To(Succeed())
		Expect(copyPlain(src, dst)).To(MatchError(os.ErrExist))
		got, err := os.ReadFile(dst)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(got)).To(Equal("keep"))
	})

	It("removes a partial destination on a read error", func() {
		dir := GinkgoT().TempDir()
		dst := filepath.Join(dir, "copy")
		Expect(copyPlain(dir, dst)).NotTo(Succeed())
		Expect(dst).NotTo(BeAnExistingFile())
	})
})
