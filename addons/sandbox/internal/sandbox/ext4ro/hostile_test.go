package ext4ro

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// hctx is one hostile case: a copy of a golden image, and the good reader on
// the original bytes, which finds the structures to damage.
type hctx struct {
	img  []byte
	good *FS
}

func newCtx(golden string) *hctx {
	GinkgoHelper()
	b := loadGolden(golden + ".img.gz")
	return &hctx{img: append([]byte(nil), b...), good: mustOpen(b)}
}

func (c *hctx) ino(path string) uint64 {
	GinkgoHelper()
	ino := uint64(rootIno)
	for _, part := range strings.Split(strings.Trim(path, "/"), "/") {
		if part == "" {
			continue
		}
		kids, err := c.good.ReadDir(ino, 1000)
		Expect(err).To(Succeed())
		found := false
		for _, k := range kids {
			if k.Name == part {
				ino, found = k.Ino, true
				break
			}
		}
		Expect(found).To(BeTrue(), "path %q has no %q", path, part)
	}
	return ino
}

// raw returns the bytes of the inode of path, in the damaged image.
func (c *hctx) raw(path string) []byte {
	GinkgoHelper()
	off := inodeOffset(c.good, c.ino(path))
	return c.img[off : off+int64(c.good.isz)]
}

// block returns block n of the damaged image.
func (c *hctx) block(n uint64) []byte {
	bs := c.good.bs
	return c.img[n*bs : (n+1)*bs]
}

// firstBlock returns the physical block of logical block 0 of path.
func (c *hctx) firstBlock(path string) uint64 {
	GinkgoHelper()
	in, err := c.good.load(c.ino(path))
	Expect(err).To(Succeed())
	budget := 100
	r, err := c.good.lookup(in, 0, &budget)
	Expect(err).To(Succeed())
	Expect(r.hole).To(BeFalse())
	return r.phys
}

// freeBlock returns a block at the end of the image that holds only zeros,
// for a crafted node. It counts down from the last block.
func (c *hctx) freeBlock(k uint64) uint64 {
	GinkgoHelper()
	n := c.good.blocks - 1 - k
	Expect(bytes.Count(c.block(n), []byte{0})).To(BeEquivalentTo(c.good.bs), "block %d is not free", n)
	return n
}

func (c *hctx) sb(off int) []byte { return c.img[sbOffset+off:] }

// extentRoot returns the i_block bytes of path.
func (c *hctx) extentRoot(path string) []byte { return c.raw(path)[0x28 : 0x28+60] }

func put16(b []byte, v uint16) { le.PutUint16(b, v) }
func put32(b []byte, v uint32) { le.PutUint32(b, v) }

type hostileCase struct {
	name   string
	golden string
	mutate func(c *hctx)
	// op runs on the damaged image. nil means that Open must fail.
	op func(f *FS, c *hctx) error
	// want is the error that op or Open must return. nil means that the
	// outcome is free, but the call must end without a panic.
	want error
}

func statOp(path string) func(*FS, *hctx) error {
	return func(f *FS, c *hctx) error { _, err := f.Stat(c.ino(path)); return err }
}

func readDirOp(path string) func(*FS, *hctx) error {
	return func(f *FS, c *hctx) error { _, err := f.ReadDir(c.ino(path), 100000); return err }
}

func readFileOp(path string) func(*FS, *hctx) error {
	return func(f *FS, c *hctx) error {
		fl, err := f.Open(c.ino(path))
		if err != nil {
			return err
		}
		_, err = io.Copy(io.Discard, io.LimitReader(fl.SectionReader(), 1<<30))
		return err
	}
}

func readlinkOp(path string) func(*FS, *hctx) error {
	return func(f *FS, c *hctx) error { _, err := f.Readlink(c.ino(path)); return err }
}

func xattrsOp(path string) func(*FS, *hctx) error {
	return func(f *FS, c *hctx) error { _, err := f.Xattrs(c.ino(path)); return err }
}

// setDirEntry overwrites fields of the record of name in the first block of
// the directory, and returns the offset of the record.
func (c *hctx) dirRecord(dir, name string) []byte {
	GinkgoHelper()
	blk := c.block(c.firstBlock(dir))
	for off := 0; off+8 <= len(blk); {
		rl := int(le.Uint16(blk[off+4:]))
		nl := int(blk[off+6])
		if le.Uint32(blk[off:]) != 0 && string(blk[off+8:off+8+nl]) == name {
			return blk[off:]
		}
		Expect(rl).To(BeNumerically(">=", 8))
		off += rl
	}
	Fail("no record " + name + " in " + dir)
	return nil
}

var hostileCases = []hostileCase{
	// Superblock and geometry: Open must say no.
	{name: "bad magic", golden: "golden-ext4", mutate: func(c *hctx) { put16(c.sb(0x38), 0) }, want: ErrCorrupt},
	{name: "block size shift 7", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.sb(0x18), 7) }, want: ErrCorrupt},
	{name: "block size shift 0xFFFFFFFF", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.sb(0x18), 0xFFFFFFFF) }, want: ErrCorrupt},
	{name: "blocks per group 0", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.sb(0x20), 0) }, want: ErrCorrupt},
	{name: "blocks per group over one bitmap", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.sb(0x20), 8*4096+1) }, want: ErrCorrupt},
	{name: "inodes per group 0", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.sb(0x28), 0) }, want: ErrCorrupt},
	{name: "inodes per group over one bitmap", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.sb(0x28), 0xFFFFFFFF) }, want: ErrCorrupt},
	{name: "inode count disagrees", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.sb(0x00), uint32(c.good.inodes)+1) }, want: ErrCorrupt},
	{name: "inode size 100", golden: "golden-ext4", mutate: func(c *hctx) { put16(c.sb(0x58), 100) }, want: ErrCorrupt},
	{name: "inode size 64", golden: "golden-ext4", mutate: func(c *hctx) { put16(c.sb(0x58), 64) }, want: ErrCorrupt},
	{name: "inode size over the block", golden: "golden-ext4", mutate: func(c *hctx) { put16(c.sb(0x58), 8192) }, want: ErrCorrupt},
	{name: "block count over the image", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.sb(0x04), 0xFFFFFFF0) }, want: ErrCorrupt},
	{name: "block count 64-bit high half", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.sb(0x150), 0x7FFFFFFF) }, want: ErrCorrupt},
	{name: "block count 1 (descriptor table outside)", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.sb(0x04), 1) }, want: ErrCorrupt},
	{name: "block count 0", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.sb(0x04), 0) }, want: ErrCorrupt},
	{name: "first data block on a 4 KiB file system", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.sb(0x14), 1) }, want: ErrCorrupt},
	{name: "first inode 3", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.sb(0x54), 3) }, want: ErrCorrupt},
	{name: "first inode past the count", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.sb(0x54), 0xFFFFFFFF) }, want: ErrCorrupt},
	{name: "revision 2", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.sb(0x4C), 2) }, want: ErrUnsupported},
	{name: "journal needs recovery", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.sb(0x60), le.Uint32(c.sb(0x60))|incompatRecover) }, want: ErrUnsupported},
	{name: "unknown incompat bit", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.sb(0x60), le.Uint32(c.sb(0x60))|0x80000000) }, want: ErrUnsupported},
	{name: "compression", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.sb(0x60), le.Uint32(c.sb(0x60))|incompatCompression) }, want: ErrUnsupported},
	{name: "encryption", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.sb(0x60), le.Uint32(c.sb(0x60))|incompatEncrypt) }, want: ErrUnsupported},
	{name: "bigalloc", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.sb(0x64), le.Uint32(c.sb(0x64))|roCompatBigalloc) }, want: ErrUnsupported},
	{name: "descriptor size 63", golden: "golden-ext4", mutate: func(c *hctx) {
		Expect(c.good.incompat & incompat64Bit).NotTo(BeZero())
		put16(c.sb(0xFE), 63)
	}, want: ErrCorrupt},
	{name: "descriptor size over the block", golden: "golden-ext4", mutate: func(c *hctx) { put16(c.sb(0xFE), 8192) }, want: ErrCorrupt},
	{name: "image of one block", golden: "golden-ext4", mutate: func(c *hctx) { c.img = c.img[:1024] }, want: ErrCorrupt},
	{name: "image cut in half", golden: "golden-ext4", mutate: func(c *hctx) { c.img = c.img[:len(c.img)/2] }, want: ErrCorrupt},
	{name: "image cut after the superblock", golden: "golden-ext4", mutate: func(c *hctx) { c.img = c.img[:2048] }, want: ErrCorrupt},
	{name: "block size 1 KiB with first data block 0", golden: "golden-ext2-1k", mutate: func(c *hctx) { put32(c.sb(0x14), 0) }, want: ErrCorrupt},

	// Group descriptors and inode tables.
	{name: "inode table past the end", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.img[4096+0x08:], 0xFFFFFFF0) }, op: statOp("/a.txt"), want: ErrCorrupt},
	{name: "inode table in the high half", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.img[4096+0x28:], 0x7FFFFFFF) }, op: statOp("/a.txt"), want: ErrCorrupt},
	{name: "inode table 0", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.img[4096+0x08:], 0) }, op: statOp("/a.txt"), want: ErrCorrupt},
	{name: "inode table too close to the end", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.img[4096+0x08:], uint32(c.good.blocks-1)) }, op: statOp("/a.txt"), want: ErrCorrupt},
	{name: "inode table past the end (1 KiB)", golden: "golden-ext2-1k", mutate: func(c *hctx) { put32(c.img[2048+0x08:], 0xFFFFFFF0) }, op: statOp("/a.txt"), want: ErrCorrupt},
	{name: "inode number 0", golden: "golden-ext4", op: func(f *FS, c *hctx) error { _, err := f.Stat(0); return err }, want: ErrCorrupt},
	{name: "inode number past the count", golden: "golden-ext4", op: func(f *FS, c *hctx) error { _, err := f.Stat(f.InodeCount() + 1); return err }, want: ErrCorrupt},
	{name: "inode number 2^64-1", golden: "golden-ext4", op: func(f *FS, c *hctx) error { _, err := f.Stat(^uint64(0)); return err }, want: ErrCorrupt},
	{name: "extra_isize past the inode", golden: "golden-ext4", mutate: func(c *hctx) { put16(c.raw("/a.txt")[0x80:], 200) }, op: statOp("/a.txt"), want: ErrCorrupt},
	{name: "extra_isize not a multiple of 4", golden: "golden-ext4", mutate: func(c *hctx) { put16(c.raw("/a.txt")[0x80:], 31) }, op: statOp("/a.txt"), want: ErrCorrupt},
	{name: "size with the high bit set", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.raw("/big")[0x6C:], 0xFFFFFFFF) }, op: statOp("/big"), want: ErrCorrupt},

	// Extent trees.
	{name: "extent header magic", golden: "golden-ext4", mutate: func(c *hctx) { put16(c.extentRoot("/big"), 0) }, op: readFileOp("/big"), want: ErrCorrupt},
	{name: "extent entries over max", golden: "golden-ext4", mutate: func(c *hctx) { put16(c.extentRoot("/big")[2:], 5) }, op: readFileOp("/big"), want: ErrCorrupt},
	{name: "extent max over the node", golden: "golden-ext4", mutate: func(c *hctx) { put16(c.extentRoot("/big")[4:], 100) }, op: readFileOp("/big"), want: ErrCorrupt},
	{name: "extent depth 6", golden: "golden-ext4", mutate: func(c *hctx) { put16(c.extentRoot("/big")[6:], 6) }, op: readFileOp("/big"), want: ErrCorrupt},
	{name: "extent depth 0xFFFF", golden: "golden-ext4", mutate: func(c *hctx) { put16(c.extentRoot("/big")[6:], 0xFFFF) }, op: readFileOp("/big"), want: ErrCorrupt},
	{name: "extent index points to itself", golden: "golden-ext4", mutate: func(c *hctx) {
		self := c.firstBlock("/big")
		root := c.extentRoot("/big")
		put16(root[2:], 1)
		put16(root[4:], 4)
		put16(root[6:], 1)
		put32(root[12:], 0)
		put32(root[16:], uint32(self))
		put16(root[20:], 0)
		// The block that the index names holds the same root.
		copy(c.block(self), root[:24])
		put16(c.block(self)[4:], 340)
	}, op: readFileOp("/big"), want: ErrCorrupt},
	{name: "extent index points past the end", golden: "golden-ext4", mutate: func(c *hctx) {
		root := c.extentRoot("/big")
		put16(root[2:], 1)
		put16(root[6:], 1)
		put32(root[16:], 0xFFFFFFF0)
	}, op: readFileOp("/big"), want: ErrCorrupt},
	{name: "extent index with 0 entries", golden: "golden-ext4", mutate: func(c *hctx) {
		root := c.extentRoot("/big")
		put16(root[2:], 0)
		put16(root[6:], 1)
	}, op: readFileOp("/big"), want: ErrCorrupt},
	{name: "extent of length 0", golden: "golden-ext4", mutate: func(c *hctx) { put16(c.extentRoot("/big")[12+4:], 0) }, op: readFileOp("/big"), want: ErrCorrupt},
	{name: "extent past the end", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.extentRoot("/big")[12+8:], 0xFFFFFFF0) }, op: readFileOp("/big"), want: ErrCorrupt},
	{name: "extent start in the high half", golden: "golden-ext4", mutate: func(c *hctx) { put16(c.extentRoot("/big")[12+6:], 0xFFFF) }, op: readFileOp("/big"), want: ErrCorrupt},
	{name: "extent longer than the file system", golden: "golden-ext4", mutate: func(c *hctx) { put16(c.extentRoot("/big")[12+4:], 32768) }, op: readFileOp("/big"), want: ErrCorrupt},
	{name: "extents overlap", golden: "golden-ext4", mutate: func(c *hctx) {
		root := c.extentRoot("/sparse")
		Expect(le.Uint16(root[2:])).To(BeEquivalentTo(2), "the sparse file has two extents")
		put32(root[12+12:], 0)
	}, op: readFileOp("/sparse"), want: ErrCorrupt},
	{name: "extents out of order", golden: "golden-ext4", mutate: func(c *hctx) {
		root := c.extentRoot("/sparse")
		a, b := append([]byte(nil), root[12:24]...), append([]byte(nil), root[24:36]...)
		copy(root[12:], b)
		copy(root[24:], a)
	}, op: readFileOp("/sparse"), want: ErrCorrupt},

	// Block maps.
	{name: "block map direct pointer past the end", golden: "golden-ext2-1k", mutate: func(c *hctx) { put32(c.raw("/big")[0x28:], 0xFFFFFFFF) }, op: readFileOp("/big"), want: ErrCorrupt},
	{name: "block map indirect pointer past the end", golden: "golden-ext2-1k", mutate: func(c *hctx) { put32(c.raw("/big")[0x28+48:], 0xFFFFFFF0) }, op: readFileOp("/big"), want: ErrCorrupt},
	{name: "block map double indirect past the end", golden: "golden-ext2-1k", mutate: func(c *hctx) {
		put32(c.raw("/big")[0x28+52:], 0xFFFFFFF0)
		// A size that reaches into the double indirect range.
		put32(c.raw("/big")[0x04:], 300<<10)
	}, op: readFileOp("/big"), want: ErrCorrupt},
	{name: "block map data pointer inside an indirect block", golden: "golden-ext2-1k", mutate: func(c *hctx) {
		// The indirect block lists itself 256 times.
		ind := le.Uint32(c.raw("/big")[0x28+48:])
		blk := c.block(uint64(ind))
		for i := 0; i < len(blk); i += 4 {
			put32(blk[i:], ind)
		}
	}, op: readFileOp("/big")},
	{name: "block map double indirect loop", golden: "golden-ext2-1k", mutate: func(c *hctx) {
		ind := le.Uint32(c.raw("/big")[0x28+48:])
		put32(c.raw("/big")[0x28+52:], ind)
		put32(c.raw("/big")[0x28+56:], ind)
		blk := c.block(uint64(ind))
		for i := 0; i < len(blk); i += 4 {
			put32(blk[i:], ind)
		}
		// A size that reads through the double and the triple indirect.
		put32(c.raw("/big")[0x04:], 1<<31)
	}, op: func(f *FS, c *hctx) error {
		fl, err := f.Open(c.ino("/big"))
		if err != nil {
			return err
		}
		_, err = io.CopyN(io.Discard, fl.SectionReader(), 80<<20)
		return err
	}},

	// Directories.
	{name: "directory rec_len 0", golden: "golden-ext4", mutate: func(c *hctx) { put16(c.dirRecord("/dir", "b.txt")[4:], 0) }, op: readDirOp("/dir"), want: ErrCorrupt},
	{name: "directory rec_len 7", golden: "golden-ext4", mutate: func(c *hctx) { put16(c.dirRecord("/dir", "b.txt")[4:], 7) }, op: readDirOp("/dir"), want: ErrCorrupt},
	{name: "directory rec_len 14", golden: "golden-ext4", mutate: func(c *hctx) { put16(c.dirRecord("/dir", "b.txt")[4:], 14) }, op: readDirOp("/dir"), want: ErrCorrupt},
	{name: "directory rec_len past the block", golden: "golden-ext4", mutate: func(c *hctx) { put16(c.dirRecord("/dir", "b.txt")[4:], 4096) }, op: readDirOp("/dir"), want: ErrCorrupt},
	{name: "directory rec_len 0xFFFF", golden: "golden-ext4", mutate: func(c *hctx) { put16(c.dirRecord("/dir", "b.txt")[4:], 0xFFFF) }, op: readDirOp("/dir"), want: ErrCorrupt},
	{name: "directory name_len over rec_len", golden: "golden-ext4", mutate: func(c *hctx) { c.dirRecord("/dir", "b.txt")[6] = 255 }, op: readDirOp("/dir"), want: ErrCorrupt},
	{name: "directory name_len 0", golden: "golden-ext4", mutate: func(c *hctx) { c.dirRecord("/dir", "b.txt")[6] = 0 }, op: readDirOp("/dir"), want: ErrCorrupt},
	{name: "directory name with a slash", golden: "golden-ext4", mutate: func(c *hctx) { c.dirRecord("/dir", "b.txt")[9] = '/' }, op: readDirOp("/dir"), want: ErrCorrupt},
	{name: "directory name with a NUL byte", golden: "golden-ext4", mutate: func(c *hctx) { c.dirRecord("/dir", "b.txt")[9] = 0 }, op: readDirOp("/dir"), want: ErrCorrupt},
	{name: "directory entry inode 0xFFFFFFFF", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.dirRecord("/dir", "b.txt"), 0xFFFFFFFF) }, op: readDirOp("/dir"), want: ErrCorrupt},
	{name: "directory entry inode 1", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.dirRecord("/dir", "b.txt"), 1) }, op: readDirOp("/dir"), want: ErrCorrupt},
	{name: "directory entry inode 3", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.dirRecord("/dir", "b.txt"), 3) }, op: readDirOp("/dir"), want: ErrCorrupt},
	{name: "directory entry inode past the count", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.dirRecord("/dir", "b.txt"), uint32(c.good.inodes)+1) }, op: readDirOp("/dir"), want: ErrCorrupt},
	{name: "directory size not a multiple of the block", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.raw("/dir")[0x04:], 4097) }, op: readDirOp("/dir"), want: ErrCorrupt},
	{name: "directory size 0", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.raw("/dir")[0x04:], 0) }, op: readDirOp("/dir"), want: ErrCorrupt},
	{name: "directory size over the cap", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.raw("/dir")[0x04:], 0xFFFFF000) }, op: readDirOp("/dir"), want: ErrLimit},
	{name: "directory with a hole", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.raw("/dir")[0x04:], 8192) }, op: readDirOp("/dir"), want: ErrCorrupt},
	{name: "directory block past the end", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.extentRoot("/dir")[12+8:], 0xFFFFFFF0) }, op: readDirOp("/dir"), want: ErrCorrupt},
	{name: "directory block is an unwritten extent", golden: "golden-ext4", mutate: func(c *hctx) { put16(c.extentRoot("/dir")[12+4:], 1|0x8000) }, op: readDirOp("/dir"), want: ErrCorrupt},
	{name: "directory with a bad extent header", golden: "golden-ext4", mutate: func(c *hctx) { put16(c.extentRoot("/dir"), 1) }, op: readDirOp("/dir"), want: ErrCorrupt},
	{name: "readdir on a file", golden: "golden-ext4", op: func(f *FS, c *hctx) error { _, err := f.ReadDir(c.ino("/a.txt"), 10); return err }, want: ErrNotDir},
	{name: "open a directory", golden: "golden-ext4", op: func(f *FS, c *hctx) error { _, err := f.Open(c.ino("/dir")); return err }, want: ErrNotRegular},
	{name: "readlink on a file", golden: "golden-ext4", op: readlinkOp("/a.txt"), want: ErrNotSymlink},
	{name: "readdir cap 0 on a full directory", golden: "golden-ext4", op: func(f *FS, c *hctx) error { _, err := f.ReadDir(c.ino("/dir"), 0); return err }, want: ErrLimit},
	{name: "readdir negative cap", golden: "golden-ext4", op: func(f *FS, c *hctx) error { _, err := f.ReadDir(c.ino("/dir"), -1); return err }, want: errAny},

	// Symlinks.
	{name: "symlink of size 0", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.raw("/link")[0x04:], 0) }, op: readlinkOp("/link"), want: ErrCorrupt},
	{name: "symlink over 4096 bytes", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.raw("/link")[0x04:], 5000) }, op: readlinkOp("/link"), want: ErrLimit},
	{name: "fast symlink longer than i_block", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.raw("/link")[0x04:], 100) }, op: readlinkOp("/link"), want: ErrCorrupt},
	{name: "symlink with a NUL byte", golden: "golden-ext4", mutate: func(c *hctx) { c.raw("/link")[0x28+1] = 0 }, op: readlinkOp("/link"), want: ErrCorrupt},
	{name: "slow symlink with a bad extent", golden: "golden-ext4", mutate: func(c *hctx) { put16(c.extentRoot("/dir/longlink"), 0) }, op: readlinkOp("/dir/longlink"), want: ErrCorrupt},
	{name: "slow symlink longer than its blocks", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.raw("/dir/longlink")[0x04:], 4000) }, op: readlinkOp("/dir/longlink")},

	// File sizes.
	{name: "file of 2^63-1 bytes", golden: "golden-ext4", mutate: func(c *hctx) {
		put32(c.raw("/big")[0x04:], 0xFFFFFFFF)
		put32(c.raw("/big")[0x6C:], 0x7FFFFFFF)
	}, op: func(f *FS, c *hctx) error {
		fl, err := f.Open(c.ino("/big"))
		if err != nil {
			return err
		}
		Expect(fl.Size()).To(BeEquivalentTo(int64(1<<63 - 1)))
		// Read the last bytes: no overflow, and EOF at the end.
		p := make([]byte, 100)
		n, err := fl.ReadAt(p, fl.Size()-10)
		Expect(n).To(Equal(10))
		Expect(err).To(Equal(io.EOF))
		Expect(p[:10]).To(Equal(make([]byte, 10)))
		// Stream the start: more than the image holds.
		_, err = io.CopyN(io.Discard, fl.SectionReader(), 300<<20)
		return err
	}},

	// Extended attributes.
	{name: "xattr area without the magic", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.raw("/a.txt")[128+32:], 0) }, op: xattrsOp("/a.txt"), want: ErrCorrupt},
	{name: "xattr value offset past the area", golden: "golden-ext4", mutate: func(c *hctx) { put16(c.raw("/a.txt")[128+32+4+2:], 0xFFFF) }, op: xattrsOp("/a.txt"), want: ErrCorrupt},
	{name: "xattr value size 0xFFFFFFFF", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.raw("/a.txt")[128+32+4+8:], 0xFFFFFFFF) }, op: xattrsOp("/a.txt"), want: ErrCorrupt},
	{name: "xattr name_len 255", golden: "golden-ext4", mutate: func(c *hctx) { c.raw("/a.txt")[128+32+4] = 255 }, op: xattrsOp("/a.txt"), want: ErrCorrupt},
	{name: "xattr entries without a terminator", golden: "golden-ext4", mutate: func(c *hctx) {
		area := c.raw("/a.txt")[128+32+4:]
		for i := range area {
			area[i] = 1
		}
	}, op: xattrsOp("/a.txt"), want: ErrCorrupt},
	{name: "xattr unknown name index", golden: "golden-ext4", mutate: func(c *hctx) { c.raw("/a.txt")[128+32+4+1] = 9 }, op: xattrsOp("/a.txt"), want: ErrUnsupported},
	{name: "xattr entry with no name", golden: "golden-ext4", mutate: func(c *hctx) {
		e := c.raw("/a.txt")[128+32+4:]
		e[0], e[1] = 0, 0
	}, op: xattrsOp("/a.txt"), want: ErrCorrupt},
	{name: "xattr block past the end", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.raw("/a.txt")[0x68:], 0xFFFFFFF0) }, op: xattrsOp("/a.txt"), want: ErrCorrupt},
	{name: "xattr block that is not one", golden: "golden-ext4", mutate: func(c *hctx) { put32(c.raw("/a.txt")[0x68:], uint32(c.firstBlock("/big"))) }, op: xattrsOp("/a.txt"), want: ErrCorrupt},
	{name: "xattr block with two blocks claimed", golden: "golden-ext4", mutate: func(c *hctx) {
		n := c.freeBlock(0)
		craftXattrBlock(c.block(n), "b", "value")
		put32(c.block(n)[8:], 2)
		put32(c.raw("/a.txt")[0x68:], uint32(n))
	}, op: xattrsOp("/a.txt"), want: ErrCorrupt},
	{name: "xattr block value past the block", golden: "golden-ext4", mutate: func(c *hctx) {
		n := c.freeBlock(0)
		craftXattrBlock(c.block(n), "b", "value")
		put32(c.block(n)[32+8:], 100000)
		put32(c.raw("/a.txt")[0x68:], uint32(n))
	}, op: xattrsOp("/a.txt"), want: ErrCorrupt},
	{name: "inline directory with a bad record", golden: "golden-inline", mutate: func(c *hctx) {
		d := c.raw("/dir")
		Expect(le.Uint32(d[0x20:]) & flagInlineData).NotTo(BeZero())
		put16(d[0x28+4+4:], 3)
	}, op: readDirOp("/dir"), want: ErrCorrupt},
	{name: "inline symlink without its continuation", golden: "golden-inline", mutate: func(c *hctx) {
		l := c.raw("/dir/longlink")
		Expect(le.Uint32(l[0x20:]) & flagInlineData).NotTo(BeZero())
		// Break the name of the "data" attribute so it is not found.
		area := l[128+32+4:]
		Expect(string(area[16:20])).To(Equal("data"))
		area[16] = 'x'
	}, op: readlinkOp("/dir/longlink"), want: ErrCorrupt},
	{name: "inline file with a size past its data", golden: "golden-inline", mutate: func(c *hctx) { put32(c.raw("/a.txt")[0x04:], 100000) }, op: readFileOp("/a.txt")},
}

// errAny marks a case that only needs some error.
var errAny = errors.New("any error")

// craftXattrBlock writes an attribute block that has one user attribute.
func craftXattrBlock(b []byte, name, value string) {
	clear(b)
	put32(b[0:], xattrMagic)
	put32(b[4:], 1) // refcount
	put32(b[8:], 1) // blocks
	e := b[32:]
	e[0] = byte(len(name))
	e[1] = 1 // user.
	vo := len(b) - 4*((len(value)+3)/4)
	put16(e[2:], uint16(vo))
	put32(e[8:], uint32(len(value)))
	copy(e[16:], name)
	copy(b[vo:], value)
}

var _ = Describe("hostile images", func() {
	for _, tc := range hostileCases {
		It(tc.name, func() {
			c := newCtx(tc.golden)
			if tc.mutate != nil {
				tc.mutate(c)
			}
			var f *FS
			err, hung := guarded(5*time.Second, func() error {
				var err error
				f, err = openBytes(c.img)
				if err != nil || tc.op == nil {
					return err
				}
				return tc.op(f, c)
			})
			Expect(hung).To(BeFalse(), "the reader hung")
			GinkgoWriter.Printf("result: %v\n", err)
			switch {
			case tc.want == nil:
				// The outcome is free. Reaching here means no panic and no hang.
			case tc.want == errAny:
				Expect(err).To(HaveOccurred())
			default:
				Expect(err).To(MatchError(tc.want))
			}
		})
	}
})
