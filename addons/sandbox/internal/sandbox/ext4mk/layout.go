package ext4mk

import (
	"encoding/binary"
	"io"
	"time"
)

// On-disk constants of the ext4 format.
const (
	magic = 0xEF53

	// Feature flags.
	compatExtAttr  = 0x0008
	compatDirIndex = 0x0020

	incompatFiletype = 0x0002
	incompatExtents  = 0x0040

	roSparseSuper = 0x0001
	roLargeFile   = 0x0002
	roHugeFile    = 0x0008
	roDirNlink    = 0x0020
	roExtraIsize  = 0x0040

	defMountUserXattr = 0x0004
	defMountACL       = 0x0008

	flagExtents = 0x80000 // EXT4_EXTENTS_FL

	ftDir = 2 // directory entry file type

	extraIsize = 32
)

var (
	le16 = binary.LittleEndian.PutUint16
	le32 = binary.LittleEndian.PutUint32
)

// formatter holds what the write helpers share.
type formatter struct {
	w   io.WriterAt
	geo geometry
	now time.Time
}

func (f *formatter) write(off uint64, p []byte) error {
	_, err := f.w.WriteAt(p, int64(off)) // #nosec G115 -- off is below 2^44
	return err
}

// setBits sets the bits in [from, to) of a bitmap.
func setBits(b []byte, from, to uint32) {
	for i := from; i < to; i++ {
		if i%8 == 0 && i+8 <= to {
			b[i/8] = 0xff
			i += 7
			continue
		}
		b[i/8] |= 1 << (i % 8)
	}
}

// superblock builds the 1024-byte primary superblock.
func (f *formatter) superblock(o Options, uuid [16]byte, freeBlocks uint64) []byte {
	g := f.geo
	sb := make([]byte, 1024)
	t := uint32(f.now.Unix()) // #nosec G115 -- the field is 32 bits wide
	le32(sb[0:], g.groups*g.ipg)
	le32(sb[4:], uint32(g.blocks)) // #nosec G115 -- blocks is at most maxBlocks
	// s_r_blocks_count stays 0: nothing is reserved for root.
	le32(sb[12:], uint32(freeBlocks)) // #nosec G115 -- free blocks never exceed blocks
	le32(sb[16:], g.groups*g.ipg-firstIno)
	// s_first_data_block is 0 and s_log_block_size is 2 (4096 bytes).
	le32(sb[24:], 2)
	le32(sb[28:], 2) // s_log_cluster_size: no bigalloc, same as the block
	le32(sb[32:], blocksPerGroup)
	le32(sb[36:], blocksPerGroup)
	le32(sb[40:], g.ipg)
	le32(sb[48:], t)         // s_wtime
	le16(sb[54:], 0xffff)    // s_max_mnt_count: never check on mount
	le16(sb[56:], magic)     // s_magic
	le16(sb[58:], 1)         // s_state: valid
	le16(sb[60:], 1)         // s_errors: continue
	le32(sb[64:], t)         // s_lastcheck
	le32(sb[76:], 1)         // s_rev_level: dynamic
	le32(sb[84:], firstIno)  // s_first_ino
	le16(sb[88:], inodeSize) // s_inode_size
	le32(sb[92:], compatExtAttr|compatDirIndex)
	le32(sb[96:], incompatFiletype|incompatExtents)
	le32(sb[100:], roSparseSuper|roLargeFile|roHugeFile|roDirNlink|roExtraIsize)
	copy(sb[104:], uuid[:])
	copy(sb[120:], o.Label)
	copy(sb[236:], hashSeed(uuid)) // s_hash_seed
	sb[252] = 1                    // s_def_hash_version: half MD4
	le32(sb[256:], defMountUserXattr|defMountACL)
	le32(sb[264:], t)          // s_mkfs_time
	le16(sb[348:], extraIsize) // s_min_extra_isize
	le16(sb[350:], extraIsize) // s_want_extra_isize
	return sb
}

// encodeTime returns the 32-bit seconds field and the "extra" field of an
// inode time, the way the Linux kernel does: the low two bits of the extra
// field extend the seconds past the year 2038 and the rest holds nanoseconds.
func encodeTime(t time.Time) (sec, extra uint32) {
	s := t.Unix()
	sec = uint32(s)                               // #nosec G115 -- the high bits go into extra
	epoch := uint32((s-int64(int32(s)))>>32) & 3  // #nosec G115 -- same
	return sec, epoch | uint32(t.Nanosecond())<<2 // #nosec G115 -- below 2^30
}

// putDirInode writes a 256-byte inode for a directory with one block, mapped
// by one extent.
func (f *formatter) putDirInode(b []byte, mode uint16, uid, gid uint32, links uint16, block uint64) {
	sec, extra := encodeTime(f.now)
	le16(b[0:], mode)
	le16(b[2:], uint16(uid))  // #nosec G115 -- the low half; the high half is below
	le32(b[4:], blockSize)    // i_size
	le32(b[8:], sec)          // atime
	le32(b[12:], sec)         // ctime
	le32(b[16:], sec)         // mtime
	le16(b[24:], uint16(gid)) // #nosec G115 -- the low half
	le16(b[26:], links)
	le32(b[28:], blockSize/512) // i_blocks in 512-byte units
	le32(b[32:], flagExtents)
	// i_block holds an extent tree: header, then one leaf extent.
	le16(b[40:], 0xF30A) // eh_magic
	le16(b[42:], 1)      // eh_entries
	le16(b[44:], 4)      // eh_max: four extents fit in i_block
	// eh_depth 0, eh_generation 0, ee_block 0.
	le16(b[56:], 1)                 // ee_len
	le16(b[58:], uint16(block>>32)) // #nosec G115 -- ee_start_hi: block is below maxBlocks, so the high half is 0
	le32(b[60:], uint32(block))     // #nosec G115 -- ee_start_lo: block is below maxBlocks
	le16(b[120:], uint16(uid>>16))  // uid_high
	le16(b[122:], uint16(gid>>16))  // gid_high
	le16(b[128:], extraIsize)       // i_extra_isize
	le32(b[132:], extra)            // ctime_extra
	le32(b[136:], extra)            // mtime_extra
	le32(b[140:], extra)            // atime_extra
	le32(b[144:], sec)              // crtime
	le32(b[148:], extra)            // crtime_extra
}

// putDirEntry writes a directory entry. A block of a file system without
// metadata_csum has no tail record, so the last entry reaches the block end.
func putDirEntry(b []byte, off int, ino uint32, recLen int, name string, ftype byte) {
	le32(b[off:], ino)
	le16(b[off+4:], uint16(recLen)) // #nosec G115 -- at most 4096
	b[off+6] = byte(len(name))      // #nosec G115 -- a name has at most 255 bytes
	b[off+7] = ftype
	copy(b[off+8:], name)
}
