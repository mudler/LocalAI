//go:build linux

// SPDX-License-Identifier: MIT
package recovery

import (
	"os"
	"path/filepath"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Sparse file allocation", func() {
	DescribeTable("keeps holes unallocated", func(offsets []int, crossFilesystem bool) {
		const size = 8*1024*1024 + 137
		for _, fallback := range []bool{false, true} {
			dir := GinkgoT().TempDir()
			if crossFilesystem {
				var err error
				dir, err = os.MkdirTemp("/dev/shm", "recovery-sparse-")
				if err != nil {
					Skip("a writable /dev/shm is needed to test cross-filesystem copies")
				}
				DeferCleanup(os.RemoveAll, dir)
			}
			src, dst, want := sparseFixtureAt(dir, size, offsets)
			if crossFilesystem {
				dst = filepath.Join(GinkgoT().TempDir(), "copy")
				srcInfo, err := os.Stat(dir)
				Expect(err).NotTo(HaveOccurred())
				dstInfo, err := os.Stat(filepath.Dir(dst))
				Expect(err).NotTo(HaveOccurred())
				if srcInfo.Sys().(*syscall.Stat_t).Dev == dstInfo.Sys().(*syscall.Stat_t).Dev {
					Skip("/dev/shm and the temporary directory are on the same filesystem")
				}
			}
			info, err := os.Stat(src)
			Expect(err).NotTo(HaveOccurred())
			if info.Sys().(*syscall.Stat_t).Blocks*512 >= size/4 {
				Skip("test filesystem does not support sparse files")
			}
			if fallback {
				fallbackCopy(src, dst, size)
			} else {
				Expect(copyPlain(src, dst)).To(Succeed())
			}
			info, err = os.Stat(dst)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Size()).To(Equal(int64(size)))
			Expect(info.Sys().(*syscall.Stat_t).Blocks * 512).To(BeNumerically("<", size/4))
			got, err := os.ReadFile(dst)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
		}
	},
		Entry("leading holes", []int{8 * 1024 * 1024}, false),
		Entry("middle holes", []int{0, 8 * 1024 * 1024}, false),
		Entry("trailing holes", []int{0}, false),
		Entry("an entirely sparse file", []int{}, false),
		Entry("leading holes across filesystems", []int{8 * 1024 * 1024}, true),
		Entry("middle holes across filesystems", []int{0, 8 * 1024 * 1024}, true),
		Entry("trailing holes across filesystems", []int{0}, true),
		Entry("an entirely sparse file across filesystems", []int{}, true),
	)
})
