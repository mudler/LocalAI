package ext4mk_test

import (
	"encoding/binary"
	"os"
	"sort"

	. "github.com/onsi/gomega"
)

// A reader of the on-disk format that shares no code with the formatter, so
// that a mistake in the writer is not repeated by the check.

const (
	bs  = 4096
	bpg = 32768
)

type group struct {
	blockBitmap, inodeBitmap, inodeTable uint32
	freeBlocks, freeInodes, dirs, flags  uint16
}

type fsys struct {
	f                          *os.File
	sb                         []byte
	blocks, inodes             uint32
	freeBlocks, freeInodes     uint32
	ipg                        uint32
	groups                     uint32
	gdtBlocks                  uint32
	gd                         []group
	compat, incompat, roCompat uint32
}

func u16(b []byte) uint16 { return binary.LittleEndian.Uint16(b) }
func u32(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }

func readAt(f *os.File, off int64, n int) []byte {
	b := make([]byte, n)
	_, err := f.ReadAt(b, off)
	Expect(err).NotTo(HaveOccurred(), "read %d bytes at %d", n, off)
	return b
}

func parse(path string) *fsys {
	f, err := os.Open(path)
	Expect(err).NotTo(HaveOccurred())
	s := &fsys{f: f}
	s.sb = readAt(f, 1024, 1024)
	sb := s.sb
	s.inodes, s.blocks = u32(sb[0:]), u32(sb[4:])
	s.freeBlocks, s.freeInodes = u32(sb[12:]), u32(sb[16:])
	s.ipg = u32(sb[40:])
	s.compat, s.incompat, s.roCompat = u32(sb[92:]), u32(sb[96:]), u32(sb[100:])
	s.groups = (s.blocks + bpg - 1) / bpg
	s.gdtBlocks = (s.groups + 127) / 128
	raw := readAt(f, bs, int(s.gdtBlocks)*bs)
	for n := uint32(0); n < s.groups; n++ {
		d := raw[n*32:]
		s.gd = append(s.gd, group{u32(d[0:]), u32(d[4:]), u32(d[8:]), u16(d[12:]), u16(d[14:]), u16(d[16:]), u16(d[18:])})
	}
	return s
}

func (s *fsys) close() { _ = s.f.Close() }

func (s *fsys) block(n uint64) []byte { return readAt(s.f, int64(n)*bs, bs) } // #nosec G115

func (s *fsys) groupBlocks(n uint32) uint32 {
	start := uint64(n) * bpg
	return uint32(min(uint64(s.blocks)-start, bpg))
}

// backupGroups lists the groups that sparse_super gives a backup, by
// generating them from the definition: 0, 1 and the powers of 3, 5 and 7.
func backupGroups(groups uint32) []uint32 {
	set := map[uint32]bool{0: true}
	if groups > 1 {
		set[1] = true
	}
	for _, base := range []uint64{3, 5, 7} {
		for v := base; v < uint64(groups); v *= base {
			set[uint32(v)] = true
		}
	}
	out := make([]uint32, 0, len(set))
	for g := range set {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func popcountZero(b []byte, from, to uint32) uint32 {
	var n uint32
	for i := from; i < to; i++ {
		if b[i/8]&(1<<(i%8)) == 0 {
			n++
		}
	}
	return n
}

// inode reads inode number n, which must be in group 0.
func (s *fsys) inode(n uint32) []byte {
	return readAt(s.f, int64(s.gd[0].inodeTable)*bs+int64(n-1)*256, 256)
}
