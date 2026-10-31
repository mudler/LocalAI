package chv

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The test image of testUpper is 16 MiB, so its default stage budget is that.
const testImageBytes = 16 << 20

// addSparse adds data/w/sparse.db, a file of one block whose apparent size is
// size bytes.
func addSparse(img string, size int64) {
	GinkgoHelper()
	src := filepath.Join(filepath.Dir(img), "src", "new.txt")
	debugfsWrite(img, "write "+src+" data/w/sparse.db", fmt.Sprintf("sif data/w/sparse.db size %d", size))
}

func hasPath(src workspace.FixtureSource, p string) bool {
	for _, e := range src {
		if e.Path == p {
			return true
		}
	}
	return false
}

var _ = Describe("upper reader stage budget", func() {
	// An old sparse file that is larger than the budget but unchanged must
	// not block later reads: it is not sent, so it does not count.
	It("does not block on an unchanged sparse file", func() {
		needTools("mke2fs", "debugfs")
		img, r := testUpper()
		addSparse(img, 40<<20)
		// The first read stages the file, under a budget that fits it.
		r.lim.stage = 64 << 20
		src, ix, err := r.read(readCtx(), nil)
		Expect(err).To(Succeed())
		Expect(hasPath(src, "w/sparse.db")).To(BeTrue(), "the first read did not send the sparse file")
		// The next read has the default budget, the image size. The file is
		// unchanged, so the index does not send it.
		r.lim.stage = 0
		src, _, err = r.read(readCtx(), ix)
		Expect(err).To(Succeed(), "an unchanged sparse file blocked the read")
		Expect(hasPath(src, "w/sparse.db")).To(BeFalse(), "an unchanged file was sent again")
	})

	// A file that the index sends and that is larger than the budget fails
	// the read before anything is staged, and the error names the path.
	It("fails a sparse file over the budget before staging", func() {
		needTools("mke2fs", "debugfs")
		img, r := testUpper()
		addSparse(img, 40<<20)
		_, _, err := r.read(readCtx(), nil)
		want := fmt.Sprintf(`staging "w/sparse.db" (%d bytes) goes over the stage limit of %d bytes`, 40<<20, testImageBytes)
		Expect(err).To(MatchError(ContainSubstring(want)), "a sparse file over the budget")
		entries, _ := os.ReadDir(r.stage)
		Expect(entries).To(BeEmpty(), "files were staged before the budget check failed")
	})

	// Files under the budget stage. Each inode counts once, so a second name
	// for the same inode does not count twice: new.txt (4 bytes), edit.txt
	// (7) and x.txt (2) add up to 13 bytes, with hard.txt as a second name
	// for new.txt.
	It("stages files under the budget", func() {
		needTools("mke2fs", "debugfs")
		img, r := testUpper()
		debugfsWrite(img, "link data/w/new.txt data/w/hard.txt")
		r.lim.stage = 13
		_, _, err := r.read(readCtx(), nil)
		Expect(err).To(Succeed(), "13 bytes of files in a 13-byte budget")
		r.lim.stage = 12
		_, _, err = r.read(readCtx(), nil)
		Expect(err).To(MatchError(ContainSubstring("stage limit of 12 bytes")), "12 bytes of budget for 13 bytes of files")
	})
})
