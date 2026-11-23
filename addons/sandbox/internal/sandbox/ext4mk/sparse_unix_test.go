//go:build unix

package ext4mk_test

import (
	"os"
	"runtime"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ext4mk"
)

var _ = Describe("FormatFile sparseness", func() {
	It("allocates only the metadata of a 4 GiB image", func() {
		path := newImage(ext4mk.Options{Size: 4 * GiB, Now: fixedNow})
		st, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Size()).To(Equal(4 * GiB))
		sys, ok := st.Sys().(*syscall.Stat_t)
		Expect(ok).To(BeTrue())
		allocated := int64(sys.Blocks) * 512 // #nosec G115 -- small
		GinkgoWriter.Printf("4 GiB image allocates %d KiB\n", allocated>>10)
		// 32 groups write two bitmaps each, and a few groups hold a backup.
		// APFS rounds the allocation of a sparse file up, and a 4 GiB image
		// takes about 2.5 MiB there. The image is still sparse: the bound
		// stays far below its size.
		limit := int64(2 * MiB)
		if runtime.GOOS == "darwin" {
			limit = 8 * MiB
		}
		Expect(allocated).To(BeNumerically("<", limit))
	})
})
