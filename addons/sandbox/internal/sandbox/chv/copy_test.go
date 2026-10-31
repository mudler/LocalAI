//go:build linux

package chv

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("cloneFile", func() {
	It("stays sparse", func() {
		dir := GinkgoT().TempDir()
		src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
		f, err := os.Create(src)
		Expect(err).To(Succeed())
		Expect(f.Truncate(256 << 20)).To(Succeed())
		data := strings.Repeat("data", 1<<18) // 1 MiB
		_, err = f.WriteAt([]byte(data), 100<<20)
		Expect(err).To(Succeed())
		_ = f.Close()
		Expect(cloneFile(src, dst)).To(Succeed())
		st, err := os.Stat(dst)
		Expect(err).To(Succeed())
		Expect(st.Size()).To(BeEquivalentTo(256 << 20))
		blocks := st.Sys().(*syscall.Stat_t).Blocks * 512
		Expect(blocks).To(BeNumerically("<=", 16<<20), "the copy uses %d bytes on disk; it is not sparse", blocks)
		g, _ := os.Open(dst)
		defer func() { _ = g.Close() }()
		buf := make([]byte, len(data))
		_, err = g.ReadAt(buf, 100<<20)
		Expect(err).To(Succeed())
		Expect(string(buf)).To(Equal(data), "the data was not copied")
		_, err = g.ReadAt(buf[:4], 0)
		Expect(err).To(Succeed())
		Expect(string(buf[:4])).To(Equal("\x00\x00\x00\x00"), "the hole was not zero")
	})
})
