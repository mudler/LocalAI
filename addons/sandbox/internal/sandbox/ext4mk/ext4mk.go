package ext4mk

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

const (
	// MinSize is the smallest file system Format accepts, in bytes.
	MinSize int64 = 16 << 20
	// MaxSize is the largest file system Format accepts, in bytes. A file
	// system without 64bit holds at most 2^32-1 blocks, so exactly this size
	// is rounded down by one block.
	MaxSize int64 = 16 << 40

	blockSize      = 4096
	blocksPerGroup = 32768
	inodeSize      = 256
	inodesPerBlock = blockSize / inodeSize
	descSize       = 32
	descPerBlock   = blockSize / descSize
	bytesPerInode  = 16384 // the mke2fs default inode ratio
	maxBlocks      = 1<<32 - 1
	labelSize      = 16

	// A last group that cannot hold its own metadata plus this many data
	// blocks is dropped, as mke2fs does.
	minLastGroupData = 50

	rootIno      = 2
	lostFoundIno = 11
	firstIno     = 11
)

// Options describes the file system to make.
type Options struct {
	// Size is the size of the file system in bytes. It is required. It is
	// rounded down to a multiple of the block size. A short last block group
	// that cannot hold its own metadata is dropped, so the file system can be
	// slightly smaller than Size.
	Size int64
	// RootUID and RootGID own the root directory. The mode is 0755.
	RootUID, RootGID uint32
	// Label is the volume label, up to 16 bytes. It is optional.
	Label string
	// UUID is the volume UUID. The zero value means a random one.
	UUID [16]byte
	// Now returns the time stamped on the file system. The default is
	// time.Now. Tests set it to get a reproducible image.
	Now func() time.Time
}

// geometry is the shape of the file system, derived from its size.
type geometry struct {
	blocks       uint64 // total blocks, after any trimming
	groups       uint32
	ipg          uint32 // inodes per group
	itableBlocks uint32 // inode table blocks per group
	gdtBlocks    uint32 // group descriptor table blocks
}

// newGeometry picks the group count and the inode count for a number of
// blocks. It follows mke2fs: the inode count comes from the size and the
// default ratio, each group holds the same number of inodes, and a last group
// that is too small for its own metadata is cut off. Cutting a group changes
// the size, so the numbers are computed again.
func newGeometry(blocks uint64) (geometry, error) {
	for {
		g := geometry{blocks: blocks}
		g.groups = uint32((blocks + blocksPerGroup - 1) / blocksPerGroup) // #nosec G115 -- blocks is at most maxBlocks, so there are fewer than 2^17 groups
		inodes := blocks * blockSize / bytesPerInode
		ipg := (inodes + uint64(g.groups) - 1) / uint64(g.groups)
		ipg = (ipg + inodesPerBlock - 1) / inodesPerBlock * inodesPerBlock
		ipg = min(max(ipg, inodesPerBlock), blocksPerGroup)
		g.ipg = uint32(ipg)
		g.itableBlocks = g.ipg / inodesPerBlock
		g.gdtBlocks = (g.groups + descPerBlock - 1) / descPerBlock
		if g.groups == 1 {
			if uint64(g.overhead(0))+minLastGroupData > blocks {
				return geometry{}, fmt.Errorf("ext4mk: %d blocks are too few for the file system metadata", blocks)
			}
			return g, nil
		}
		last := g.groupBlocks(g.groups - 1)
		if uint64(last) >= uint64(g.overhead(g.groups-1))+minLastGroupData {
			return g, nil
		}
		blocks -= uint64(last)
	}
}

// hasBackup reports whether group n holds a copy of the superblock and the
// group descriptor table. With sparse_super these are groups 0 and 1 and the
// powers of 3, 5 and 7.
func hasBackup(n uint32) bool {
	if n <= 1 {
		return true
	}
	for _, b := range []uint32{3, 5, 7} {
		v := uint64(b)
		for v < uint64(n) {
			v *= uint64(b)
		}
		if v == uint64(n) {
			return true
		}
	}
	return false
}

// groupStart is the first block of group n.
func (g geometry) groupStart(n uint32) uint64 { return uint64(n) * blocksPerGroup }

// groupBlocks is the number of blocks in group n. Only the last group can be
// short.
func (g geometry) groupBlocks(n uint32) uint32 {
	return uint32(min(g.blocks-g.groupStart(n), blocksPerGroup))
}

// backupBlocks is the number of blocks that the superblock and the group
// descriptor table take at the start of group n.
func (g geometry) backupBlocks(n uint32) uint32 {
	if hasBackup(n) {
		return 1 + g.gdtBlocks
	}
	return 0
}

// overhead is the number of metadata blocks in group n: the backup, the two
// bitmaps and the inode table.
func (g geometry) overhead(n uint32) uint32 {
	return g.backupBlocks(n) + 2 + g.itableBlocks
}

// Format writes an empty ext4 file system to w. It writes only metadata
// blocks, each at its own offset, and it never reads, so a sparse file stays
// sparse. w must read as zeros wherever Format does not write, which is true
// of a new file. Format does not set the length of the target. The last
// blocks of the file system are usually holes, so the caller must make the
// file as long as the file system, for example with Truncate. FormatFile does
// both steps.
func Format(w io.WriterAt, o Options) error {
	if w == nil {
		return errors.New("ext4mk: nil writer")
	}
	if o.Size < MinSize {
		return fmt.Errorf("ext4mk: size %d is below the minimum of %d bytes", o.Size, MinSize)
	}
	if o.Size > MaxSize {
		return fmt.Errorf("ext4mk: size %d is above the maximum of %d bytes", o.Size, MaxSize)
	}
	if len(o.Label) > labelSize {
		return fmt.Errorf("ext4mk: label is %d bytes, the limit is %d", len(o.Label), labelSize)
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	uuid := o.UUID
	if uuid == ([16]byte{}) {
		if _, err := rand.Read(uuid[:]); err != nil {
			return fmt.Errorf("ext4mk: random uuid: %w", err)
		}
	}
	geo, err := newGeometry(min(uint64(o.Size/blockSize), maxBlocks))
	if err != nil {
		return err
	}
	f := &formatter{w: w, geo: geo, now: now()}

	// Group 0 holds the two directory blocks right after its metadata.
	rootBlock := f.geo.groupStart(0) + uint64(geo.overhead(0))
	lostBlock := rootBlock + 1

	gdt := make([]byte, int(geo.gdtBlocks)*blockSize)
	bitmap := make([]byte, blockSize)
	var freeBlocks uint64
	for n := uint32(0); n < geo.groups; n++ {
		start := geo.groupStart(n)
		inGroup := geo.groupBlocks(n)
		used := geo.overhead(n)
		dirs, freeInodes := uint16(0), geo.ipg
		if n == 0 {
			used += 2
			dirs, freeInodes = 2, geo.ipg-firstIno
		}
		free := inGroup - used

		// Block bitmap: metadata and padding past the end are in use.
		clear(bitmap)
		setBits(bitmap, 0, used)
		setBits(bitmap, inGroup, blockSize*8)
		first := start + uint64(geo.backupBlocks(n))
		if err := f.write(first*blockSize, bitmap); err != nil {
			return err
		}
		// Inode bitmap: only group 0 has inodes in use, and the bits past
		// the last inode are padding.
		clear(bitmap)
		if n == 0 {
			setBits(bitmap, 0, firstIno)
		}
		setBits(bitmap, geo.ipg, blockSize*8)
		if err := f.write((first+1)*blockSize, bitmap); err != nil {
			return err
		}

		d := gdt[n*descSize:]
		le32(d[0:], uint32(first))       // #nosec G115 -- first is a block below maxBlocks, which fits 32 bits
		le32(d[4:], uint32(first+1))     // #nosec G115 -- first is a block below maxBlocks, which fits 32 bits
		le32(d[8:], uint32(first+2))     // #nosec G115 -- first is a block below maxBlocks, which fits 32 bits
		le16(d[12:], uint16(free))       // #nosec G115 -- at most blocksPerGroup
		le16(d[14:], uint16(freeInodes)) // #nosec G115 -- at most blocksPerGroup
		le16(d[16:], dirs)
		freeBlocks += uint64(free)
	}

	// The first inode table block holds the root and lost+found inodes. The
	// rest of every table stays a hole.
	table := make([]byte, blockSize)
	f.putDirInode(table[(rootIno-1)*inodeSize:], 0o40755, o.RootUID, o.RootGID, 3, rootBlock)
	f.putDirInode(table[(lostFoundIno-1)*inodeSize:], 0o40700, 0, 0, 2, lostBlock)
	tableBlock := geo.groupStart(0) + uint64(geo.backupBlocks(0)) + 2
	if err := f.write(tableBlock*blockSize, table); err != nil {
		return err
	}
	root := make([]byte, blockSize)
	putDirEntry(root, 0, rootIno, 12, ".", ftDir)
	putDirEntry(root, 12, rootIno, 12, "..", ftDir)
	putDirEntry(root, 24, lostFoundIno, blockSize-24, "lost+found", ftDir)
	if err := f.write(rootBlock*blockSize, root); err != nil {
		return err
	}
	lost := make([]byte, blockSize)
	putDirEntry(lost, 0, lostFoundIno, 12, ".", ftDir)
	putDirEntry(lost, 12, rootIno, blockSize-12, "..", ftDir)
	if err := f.write(lostBlock*blockSize, lost); err != nil {
		return err
	}

	sb := f.superblock(o, uuid, freeBlocks)
	for n := uint32(0); n < geo.groups; n++ {
		if !hasBackup(n) {
			continue
		}
		start := geo.groupStart(n) * blockSize
		// The first superblock sits after the 1024-byte boot area. A backup
		// starts its block, as the block is larger than 1024 bytes.
		off := start
		if n == 0 {
			off += 1024
		} else {
			// A backup is marked with its group and does not claim a clean
			// state, as mke2fs writes it, so e2fsck -b repairs from it.
			le16(sb[90:], uint16(n))
			le16(sb[58:], 0)
		}
		if err := f.write(off, sb); err != nil {
			return err
		}
		if err := f.write(start+blockSize, gdt); err != nil {
			return err
		}
	}
	return nil
}

// FormatFile creates the file at path, which must not exist, with mode 0600
// and makes it a file system of o.Size bytes. The file is sparse. It is
// removed again on error.
func FormatFile(path string, o Options) (err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- the caller chooses the path
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(path)
		}
	}()
	if o.Size < MinSize || o.Size > MaxSize {
		// Format reports the precise error. Check first so that Truncate is
		// not called with a size that is not valid.
		return Format(f, o)
	}
	if err = f.Truncate(o.Size); err != nil {
		return err
	}
	if err = Format(f, o); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	return f.Close()
}

// hashSeed derives the directory hash seed from the UUID, so that the same
// UUID gives the same image.
func hashSeed(uuid [16]byte) []byte {
	sum := sha256.Sum256(uuid[:])
	return sum[:16]
}
