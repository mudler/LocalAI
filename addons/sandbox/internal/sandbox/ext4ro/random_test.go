package ext4ro

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The images that the corruption specs and the fuzz target start from. All
// of them come from testdata, so these specs run without e2fsprogs.
var corruptionBases = []string{"golden-ext4", "golden-ext2-1k", "golden-inline", "golden-htree"}

var randomWalk = walkLimits{depth: 64, nodes: 50000, dirEntries: 50000, fileBytes: 4 << 20}

// fullWalk runs every kind of call on an image, as a caller does. It returns
// the first error. A panic is not caught: it fails the spec.
func fullWalk(img []byte) error {
	f, err := openBytes(img)
	if err != nil {
		return err
	}
	_, err = walkFS(f, randomWalk)
	// Also call the reader on inodes that no directory reaches, and with
	// the wrong type, because a caller may do both with forged numbers.
	for _, ino := range []uint64{0, 1, 2, 3, 7, 8, 11, 12, 13, 20, 40, f.InodeCount(), f.InodeCount() + 1, 1 << 40} {
		_, _ = f.Stat(ino)
		_, _ = f.ReadDir(ino, 100)
		_, _ = f.Readlink(ino)
		_, _ = f.Xattrs(ino)
		if fl, e := f.Open(ino); e == nil {
			buf := make([]byte, 4096)
			_, _ = fl.ReadAt(buf, 0)
			_, _ = fl.ReadAt(buf, fl.Size()/2)
		}
	}
	return err
}

// typed reports whether err is one that the reader or the walk defines.
func typed(err error) bool {
	if err == nil {
		return true
	}
	for _, t := range []error{ErrCorrupt, ErrLimit, ErrUnsupported, ErrNotDir, ErrNotRegular, ErrNotSymlink, errLoop} {
		if errors.Is(err, t) {
			return true
		}
	}
	return strings.HasPrefix(err.Error(), "walk: path ") || strings.Contains(err.Error(), "negative entry cap")
}

// corrupt writes n damaged bytes into img. Most of them land in chunks that
// are not zero, because those hold the structures. The rest land anywhere.
func corrupt(img []byte, rng *rand.Rand, n int, chunks []int) {
	for range n {
		var off int
		if len(chunks) > 0 && rng.IntN(10) < 9 {
			off = chunks[rng.IntN(len(chunks))] + rng.IntN(64)
		} else {
			off = rng.IntN(len(img))
		}
		if off >= len(img) {
			off = len(img) - 1
		}
		switch rng.IntN(5) {
		case 0:
			img[off] = 0
		case 1:
			img[off] = 0xFF
		case 2:
			img[off] ^= 1 << rng.IntN(8)
		case 3:
			img[off] = byte(rng.IntN(256))
		default:
			// A small number: a count, a length or an offset.
			img[off] = byte(rng.IntN(8))
		}
	}
}

func nonZeroChunks(img []byte) []int {
	var out []int
	for off := 0; off+64 <= len(img); off += 64 {
		if bytes.Count(img[off:off+64], []byte{0}) != 64 {
			out = append(out, off)
		}
	}
	return out
}

var _ = Describe("random corruption", func() {
	const perImage = 1500
	for i, name := range corruptionBases {
		It("survives "+name, func() {
			base := loadGolden(name + ".img.gz")
			chunks := nonZeroChunks(base)
			Expect(chunks).NotTo(BeEmpty())
			rng := rand.New(rand.NewPCG(uint64(i)+1, 0x5eed))
			work := make([]byte, len(base))
			failed := 0
			for iter := range perImage {
				copy(work, base)
				corrupt(work, rng, 1+rng.IntN(48), chunks)
				err, hung := guarded(5*time.Second, func() error { return fullWalk(work) })
				Expect(hung).To(BeFalse(), "iteration %d hung", iter)
				Expect(typed(err)).To(BeTrue(), "iteration %d: untyped error %v", iter, err)
				if err != nil {
					failed++
				}
			}
			// The corruption must matter: most runs find damage, and some
			// do not. A test that never fails would not test anything.
			Expect(failed).To(BeNumerically(">", perImage/10), "damage was found in only %d runs", failed)
			GinkgoWriter.Printf("%s: %d of %d corrupted images gave an error\n", name, failed, perImage)
		})
	}

	It("survives a corrupted tree that mke2fs built", func() {
		img := buildImage(imageSpec{name: "rand-tree", size: 16 << 20, args: []string{"-t", "ext4", "-O", "^has_journal"}, tree: makeTree, script: treeScript})
		DeferCleanup(releaseImage, "rand-tree")
		base := readImage(img)
		chunks := nonZeroChunks(base)
		rng := rand.New(rand.NewPCG(99, 0x5eed))
		work := make([]byte, len(base))
		for iter := range 300 {
			copy(work, base)
			corrupt(work, rng, 1+rng.IntN(64), chunks)
			err, hung := guarded(10*time.Second, func() error { return fullWalk(work) })
			Expect(hung).To(BeFalse(), "iteration %d hung", iter)
			Expect(typed(err)).To(BeTrue(), "iteration %d: untyped error %v", iter, err)
		}
	})
})
