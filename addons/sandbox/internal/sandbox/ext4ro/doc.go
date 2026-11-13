// Package ext4ro is a read-only, pure Go reader for ext2, ext3 and ext4
// file system images.
//
// The macOS sandbox backend reads the guest's upper layer on the host after
// the guest froze its file system. The Linux backend does this with debugfs,
// which macOS does not have. This package replaces it. The image is hostile:
// a process in the guest can write any bytes into the file system
// structures. The reader must never panic, never loop forever, never
// allocate memory from a number on the disk and never read outside the
// image. It reports a damaged structure as an error.
//
// # Supported
//
//   - ext2, ext3 and ext4 images from mke2fs or from the Linux kernel, with
//     block sizes from 1 KiB to 64 KiB.
//   - 32-bit and 64-bit group descriptors, flex_bg, sparse_super and
//     meta_bg.
//   - Extent trees (depth up to 5, unwritten extents read as zeros) and the
//     classic block map (direct, indirect, double and triple indirect).
//   - Inodes of 128 bytes and larger, with the extra fields (nanosecond and
//     epoch bits, i_version_hi).
//   - Linear directories and htree directories. The reader scans every
//     directory block. The dx root and the dx nodes look like blocks that
//     hold one empty record, so the scan skips them. The checksum tail of a
//     metadata_csum directory block is a record with inode 0 and is skipped
//     in the same way.
//   - inline_data for directories, files and symlinks, with the
//     "system.data" continuation.
//   - Extended attributes in the inode body and in the external block, with
//     the full names (for example "trusted.overlay.opaque").
//
// # Not supported
//
// Open returns [ErrUnsupported] for an image that needs journal recovery,
// and for these features: compression, encryption, journal_dev, dirdata,
// bigalloc, sparse_super2 with meta_bg, unknown incompat bits and revisions
// above 1. An extended attribute whose value lives in an inode (ea_inode)
// is returned with [Xattr.Err] set to [ErrUnsupported]; the other
// attributes of the inode stay readable. The reader does not write, does
// not replay a journal and does not read free-space bitmaps.
//
// # Checksums
//
// The reader does not verify any checksum (superblock, group descriptor,
// inode, directory, extent or attribute). The caller reads an image that a
// guest may have written, so a checksum proves nothing: the guest can
// compute a correct one. The reader uses structural checks instead.
//
// # Hostile image rules
//
// Every read is checked against the image size. All offset arithmetic uses
// uint64 or int64 values that are checked before they are multiplied.
// A count from the disk is capped before the reader allocates from it.
// The reader never allocates from a file size. Open checks the superblock,
// and every other structure is checked when the reader uses it:
//
//   - The block size is 1 KiB to 64 KiB, the group geometry is consistent
//     with the block and inode counts, the file system is not larger than
//     the image, and the group descriptor table is inside the image.
//   - An inode table is inside the file system, and the inode size is a
//     power of two from 128 up to the block size.
//   - A directory record has rec_len of at least 8, a multiple of 4,
//     inside its block, and a name that fits. A name that is empty, or has
//     "/" or a NUL byte, is corrupt. A record whose inode is outside the
//     range that [FS.InodeInRange] accepts is corrupt.
//   - An extent node has the magic, a count that fits the node, a depth
//     that is smaller than the depth of its parent, sorted entries that do
//     not overlap, and blocks inside the file system.
//   - A block map pointer is inside the file system. The map has a fixed
//     depth, so it cannot loop.
//   - An attribute area has the magic, entries and values inside the area,
//     and a terminator.
//
// The caps are named constants. A cap that is hit gives [ErrLimit].
//
//   - maxDirBytes (256 MiB): the size of one directory. A caller that lists
//     many directories also passes a shared byte budget to
//     [FS.ReadDirContext].
//   - maxSymlink (4096): the length of a symlink target.
//   - maxXattrBytes (1 MiB): the total value bytes of the attributes of
//     one inode. Entries may share one value, so this cap stops a
//     multiplied copy.
//   - maxNodeVisits (4 Mi): extent tree nodes that one directory read or
//     one ReadAt call visits.
//   - maxExtentDepth (5): the depth of an extent tree.
//   - maxGroups (2^32): the number of block groups. The reader reads a
//     group descriptor when it needs it, so the group count allocates
//     nothing.
//
// A file can be larger than the image. Blocks that the map does not cover
// read as zeros, as holes do. [File.ReadAt] allocates nothing from the
// size, so io.Copy of such a file streams zeros until the size. The work of
// one ReadAt call is proportional to the length of the buffer.
//
// The reader does not detect a directory that is reachable twice, or a
// loop in the directory graph. The caller walks the tree and keeps a set of
// the directory inodes that it has seen. It also checks for duplicate
// names, as the dedup rules of the caller need.
//
// # Concurrency
//
// An [FS] and a [File] hold no state that changes after Open, so they are
// safe for concurrent use when the underlying io.ReaderAt is.
package ext4ro
