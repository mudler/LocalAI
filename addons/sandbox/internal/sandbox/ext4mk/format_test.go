package ext4mk_test

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ext4mk"
)

const (
	MiB = int64(1) << 20
	GiB = int64(1) << 30
)

var fixedNow = func() time.Time { return time.Date(2026, 11, 13, 10, 0, 0, 123, time.UTC) }

var fixedUUID = [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}

// newImage formats a new image in a temporary directory and returns its path.
func newImage(o ext4mk.Options) string {
	path := filepath.Join(GinkgoT().TempDir(), "disk.img")
	Expect(ext4mk.FormatFile(path, o)).To(Succeed())
	return path
}

// expectValid checks every invariant of the output without any tool.
func expectValid(path string, asked int64, uid, gid uint32) *fsys {
	s := parse(path)
	DeferCleanup(s.close)
	sb := s.sb

	Expect(u16(sb[56:])).To(BeEquivalentTo(0xEF53), "magic")
	Expect(u32(sb[24:])).To(BeEquivalentTo(2), "block size 4096")
	Expect(u32(sb[32:])).To(BeEquivalentTo(bpg))
	Expect(u16(sb[88:])).To(BeEquivalentTo(256), "inode size")
	Expect(u32(sb[84:])).To(BeEquivalentTo(11), "first inode")
	Expect(u16(sb[58:])).To(BeEquivalentTo(1), "state")
	Expect(u16(sb[60:])).To(BeEquivalentTo(1), "errors")
	Expect(u32(sb[76:])).To(BeEquivalentTo(1), "rev level")
	Expect(u16(sb[54:])).To(BeEquivalentTo(0xffff), "max mount count")
	Expect(u32(sb[72:])).To(BeZero(), "creator os")
	Expect(u32(sb[256:])).To(BeEquivalentTo(0x0c), "default mount options")
	Expect(u16(sb[206:])).To(BeZero(), "reserved gdt blocks")
	Expect(u16(sb[348:])).To(BeEquivalentTo(32))
	Expect(u16(sb[350:])).To(BeEquivalentTo(32))
	Expect(sb[372]).To(BeZero(), "log groups per flex")
	Expect(u32(sb[8:])).To(BeZero(), "reserved blocks")
	Expect(s.compat).To(BeEquivalentTo(0x08 | 0x20))
	Expect(s.incompat).To(BeEquivalentTo(0x02 | 0x40))
	Expect(s.roCompat).To(BeEquivalentTo(0x01|0x02|0x08|0x20|0x40), "ro_compat")
	Expect(u32(sb[48:])).To(Equal(u32(sb[264:])), "wtime and mkfs time")

	// The size: rounded down to a block and at most one group short.
	size := uint64(s.blocks) * bs
	Expect(size).To(BeNumerically("<=", uint64(asked)))
	Expect(uint64(asked) - size).To(BeNumerically("<", uint64(bpg)*bs+bs))
	Expect(s.groups * s.ipg).To(Equal(s.inodes))
	Expect(s.ipg%16).To(BeZero(), "inode tables fill whole blocks")
	Expect(s.ipg).To(BeNumerically("<=", bpg))
	Expect(s.gd).To(HaveLen(int(s.groups)))
	last := s.groupBlocks(s.groups - 1)
	Expect(last).To(BeNumerically(">", 0))

	// Layout: every range lies in the file system and none overlaps.
	type span struct {
		start, n uint64
		what     string
	}
	var spans []span
	backups := backupGroups(s.groups)
	hasBackup := map[uint32]bool{}
	for _, g := range backups {
		hasBackup[g] = true
	}
	itb := uint64(s.ipg) / 16
	var freeB, freeI uint32
	var dirs int
	for n := uint32(0); n < s.groups; n++ {
		g := s.gd[n]
		start := uint64(n) * bpg
		pos := start
		if hasBackup[n] {
			spans = append(spans, span{start, 1 + uint64(s.gdtBlocks), "backup"})
			pos += 1 + uint64(s.gdtBlocks)
			// The backup is a superblock and an identical descriptor table.
			off := int64(start) * bs
			if n == 0 {
				off += 1024
			}
			bsb := readAt(s.f, off, 1024)
			Expect(u16(bsb[56:])).To(BeEquivalentTo(0xEF53), "backup magic, group %d", n)
			Expect(u16(bsb[90:])).To(BeEquivalentTo(n), "backup group number")
			Expect(u32(bsb[0:])).To(Equal(s.inodes))
			Expect(u32(bsb[4:])).To(Equal(s.blocks))
			Expect(readAt(s.f, int64(start+1)*bs, int(s.gdtBlocks)*bs)).
				To(Equal(readAt(s.f, bs, int(s.gdtBlocks)*bs)), "descriptor table copy in group %d", n)
		}
		Expect(uint64(g.blockBitmap)).To(Equal(pos), "block bitmap of group %d", n)
		Expect(uint64(g.inodeBitmap)).To(Equal(pos+1), "inode bitmap of group %d", n)
		Expect(uint64(g.inodeTable)).To(Equal(pos+2), "inode table of group %d", n)
		spans = append(spans, span{pos, 1, "block bitmap"}, span{pos + 1, 1, "inode bitmap"}, span{pos + 2, itb, "inode table"})

		// The bitmaps agree with the descriptor counts, and the padding
		// bits are set.
		in := s.groupBlocks(n)
		bb := s.block(uint64(g.blockBitmap))
		Expect(popcountZero(bb, 0, in)).To(BeEquivalentTo(g.freeBlocks), "free blocks of group %d", n)
		Expect(popcountZero(bb, in, bpg)).To(BeZero(), "block bitmap padding of group %d", n)
		ib := s.block(uint64(g.inodeBitmap))
		Expect(popcountZero(ib, 0, s.ipg)).To(BeEquivalentTo(g.freeInodes), "free inodes of group %d", n)
		Expect(popcountZero(ib, s.ipg, bs*8)).To(BeZero(), "inode bitmap padding of group %d", n)
		// The metadata blocks are marked in use.
		Expect(popcountZero(bb, 0, uint32(pos-start+2+itb))).To(BeZero(), "metadata of group %d in use", n)
		freeB += uint32(g.freeBlocks)
		freeI += uint32(g.freeInodes)
		dirs += int(g.dirs)
	}
	Expect(freeB).To(Equal(s.freeBlocks), "free blocks add up")
	Expect(freeI).To(Equal(s.freeInodes), "free inodes add up")
	Expect(dirs).To(Equal(2), "directories")
	Expect(s.gd[0].dirs).To(BeEquivalentTo(2))
	Expect(s.freeInodes).To(Equal(s.inodes - 11))

	// Inodes 1 to 10 are used in the bitmap, 11 too.
	ib0 := s.block(uint64(s.gd[0].inodeBitmap))
	Expect(popcountZero(ib0, 0, 11)).To(BeZero())
	Expect(popcountZero(ib0, 11, s.ipg)).To(Equal(s.ipg - 11))

	// Root and lost+found.
	root := s.inode(2)
	Expect(u16(root[0:])).To(BeEquivalentTo(0o40755))
	Expect(uint32(u16(root[2:])) | uint32(u16(root[120:]))<<16).To(Equal(uid))
	Expect(uint32(u16(root[24:])) | uint32(u16(root[122:]))<<16).To(Equal(gid))
	Expect(u16(root[26:])).To(BeEquivalentTo(3), "root links")
	Expect(u32(root[4:])).To(BeEquivalentTo(bs))
	Expect(u32(root[28:])).To(BeEquivalentTo(8))
	Expect(u32(root[32:])).To(BeEquivalentTo(0x80000), "extents flag")
	Expect(u16(root[128:])).To(BeEquivalentTo(32), "extra isize")
	lost := s.inode(11)
	Expect(u16(lost[0:])).To(BeEquivalentTo(0o40700))
	Expect(u16(lost[2:])).To(BeZero())
	Expect(u16(lost[26:])).To(BeEquivalentTo(2), "lost+found links")
	Expect(u32(lost[32:])).To(BeEquivalentTo(0x80000))
	for _, n := range []uint32{1, 3, 4, 5, 6, 7, 8, 9, 10} {
		Expect(s.inode(n)).To(Equal(make([]byte, 256)), "reserved inode %d", n)
	}
	// The times: second fields equal, the extra field holds the nanoseconds.
	Expect(u32(root[8:])).To(Equal(u32(root[16:])))
	Expect(u32(root[144:])).To(Equal(u32(root[8:])), "crtime")

	rootBlock, lostBlock := extentBlock(root), extentBlock(lost)
	spans = append(spans, span{rootBlock, 1, "root dir"}, span{lostBlock, 1, "lost+found dir"})
	expectDir(s.block(rootBlock), []dirent{{2, "."}, {2, ".."}, {11, "lost+found"}})
	expectDir(s.block(lostBlock), []dirent{{11, "."}, {2, ".."}})
	Expect(popcountZero(s.block(uint64(s.gd[0].blockBitmap)), uint32(rootBlock), uint32(lostBlock)+1)).To(BeZero(), "dir blocks in use")

	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	for i, sp := range spans {
		Expect(sp.start+sp.n).To(BeNumerically("<=", uint64(s.blocks)), sp.what)
		if i > 0 {
			prev := spans[i-1]
			Expect(prev.start+prev.n).To(BeNumerically("<=", sp.start), "%s overlaps %s", prev.what, sp.what)
		}
	}
	// A block outside every group's backup range holds no superblock.
	for n := uint32(0); n < s.groups; n++ {
		if n > 1 && !hasBackup[n] {
			Expect(readAt(s.f, int64(n)*bpg*bs, bs)[56:58]).NotTo(Equal([]byte{0x53, 0xEF}), "group %d has no backup", n)
		}
	}
	return s
}

// extentBlock returns the block of the single extent in an inode.
func extentBlock(ino []byte) uint64 {
	Expect(u16(ino[40:])).To(BeEquivalentTo(0xF30A), "extent magic")
	Expect(u16(ino[42:])).To(BeEquivalentTo(1), "entries")
	Expect(u16(ino[44:])).To(BeEquivalentTo(4), "max entries")
	Expect(u16(ino[46:])).To(BeZero(), "depth")
	Expect(u32(ino[52:])).To(BeZero(), "logical block")
	Expect(u16(ino[56:])).To(BeEquivalentTo(1), "length")
	return uint64(u16(ino[58:]))<<32 | uint64(u32(ino[60:]))
}

type dirent struct {
	ino  uint32
	name string
}

// expectDir walks a directory block and checks that the entries are well
// formed, fill the block and have the wanted inodes and names in order.
func expectDir(b []byte, want []dirent) {
	off := 0
	var got []dirent
	for off < bs {
		recLen := int(u16(b[off+4:]))
		nameLen := int(b[off+6])
		Expect(recLen % 4).To(BeZero())
		Expect(recLen).To(BeNumerically(">=", 8+nameLen))
		Expect(off + recLen).To(BeNumerically("<=", bs))
		Expect(b[off+7]).To(BeEquivalentTo(2), "file type: directory")
		got = append(got, dirent{u32(b[off:]), string(b[off+8 : off+8+nameLen])})
		off += recLen
	}
	Expect(off).To(Equal(bs), "entries fill the block, with no checksum tail")
	Expect(got).To(Equal(want))
}

var _ = Describe("Format", func() {
	DescribeTable("a file system with every invariant",
		func(size int64) {
			path := newImage(ext4mk.Options{Size: size, RootUID: 1000, RootGID: 1000, Now: fixedNow})
			expectValid(path, size, 1000, 1000)
		},
		Entry("16 MiB, the minimum", 16*MiB),
		Entry("33 MiB", 33*MiB),
		Entry("127 MiB", 127*MiB),
		Entry("128 MiB, one full group", 128*MiB),
		Entry("129 MiB, a last group too small to keep", 129*MiB),
		Entry("131 MiB, a last group that stays", 131*MiB),
		Entry("1 GiB", GiB),
		Entry("1 GiB and one block", GiB+4096),
		Entry("1 GiB and a part of a block", GiB+100),
		Entry("4 GiB", 4*GiB),
	)

	It("applies the backup rule of sparse_super on a file system with many groups", func() {
		// 40 GiB has 320 groups. The backups are 0, 1 and the powers of 3, 5
		// and 7 below 320.
		path := newImage(ext4mk.Options{Size: 40 * GiB, Now: fixedNow})
		s := expectValid(path, 40*GiB, 0, 0)
		Expect(s.groups).To(BeEquivalentTo(320))
		Expect(backupGroups(s.groups)).To(Equal([]uint32{0, 1, 3, 5, 7, 9, 25, 27, 49, 81, 125, 243}))
	})

	It("stores the label and the UUID", func() {
		path := newImage(ext4mk.Options{Size: 16 * MiB, Label: "upper", UUID: fixedUUID})
		s := parse(path)
		DeferCleanup(s.close)
		Expect(s.sb[104:120]).To(Equal(fixedUUID[:]))
		Expect(string(bytes.TrimRight(s.sb[120:136], "\x00"))).To(Equal("upper"))
	})

	It("makes a different UUID each time when none is given", func() {
		a := parse(newImage(ext4mk.Options{Size: 16 * MiB}))
		b := parse(newImage(ext4mk.Options{Size: 16 * MiB}))
		DeferCleanup(a.close)
		DeferCleanup(b.close)
		Expect(a.sb[104:120]).NotTo(Equal(b.sb[104:120]))
		Expect(a.sb[104:120]).NotTo(Equal(make([]byte, 16)))
	})

	It("applies the root owner above 16 bits", func() {
		path := newImage(ext4mk.Options{Size: 16 * MiB, RootUID: 0x12345, RootGID: 0x2abcd})
		expectValid(path, 16*MiB, 0x12345, 0x2abcd)
	})

	It("encodes the time past 2038 with the epoch bits", func() {
		late := func() time.Time { return time.Date(2300, 1, 1, 0, 0, 0, 7, time.UTC) }
		s := parse(newImage(ext4mk.Options{Size: 16 * MiB, Now: late}))
		DeferCleanup(s.close)
		ino := s.inode(2)
		extra := u32(ino[132:])
		Expect(extra >> 2).To(BeEquivalentTo(7))
		Expect(extra & 3).To(BeEquivalentTo(late().Unix() >> 32 & 3))
		Expect(extra & 3).NotTo(BeZero())
	})

	It("gives byte-identical images for the same options", func() {
		o := ext4mk.Options{Size: 300 * MiB, UUID: fixedUUID, Now: fixedNow, RootUID: 1000, RootGID: 1000, Label: "x"}
		a, err := os.ReadFile(newImage(o))
		Expect(err).NotTo(HaveOccurred())
		b, err := os.ReadFile(newImage(o))
		Expect(err).NotTo(HaveOccurred())
		Expect(bytes.Equal(a, b)).To(BeTrue(), "two runs differ")
	})

	It("stays small and fast for a 64 GiB image", func() {
		path := filepath.Join(GinkgoT().TempDir(), "big.img")
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		start := time.Now()
		Expect(ext4mk.FormatFile(path, ext4mk.Options{Size: 64 * GiB, Now: fixedNow})).To(Succeed())
		took := time.Since(start)
		runtime.ReadMemStats(&after)
		GinkgoWriter.Printf("64 GiB: %v, %d KiB allocated\n", took, (after.TotalAlloc-before.TotalAlloc)>>10)
		Expect(took).To(BeNumerically("<", 5*time.Second))
		Expect(after.TotalAlloc - before.TotalAlloc).To(BeNumerically("<", 16<<20))
		expectValid(path, 64*GiB, 0, 0)
	})

	It("writes through a plain WriterAt and never reads", func() {
		w := &recorder{}
		Expect(ext4mk.Format(w, ext4mk.Options{Size: 1 * GiB, Now: fixedNow})).To(Succeed())
		Expect(w.writes).NotTo(BeEmpty())
		Expect(w.maxEnd).To(BeNumerically("<=", GiB))
		Expect(w.bytes).To(BeNumerically("<", 4<<20), "only metadata is written")
	})

	Describe("validation", func() {
		var w *recorder
		BeforeEach(func() { w = &recorder{} })

		It("rejects a size below the minimum", func() {
			Expect(ext4mk.Format(w, ext4mk.Options{Size: 16*MiB - 1})).To(MatchError(ContainSubstring("below the minimum")))
			Expect(ext4mk.Format(w, ext4mk.Options{})).To(MatchError(ContainSubstring("below the minimum")))
			Expect(ext4mk.Format(w, ext4mk.Options{Size: -1})).To(HaveOccurred())
		})
		It("rejects a size above the maximum", func() {
			Expect(ext4mk.Format(w, ext4mk.Options{Size: ext4mk.MaxSize + 1})).To(MatchError(ContainSubstring("above the maximum")))
		})
		It("accepts the maximum, rounded down to the block limit", func() {
			Expect(ext4mk.Format(w, ext4mk.Options{Size: ext4mk.MaxSize, Now: fixedNow})).To(Succeed())
			Expect(w.maxEnd).To(BeNumerically("<=", ext4mk.MaxSize))
		})
		It("rejects a long label", func() {
			Expect(ext4mk.Format(w, ext4mk.Options{Size: 16 * MiB, Label: "12345678901234567"})).To(MatchError(ContainSubstring("label")))
			Expect(ext4mk.Format(w, ext4mk.Options{Size: 16 * MiB, Label: "1234567890123456"})).To(Succeed())
		})
		It("rejects a nil writer", func() {
			Expect(ext4mk.Format(nil, ext4mk.Options{Size: 16 * MiB})).To(HaveOccurred())
		})
		It("reports a write error", func() {
			Expect(ext4mk.Format(failer{}, ext4mk.Options{Size: 16 * MiB})).To(HaveOccurred())
		})
		It("refuses to overwrite a file and removes the file it made on error", func() {
			dir := GinkgoT().TempDir()
			existing := filepath.Join(dir, "a")
			Expect(os.WriteFile(existing, []byte("keep"), 0o600)).To(Succeed())
			Expect(ext4mk.FormatFile(existing, ext4mk.Options{Size: 16 * MiB})).NotTo(Succeed())
			data, err := os.ReadFile(existing)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(Equal("keep"))

			bad := filepath.Join(dir, "b")
			Expect(ext4mk.FormatFile(bad, ext4mk.Options{Size: 1})).NotTo(Succeed())
			Expect(bad).NotTo(BeAnExistingFile())
		})
		It("creates the file with mode 0600", func() {
			path := newImage(ext4mk.Options{Size: 16 * MiB})
			st, err := os.Stat(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(st.Mode().Perm()).To(Equal(os.FileMode(0o600)))
			Expect(st.Size()).To(Equal(16 * MiB))
		})
	})
})

// recorder is a WriterAt that remembers where the writes went.
type recorder struct {
	writes []int64
	maxEnd int64
	bytes  int64
}

func (r *recorder) WriteAt(p []byte, off int64) (int, error) {
	r.writes = append(r.writes, off)
	r.bytes += int64(len(p))
	r.maxEnd = max(r.maxEnd, off+int64(len(p)))
	return len(p), nil
}

type failer struct{}

func (failer) WriteAt([]byte, int64) (int, error) { return 0, os.ErrInvalid }
