package ext4ro

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Errors that the reader returns. Detail is added with fmt.Errorf("%w: ...").
var (
	// ErrCorrupt means a structure on the disk is not valid.
	ErrCorrupt = errors.New("ext4ro: corrupt file system")
	// ErrNotDir means the inode is not a directory.
	ErrNotDir = errors.New("ext4ro: not a directory")
	// ErrNotRegular means the inode is not a regular file.
	ErrNotRegular = errors.New("ext4ro: not a regular file")
	// ErrNotSymlink means the inode is not a symbolic link.
	ErrNotSymlink = errors.New("ext4ro: not a symbolic link")
	// ErrUnsupported means the image uses a feature that the reader does not
	// support.
	ErrUnsupported = errors.New("ext4ro: unsupported feature")
	// ErrLimit means a cap on the work or on the size of a result was hit.
	ErrLimit = errors.New("ext4ro: limit reached")
)

// Caps for hostile images. The package comment explains each one.
const (
	maxDirBytes    = 256 << 20
	maxSymlink     = 4096
	maxXattrBytes  = 1 << 20
	maxNodeVisits  = 4 << 20
	maxExtentDepth = 5
	maxGroups      = 1 << 32
	// maxRun caps the length of a run of blocks, so that run arithmetic
	// cannot overflow. A run is a block count, and 2^40 blocks of 64 KiB
	// are more than int64 can address, so no real file is cut by this.
	maxRun = 1 << 40
)

// Layout constants of the superblock and of the inode.
const (
	sbOffset = 1024
	sbSize   = 1024
	sbMagic  = 0xEF53

	rootIno       = 2
	goodOldFirst  = 11
	goodOldInode  = 128
	minBlockShift = 10 // 1 KiB
	maxBlockLog   = 6  // 64 KiB
	iBlockLen     = 60 // bytes of i_block
)

// Feature bits that the reader looks at.
const (
	compatHasJournal   = 0x0004
	compatSparseSuper2 = 0x0200

	incompatCompression = 0x0001
	incompatFiletype    = 0x0002
	incompatRecover     = 0x0004
	incompatJournalDev  = 0x0008
	incompatMetaBG      = 0x0010
	incompatExtents     = 0x0040
	incompat64Bit       = 0x0080
	incompatMMP         = 0x0100
	incompatFlexBG      = 0x0200
	incompatEAInode     = 0x0400
	incompatDirdata     = 0x1000
	incompatCsumSeed    = 0x2000
	incompatLargedir    = 0x4000
	incompatInlineData  = 0x8000
	incompatEncrypt     = 0x10000
	incompatCasefold    = 0x20000

	incompatKnown = incompatFiletype | incompatMetaBG | incompatExtents |
		incompat64Bit | incompatMMP | incompatFlexBG | incompatEAInode |
		incompatCsumSeed | incompatLargedir | incompatInlineData |
		incompatCasefold

	roCompatSparseSuper = 0x0001
	roCompatHugeFile    = 0x0008
	roCompatBigalloc    = 0x0200
)

// Inode flags.
const (
	flagExtents    = 0x00080000
	flagInlineData = 0x10000000
	flagHugeFile   = 0x00040000
)

// File type bits of st_mode.
const (
	sIFMT  = 0o170000
	sIFDIR = 0o040000
	sIFREG = 0o100000
	sIFLNK = 0o120000
	sIFCHR = 0o020000
	sIFBLK = 0o060000
)

var le = binary.LittleEndian

// FS is a read-only view of an ext2, ext3 or ext4 image.
type FS struct {
	r    io.ReaderAt
	size int64

	bs        uint64 // block size in bytes
	blocks    uint64 // blocks in the file system
	firstData uint64 // s_first_data_block
	bpg       uint64 // blocks per group
	ipg       uint64 // inodes per group
	groups    uint64
	inodes    uint64 // s_inodes_count
	firstIno  uint64
	isz       uint64 // inode size
	descSize  uint64 // group descriptor size
	metaFirst uint64 // s_first_meta_bg

	compat, incompat, roCompat uint32
}

// Open reads the superblock of the image in r, which holds size bytes, and
// checks it. It reads nothing else. The superblock sizes are checked against
// size: a file system that is larger than the image is corrupt.
func Open(r io.ReaderAt, size int64) (*FS, error) {
	if r == nil {
		return nil, errors.New("ext4ro: nil reader")
	}
	if size < sbOffset+sbSize {
		return nil, fmt.Errorf("%w: image of %d bytes is smaller than a superblock", ErrCorrupt, size)
	}
	f := &FS{r: r, size: size}
	var sb [sbSize]byte
	if err := f.readAt(sb[:], sbOffset); err != nil {
		return nil, err
	}
	if m := le.Uint16(sb[0x38:]); m != sbMagic {
		return nil, fmt.Errorf("%w: superblock magic %#x", ErrCorrupt, m)
	}
	logBS := le.Uint32(sb[0x18:])
	if logBS > maxBlockLog {
		return nil, fmt.Errorf("%w: block size shift %d", ErrCorrupt, logBS)
	}
	f.bs = 1 << (minBlockShift + logBS)
	rev := le.Uint32(sb[0x4C:])
	switch rev {
	case 0:
		f.isz, f.firstIno = goodOldInode, goodOldFirst
	case 1:
		f.isz = uint64(le.Uint16(sb[0x58:]))
		f.firstIno = uint64(le.Uint32(sb[0x54:]))
		f.compat = le.Uint32(sb[0x5C:])
		f.incompat = le.Uint32(sb[0x60:])
		f.roCompat = le.Uint32(sb[0x64:])
	default:
		return nil, fmt.Errorf("%w: revision %d", ErrUnsupported, rev)
	}
	if err := f.checkFeatures(); err != nil {
		return nil, err
	}
	if f.isz < goodOldInode || f.isz > f.bs || f.isz&(f.isz-1) != 0 {
		return nil, fmt.Errorf("%w: inode size %d", ErrCorrupt, f.isz)
	}
	f.descSize = 32
	if f.incompat&incompat64Bit != 0 {
		f.descSize = uint64(le.Uint16(sb[0xFE:]))
		if f.descSize < 64 || f.descSize > 1024 || f.descSize > f.bs || f.descSize&(f.descSize-1) != 0 {
			return nil, fmt.Errorf("%w: group descriptor size %d", ErrCorrupt, f.descSize)
		}
	}
	f.blocks = uint64(le.Uint32(sb[0x04:]))
	if f.incompat&incompat64Bit != 0 {
		f.blocks |= uint64(le.Uint32(sb[0x150:])) << 32
	}
	f.firstData = uint64(le.Uint32(sb[0x14:]))
	wantFirst := uint64(0)
	if f.bs == 1024 {
		wantFirst = 1
	}
	if f.firstData != wantFirst {
		return nil, fmt.Errorf("%w: first data block %d for block size %d", ErrCorrupt, f.firstData, f.bs)
	}
	if f.blocks <= f.firstData {
		return nil, fmt.Errorf("%w: %d blocks", ErrCorrupt, f.blocks)
	}
	if f.blocks > uint64(size)/f.bs {
		return nil, fmt.Errorf("%w: file system of %d blocks does not fit the image of %d bytes", ErrCorrupt, f.blocks, size)
	}
	f.bpg = uint64(le.Uint32(sb[0x20:]))
	f.ipg = uint64(le.Uint32(sb[0x28:]))
	if f.bpg == 0 || f.bpg > 8*f.bs {
		return nil, fmt.Errorf("%w: %d blocks per group", ErrCorrupt, f.bpg)
	}
	if f.ipg == 0 || f.ipg > 8*f.bs || f.ipg < f.bs/f.isz {
		return nil, fmt.Errorf("%w: %d inodes per group", ErrCorrupt, f.ipg)
	}
	f.groups = (f.blocks - f.firstData + f.bpg - 1) / f.bpg
	if f.groups == 0 || f.groups > maxGroups {
		return nil, fmt.Errorf("%w: %d groups", ErrCorrupt, f.groups)
	}
	f.inodes = uint64(le.Uint32(sb[0x00:]))
	if f.inodes != f.groups*f.ipg {
		return nil, fmt.Errorf("%w: %d inodes, but %d groups of %d", ErrCorrupt, f.inodes, f.groups, f.ipg)
	}
	if f.firstIno < goodOldFirst || f.firstIno > f.inodes {
		return nil, fmt.Errorf("%w: first inode %d of %d", ErrCorrupt, f.firstIno, f.inodes)
	}
	f.metaFirst = uint64(le.Uint32(sb[0x104:]))
	if f.incompat&incompatMetaBG == 0 {
		// The table follows the superblock. With meta_bg each descriptor
		// block is checked when the reader uses it.
		n := (f.groups*f.descSize + f.bs - 1) / f.bs
		if n > f.blocks-f.firstData-1 {
			return nil, fmt.Errorf("%w: the group descriptor table of %d blocks is outside the file system", ErrCorrupt, n)
		}
	}
	return f, nil
}

func (f *FS) checkFeatures() error {
	switch {
	case f.incompat&incompatRecover != 0:
		return fmt.Errorf("%w: the journal needs recovery (needs_recovery is set); replay it before reading", ErrUnsupported)
	case f.incompat&incompatJournalDev != 0:
		return fmt.Errorf("%w: journal device", ErrUnsupported)
	case f.incompat&incompatCompression != 0:
		return fmt.Errorf("%w: compression", ErrUnsupported)
	case f.incompat&incompatEncrypt != 0:
		return fmt.Errorf("%w: encryption", ErrUnsupported)
	case f.incompat&incompatDirdata != 0:
		return fmt.Errorf("%w: dirdata", ErrUnsupported)
	case f.roCompat&roCompatBigalloc != 0:
		return fmt.Errorf("%w: bigalloc", ErrUnsupported)
	case f.compat&compatSparseSuper2 != 0 && f.incompat&incompatMetaBG != 0:
		return fmt.Errorf("%w: sparse_super2 with meta_bg", ErrUnsupported)
	case f.incompat&^incompatKnown != 0:
		return fmt.Errorf("%w: incompat features %#x", ErrUnsupported, f.incompat&^incompatKnown)
	}
	return nil
}

// BlockSize returns the block size in bytes.
func (f *FS) BlockSize() int { return int(f.bs) }

// InodeCount returns s_inodes_count.
func (f *FS) InodeCount() uint64 { return f.inodes }

// FirstIno returns s_first_ino: the first inode that is not reserved. It is
// 11 for a revision 0 file system.
func (f *FS) FirstIno() uint64 { return f.firstIno }

// InodeInRange reports whether ino can be an inode of this file system: the
// root (2), or an inode from FirstIno up to InodeCount. A directory entry
// that points outside this range is corrupt.
func (f *FS) InodeInRange(ino uint64) bool {
	return ino == rootIno || (ino >= f.firstIno && ino <= f.inodes)
}

// readAt fills p from the image at off. The read must be inside the image.
func (f *FS) readAt(p []byte, off uint64) error {
	size := uint64(f.size)
	if off > size || uint64(len(p)) > size-off {
		return fmt.Errorf("%w: read of %d bytes at %d is outside the image of %d bytes", ErrCorrupt, len(p), off, size)
	}
	if len(p) == 0 {
		return nil
	}
	n, err := f.r.ReadAt(p, int64(off))
	if n == len(p) {
		return nil
	}
	if err == nil || errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: short read at %d (%d of %d bytes)", ErrCorrupt, off, n, len(p))
	}
	return fmt.Errorf("ext4ro: read at %d: %w", off, err)
}

// readBlock reads block blk, which must be inside the file system.
func (f *FS) readBlock(blk uint64) ([]byte, error) {
	if blk >= f.blocks {
		return nil, fmt.Errorf("%w: block %d is outside the file system of %d blocks", ErrCorrupt, blk, f.blocks)
	}
	b := make([]byte, f.bs)
	if err := f.readAt(b, blk*f.bs); err != nil {
		return nil, err
	}
	return b, nil
}

// hasSuper reports whether group g holds a superblock backup. It is only
// needed to place the descriptor blocks of a meta_bg file system.
func (f *FS) hasSuper(g uint64) bool {
	if f.roCompat&roCompatSparseSuper == 0 || g <= 1 {
		return true
	}
	if g&1 == 0 {
		return false
	}
	for _, p := range [...]uint64{3, 5, 7} {
		x := p
		for x < g {
			x *= p
		}
		if x == g {
			return true
		}
	}
	return false
}

// inodeTable returns the first block of the inode table of group g, from the
// group descriptor.
func (f *FS) inodeTable(g uint64) (uint64, error) {
	perBlock := f.bs / f.descSize
	db := g / perBlock
	var blk uint64
	if f.incompat&incompatMetaBG != 0 && db >= f.metaFirst {
		first := db * perBlock
		blk = f.firstData + first*f.bpg
		if f.hasSuper(first) {
			blk++
		}
	} else {
		blk = f.firstData + 1 + db
	}
	if blk >= f.blocks {
		return 0, fmt.Errorf("%w: group descriptor block %d is outside the file system", ErrCorrupt, blk)
	}
	var d [64]byte
	n := min(f.descSize, 64)
	if err := f.readAt(d[:n], blk*f.bs+(g%perBlock)*f.descSize); err != nil {
		return 0, err
	}
	t := uint64(le.Uint32(d[0x08:]))
	if f.descSize >= 64 {
		t |= uint64(le.Uint32(d[0x28:])) << 32
	}
	tblBlocks := (f.ipg*f.isz + f.bs - 1) / f.bs
	if t < f.firstData+1 || t >= f.blocks || tblBlocks > f.blocks-t {
		return 0, fmt.Errorf("%w: inode table of group %d at block %d is outside the file system", ErrCorrupt, g, t)
	}
	return t, nil
}

// inode is a parsed inode with its raw bytes.
type inode struct {
	Inode
	raw     []byte // the whole on-disk inode
	extra   uint64 // i_extra_isize, checked
	fileACL uint64
	iblocks uint64 // i_blocks in 512-byte units, as stored
}

// iblock returns the 60 bytes of i_block.
func (n *inode) iblock() []byte { return n.raw[0x28 : 0x28+iBlockLen] }

func (n *inode) typ() uint16 { return n.Mode & sIFMT }

// load reads and parses inode ino.
func (f *FS) load(ino uint64) (*inode, error) {
	if ino == 0 || ino > f.inodes {
		return nil, fmt.Errorf("%w: inode %d is outside 1 to %d", ErrCorrupt, ino, f.inodes)
	}
	g := (ino - 1) / f.ipg
	idx := (ino - 1) % f.ipg
	t, err := f.inodeTable(g)
	if err != nil {
		return nil, err
	}
	raw := make([]byte, f.isz)
	if err := f.readAt(raw, t*f.bs+idx*f.isz); err != nil {
		return nil, err
	}
	n := &inode{raw: raw}
	n.Ino = ino
	n.Mode = le.Uint16(raw[0x00:])
	n.UID = uint32(le.Uint16(raw[0x02:])) | uint32(le.Uint16(raw[0x78:]))<<16
	n.GID = uint32(le.Uint16(raw[0x18:])) | uint32(le.Uint16(raw[0x7A:]))<<16
	n.Links = le.Uint16(raw[0x1A:])
	n.Flags = le.Uint32(raw[0x20:])
	n.Generation = le.Uint32(raw[0x64:])
	n.Version = uint64(le.Uint32(raw[0x24:]))
	n.BlockWords = [2]uint32{le.Uint32(raw[0x28:]), le.Uint32(raw[0x2C:])}
	size := uint64(le.Uint32(raw[0x04:]))
	if n.typ() == sIFREG || f.incompat&incompatLargedir != 0 {
		size |= uint64(le.Uint32(raw[0x6C:])) << 32
	}
	if size > 1<<63-1 {
		return nil, fmt.Errorf("%w: inode %d has size %d", ErrCorrupt, ino, size)
	}
	n.Size = int64(size)
	n.fileACL = uint64(le.Uint32(raw[0x68:]))
	n.iblocks = uint64(le.Uint32(raw[0x1C:]))
	if f.incompat&incompat64Bit != 0 {
		n.fileACL |= uint64(le.Uint16(raw[0x76:])) << 32
	}
	if f.roCompat&roCompatHugeFile != 0 {
		n.iblocks |= uint64(le.Uint16(raw[0x74:])) << 32
	}
	if f.isz > goodOldInode {
		n.extra = uint64(le.Uint16(raw[0x80:]))
		if n.extra&3 != 0 || n.extra > f.isz-goodOldInode {
			return nil, fmt.Errorf("%w: inode %d has i_extra_isize %d", ErrCorrupt, ino, n.extra)
		}
	}
	// Each extra field is read only when i_extra_isize covers it. The
	// offsets are 0x84 (ctime), 0x88 (mtime), 0x8C (atime) and 0x98
	// (i_version_hi).
	field := func(off uint64) (uint32, bool) {
		if n.extra+goodOldInode < off+4 {
			return 0, false
		}
		return le.Uint32(raw[off:]), true
	}
	var ae uint32
	n.CtimeExtra, _ = field(0x84)
	n.MtimeExtra, _ = field(0x88)
	ae, _ = field(0x8C)
	if hi, ok := field(0x98); ok {
		n.Version |= uint64(hi) << 32
	}
	n.AtimeSec = epochTime(le.Uint32(raw[0x08:]), ae)
	n.CtimeSec = epochTime(le.Uint32(raw[0x0C:]), n.CtimeExtra)
	n.MtimeSec = epochTime(le.Uint32(raw[0x10:]), n.MtimeExtra)
	if t := n.typ(); t == sIFCHR || t == sIFBLK {
		n.Rdev = decodeDev(n.BlockWords)
	}
	return n, nil
}

// epochTime combines a signed 32-bit time with the two epoch bits of the
// extra field, as the kernel does.
func epochTime(base, extra uint32) int64 {
	return int64(int32(base)) + int64(extra&3)<<32
}

// decodeDev decodes a device number. The old encoding (8-bit major and
// minor) is in i_block[0]. When it is zero, the new encoding is in
// i_block[1]. The result is major<<20 | minor, as the kernel stores it.
func decodeDev(w [2]uint32) uint32 {
	if w[0] != 0 {
		return (w[0]>>8&0xFF)<<20 | w[0]&0xFF
	}
	d := w[1]
	major := (d & 0xFFF00) >> 8
	minor := d&0xFF | (d>>12)&0xFFF00
	return major<<20 | minor
}

// Stat returns the inode ino. Any inode from 1 to InodeCount can be read.
// Whether a directory entry may point to it is a different rule, see
// InodeInRange.
func (f *FS) Stat(ino uint64) (Inode, error) {
	n, err := f.load(ino)
	if err != nil {
		return Inode{}, err
	}
	return n.Inode, nil
}

// Inode holds the fields of an inode that callers use.
type Inode struct {
	Ino  uint64
	Mode uint16 // st_mode, with the file type bits
	// UID and GID are 32 bits wide: the low half from the base inode and the
	// high half from the osd2 area.
	UID, GID uint32
	Size     int64
	Links    uint16
	Flags    uint32
	// Generation is i_generation.
	Generation uint32
	// Version is i_version as debugfs shows it: the low 32 bits from osd1
	// and the high 32 bits from i_version_hi when the inode has it.
	Version uint64
	// The times are seconds since the epoch. The epoch bits of the extra
	// field are included.
	CtimeSec, MtimeSec, AtimeSec int64
	// CtimeExtra and MtimeExtra are the raw extra fields (nanoseconds and
	// epoch bits). They are 0 when the inode is 128 bytes, or when
	// i_extra_isize does not cover them.
	CtimeExtra, MtimeExtra uint32
	// Rdev is the device number of a character or block device, as
	// major<<20 | minor. It is 0 for other types.
	Rdev uint32
	// BlockWords is i_block[0] and i_block[1], raw.
	BlockWords [2]uint32
}

// IsDir reports whether the inode is a directory.
func (n Inode) IsDir() bool { return n.Mode&sIFMT == sIFDIR }

// IsRegular reports whether the inode is a regular file.
func (n Inode) IsRegular() bool { return n.Mode&sIFMT == sIFREG }

// IsSymlink reports whether the inode is a symbolic link.
func (n Inode) IsSymlink() bool { return n.Mode&sIFMT == sIFLNK }

// IsWhiteout reports an overlayfs whiteout: a character device with the
// device number 0:0, which is i_block[0] and i_block[1] both zero.
func (n Inode) IsWhiteout() bool {
	return n.Mode&sIFMT == sIFCHR && n.BlockWords[0] == 0 && n.BlockWords[1] == 0
}

// DevMajor returns the major number of Rdev.
func (n Inode) DevMajor() uint32 { return n.Rdev >> 20 }

// DevMinor returns the minor number of Rdev.
func (n Inode) DevMinor() uint32 { return n.Rdev & (1<<20 - 1) }
