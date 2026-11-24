package ext4ro

import (
	"bytes"
	"errors"
	"io"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// failReader fails every read at or after the offset after.
type failReader struct {
	data  []byte
	err   error
	after int64
}

func (r *failReader) ReadAt(p []byte, off int64) (int, error) {
	if off >= r.after {
		return 0, r.err
	}
	return bytes.NewReader(r.data).ReadAt(p, off)
}

type readerFunc func(p []byte, off int64) (int, error)

func (f readerFunc) ReadAt(p []byte, off int64) (int, error) { return f(p, off) }

// craftTree rewrites the extent tree of /a.txt in a golden image to a tree of
// the given depth, with nodes in free blocks at the end of the image. The
// tree maps logical block 0 to the old data block.
func craftTree(c *hctx, depth int) []byte {
	GinkgoHelper()
	data := c.firstBlock("/a.txt")
	root := c.extentRoot("/a.txt")
	clear(root)
	put16(root[0:], 0xF30A)
	put16(root[2:], 1)
	put16(root[4:], 4)
	put16(root[6:], uint16(depth))
	next := root[12:]
	for level := depth; level >= 1; level-- {
		blk := c.freeBlock(uint64(level))
		put32(next[0:], 0)
		put32(next[4:], uint32(blk))
		put16(next[8:], 0)
		n := c.block(blk)
		put16(n[0:], 0xF30A)
		put16(n[2:], 1)
		put16(n[4:], 340)
		put16(n[6:], uint16(level-1))
		next = n[12:]
	}
	put32(next[0:], 0)
	put16(next[4:], 1)
	put16(next[6:], 0)
	put32(next[8:], uint32(data))
	return root
}

func readOneByte(f *FS, ino uint64, off int64) error {
	GinkgoHelper()
	fl, err := f.Open(ino)
	Expect(err).To(Succeed())
	_, err = fl.ReadAt(make([]byte, 1), off)
	return err
}

var _ = Describe("extent trees", func() {
	for depth := 0; depth <= 5; depth++ {
		It("reads a tree of depth "+string(rune('0'+depth)), func() {
			c := newCtx("golden-ext4")
			craftTree(c, depth)
			f := mustOpen(c.img)
			Expect(string(readAll(f, c.ino("/a.txt")))).To(Equal("hello\n"))
		})
	}

	It("rejects a tree of depth 6", func() {
		c := newCtx("golden-ext4")
		root := craftTree(c, 5)
		put16(root[6:], 6)
		Expect(readOneByte(mustOpen(c.img), c.ino("/a.txt"), 0)).To(MatchError(ErrCorrupt))
	})

	It("rejects a child whose depth is not one less", func() {
		c := newCtx("golden-ext4")
		craftTree(c, 3)
		put16(c.block(c.good.blocks - 1 - 2)[6:], 0)
		Expect(readOneByte(mustOpen(c.img), c.ino("/a.txt"), 0)).To(MatchError(ErrCorrupt))
	})

	It("rejects index entries out of order", func() {
		c := newCtx("golden-ext4")
		root := craftTree(c, 1)
		put16(root[2:], 2)
		copy(root[24:36], root[12:24])
		put32(root[24:], 0) // the same start block twice
		put32(c.raw("/a.txt")[0x04:], 100000)
		Expect(readOneByte(mustOpen(c.img), c.ino("/a.txt"), 5000)).To(MatchError(ErrCorrupt))
	})

	It("stops at the node budget", func() {
		c := newCtx("golden-ext4")
		craftTree(c, 5)
		f := mustOpen(c.img)
		in, err := f.load(c.ino("/a.txt"))
		Expect(err).To(Succeed())
		budget := 3
		_, err = f.lookup(in, 0, &budget)
		Expect(err).To(MatchError(ErrLimit))
		budget = 6
		r, err := f.lookup(in, 0, &budget)
		Expect(err).To(Succeed())
		Expect(r.hole).To(BeFalse())
		Expect(budget).To(Equal(0))
	})

	It("reads an unwritten extent as zeros", func() {
		c := newCtx("golden-ext4")
		ee := c.extentRoot("/big")[12+4:]
		put16(ee, le.Uint16(ee)|0x8000)
		want := readAll(c.good, c.ino("/big"))
		b := readAll(mustOpen(c.img), c.ino("/big"))
		Expect(b).To(HaveLen(40000))
		// mke2fs splits the file in two extents (8 blocks and 2): the first
		// one is unwritten now, the second is not.
		Expect(bytes.Count(b[:32768], []byte{0})).To(Equal(32768))
		Expect(bytes.Equal(b[32768:], want[32768:])).To(BeTrue())
	})

	It("reads holes of a sparse file as zeros", func() {
		c := newCtx("golden-ext4")
		fl, err := c.good.Open(c.ino("/sparse"))
		Expect(err).To(Succeed())
		p := make([]byte, 100)
		n, err := fl.ReadAt(p, 1000)
		Expect(err).To(Succeed())
		Expect(n).To(Equal(100))
		Expect(bytes.Count(p, []byte{0})).To(Equal(100))
		n, err = fl.ReadAt(p, 19950)
		Expect(n).To(Equal(50))
		Expect(err).To(Equal(io.EOF))
		n, err = fl.ReadAt(p, 20000)
		Expect(n).To(BeZero())
		Expect(err).To(Equal(io.EOF))
		_, err = fl.ReadAt(p, -1)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("directory blocks", func() {
	rec := func(b []byte, off int, ino uint32, rl uint16, name string, typ byte) {
		put32(b[off:], ino)
		put16(b[off+4:], rl)
		b[off+6] = byte(len(name))
		b[off+7] = typ
		copy(b[off+8:], name)
	}

	It("skips the checksum tail of a metadata_csum block", func() {
		f := &FS{bs: 4096, firstIno: 11, inodes: 100}
		b := make([]byte, 4096)
		rec(b, 0, 12, 12, "a", 1)
		rec(b, 12, 13, 4096-12-12, "b", 1)
		rec(b, 4096-12, 0, 12, "", 0xDE) // inode 0, rec_len 12, name_len 0, type 0xDE
		got, err := f.parseDirBlock(b, nil, 10)
		Expect(err).To(Succeed())
		Expect(got).To(Equal([]DirEntry{{12, "a", 1}, {13, "b", 1}}))
	})

	It("skips a dx node and the dx root records", func() {
		f := &FS{bs: 1024, firstIno: 11, inodes: 100}
		node := make([]byte, 1024)
		rec(node, 0, 0, 1024, "", 0) // a dx node: one empty record
		got, err := f.parseDirBlock(node, nil, 10)
		Expect(err).To(Succeed())
		Expect(got).To(BeEmpty())
		root := make([]byte, 1024)
		rec(root, 0, 2, 12, ".", 2)
		rec(root, 12, 2, 1012, "..", 2) // covers the dx root info and the index
		got, err = f.parseDirBlock(root, nil, 10)
		Expect(err).To(Succeed())
		Expect(got).To(BeEmpty())
	})

	It("decodes rec_len of a 64 KiB block", func() {
		f := &FS{bs: 65536, firstIno: 11, inodes: 100}
		b := make([]byte, 65536)
		rec(b, 0, 12, 0xFFFF, "x", 1) // 65535 means the whole block
		got, err := f.parseDirBlock(b, nil, 10)
		Expect(err).To(Succeed())
		Expect(got).To(HaveLen(1))
		b2 := make([]byte, 65536)
		rec(b2, 0, 12, 0, "y", 1) // 0 means the whole block too
		got, err = f.parseDirBlock(b2, nil, 10)
		Expect(err).To(Succeed())
		Expect(got).To(HaveLen(1))
		// A record of 65532 bytes leaves 4 bytes, which cannot hold a record.
		b3 := make([]byte, 65536)
		rec(b3, 0, 12, 0xFFFC, "z", 1)
		_, err = f.parseDirBlock(b3, nil, 10)
		Expect(err).To(MatchError(ErrCorrupt))
	})

	It("drops . and .. and stops at the cap", func() {
		f := &FS{bs: 1024, firstIno: 11, inodes: 100}
		b := make([]byte, 1024)
		rec(b, 0, 2, 12, ".", 2)
		rec(b, 12, 2, 12, "..", 2)
		rec(b, 24, 12, 12, "a", 1)
		rec(b, 36, 13, 1024-36, "b", 1)
		got, err := f.parseDirBlock(b, nil, 2)
		Expect(err).To(Succeed())
		Expect(got).To(HaveLen(2))
		_, err = f.parseDirBlock(b, nil, 1)
		Expect(err).To(MatchError(ErrLimit))
	})
})

var _ = Describe("xattr area limits", func() {
	It("caps values that entries share", func() {
		// 3000 entries share one 4000 byte value: 12 MB if each is copied.
		region := make([]byte, 65536)
		for i := range 3000 {
			e := region[i*20:]
			e[0] = 1
			e[1] = 1
			put16(e[2:], 61000)
			put32(e[8:], 4000)
			e[16] = 'a'
		}
		_, err := parseXattrs(region, 0)
		Expect(err).To(MatchError(ErrLimit))
	})

	It("marks an ea_inode value and keeps the others", func() {
		c := newCtx("golden-ext4")
		put32(c.raw("/a.txt")[128+32+4+4:], 77) // e_value_inum of the first entry
		f := mustOpen(c.img)
		xs, err := f.Xattrs(c.ino("/a.txt"))
		Expect(err).To(Succeed())
		Expect(xs).To(HaveLen(2))
		Expect(xs[0].Err).To(MatchError(ErrUnsupported))
		Expect(xs[1].Err).To(BeNil())
		Expect(xs[1].Value).NotTo(BeEmpty())
		_, ok, err := f.Xattr(c.ino("/a.txt"), xs[0].Name)
		Expect(ok).To(BeTrue())
		Expect(err).To(MatchError(ErrUnsupported))
		v, ok, err := f.Xattr(c.ino("/a.txt"), xs[1].Name)
		Expect(err).To(Succeed())
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal(xs[1].Value))
		_, ok, err = f.Xattr(c.ino("/a.txt"), "user.missing")
		Expect(err).To(Succeed())
		Expect(ok).To(BeFalse())
	})

	It("reads an attribute block", func() {
		c := newCtx("golden-ext4")
		n := c.freeBlock(0)
		craftXattrBlock(c.block(n), "b", "block value")
		put32(c.raw("/empty")[0x68:], uint32(n))
		v, ok, err := mustOpen(c.img).Xattr(c.ino("/empty"), "user.b")
		Expect(err).To(Succeed())
		Expect(ok).To(BeTrue())
		Expect(string(v)).To(Equal("block value"))
	})
})

var _ = Describe("the underlying reader", func() {
	It("passes an I/O error through, without calling it corruption", func() {
		img := loadGolden("golden-ext4.img.gz")
		boom := errors.New("disk on fire")
		f, err := Open(&failReader{data: img, err: boom, after: 8192}, int64(len(img)))
		Expect(err).To(Succeed())
		_, err = f.Stat(2)
		Expect(err).To(MatchError(boom))
		Expect(err).NotTo(MatchError(ErrCorrupt))
	})

	It("treats a short reader as corrupt", func() {
		// The reader holds less than the size that Open is told: it ends
		// before the last block, where /a.txt now has its data.
		c := newCtx("golden-ext4")
		last := c.good.blocks - 1
		put32(c.extentRoot("/a.txt")[12+8:], uint32(last))
		short := readerFunc(func(p []byte, off int64) (int, error) {
			if off >= int64(last*c.good.bs) {
				return 0, io.EOF
			}
			return bytes.NewReader(c.img).ReadAt(p, off)
		})
		f, err := Open(short, int64(len(c.img)))
		Expect(err).To(Succeed())
		fl, err := f.Open(c.ino("/a.txt"))
		Expect(err).To(Succeed())
		_, err = fl.ReadAt(make([]byte, 5), 0)
		Expect(err).To(MatchError(ErrCorrupt))
	})

	It("does not read outside the image", func() {
		img := loadGolden("golden-ext4.img.gz")
		r := readerFunc(func(p []byte, off int64) (int, error) {
			Expect(off).To(BeNumerically(">=", 0))
			Expect(off + int64(len(p))).To(BeNumerically("<=", int64(len(img))))
			return bytes.NewReader(img).ReadAt(p, off)
		})
		f, err := Open(r, int64(len(img)))
		Expect(err).To(Succeed())
		nodes, err := walkFS(f, defaultWalk)
		Expect(err).To(Succeed())
		Expect(len(nodes)).To(BeNumerically(">", 10))
	})

	It("refuses a nil reader and a short image", func() {
		_, err := Open(nil, 10)
		Expect(err).To(HaveOccurred())
		_, err = Open(strings.NewReader("x"), 1)
		Expect(err).To(MatchError(ErrCorrupt))
		_, err = Open(strings.NewReader("x"), -5)
		Expect(err).To(MatchError(ErrCorrupt))
	})
})

var _ = Describe("directory loops", func() {
	It("returns the entry that points back, and the walk catches the loop", func() {
		c := newCtx("golden-ext4")
		parent := c.ino("/dir")
		// In /dir/sub, c.txt now points to /dir.
		put32(c.dirRecord("/dir/sub", "c.txt"), uint32(parent))
		f := mustOpen(c.img)
		kids, err := f.ReadDir(c.ino("/dir/sub"), 10)
		Expect(err).To(Succeed())
		Expect(kids[0].Ino).To(Equal(parent))
		_, err = walkFS(f, defaultWalk)
		Expect(err).To(MatchError(errLoop))
	})

	It("catches an entry that points to its own directory", func() {
		c := newCtx("golden-ext4")
		put32(c.dirRecord("/dir/sub", "c.txt"), uint32(c.ino("/dir/sub")))
		_, err := walkFS(mustOpen(c.img), defaultWalk)
		Expect(err).To(MatchError(errLoop))
	})

	It("stops a walk that does not detect loops by the depth cap", func() {
		c := newCtx("golden-ext4")
		put32(c.dirRecord("/dir/sub", "c.txt"), uint32(c.ino("/dir")))
		f := mustOpen(c.img)
		// A walk without the visited set, as a careless caller writes it.
		depth := 0
		var walk func(ino uint64) error
		walk = func(ino uint64) error {
			if depth++; depth > 50 {
				return ErrLimit
			}
			kids, err := f.ReadDir(ino, 100)
			if err != nil {
				return err
			}
			for _, k := range kids {
				if in, err := f.Stat(k.Ino); err == nil && in.IsDir() {
					if err := walk(k.Ino); err != nil {
						return err
					}
				}
			}
			return nil
		}
		Expect(walk(rootIno)).To(MatchError(ErrLimit))
	})
})
