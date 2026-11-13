// Package ext4mk formats a block device or a sparse file with a small ext4
// file system, in pure Go and without any external tool.
//
// The macOS sandbox backend gives each virtual machine a sparse disk image.
// The Linux guest mounts it read-write as the upper layer of an overlay, and
// the host reads it later. Linux makes such an image with mke2fs. macOS has no
// mke2fs, and a tar-to-ext4 converter sizes the file system to its content, so
// it cannot make a large empty one. This package fills that gap and nothing
// more.
//
// # What it produces
//
// A file system with these properties:
//
//   - 4096-byte blocks, 256-byte inodes, 32768 blocks per group.
//   - About one inode per 16 KiB, rounded so that the inode tables fill whole
//     blocks. The count is the same in every group.
//   - Compat features ext_attr and dir_index. Incompat features filetype and
//     extent. Read-only compat features sparse_super, large_file, huge_file,
//     dir_nlink and extra_isize.
//   - No journal, so a reader never has to replay one.
//   - No metadata_csum, no gdt_csum, no flex_bg, no 64bit, no resize_inode, no
//     inline_data and no bigalloc. Without checksums the kernel writes the
//     file system without computing any, and a reader needs no checksum
//     support.
//   - The superblock and the group descriptor table are backed up in groups 0
//     and 1 and in the groups that are a power of 3, 5 or 7 (sparse_super).
//   - Every group has a block bitmap, an inode bitmap and an inode table, laid
//     out in that order after the descriptor backup, if any.
//   - A root directory (inode 2) owned by the requested user and group, and a
//     lost+found directory (inode 11), so that e2fsck reports a clean file
//     system. Inodes 1 and 3 to 10 are reserved and zeroed.
//   - Nothing is reserved for the super user: the guest user that fills the
//     image is not root.
//
// # How it writes
//
// Format writes only metadata blocks, each at its own offset, and never reads.
// The target must read as zeros everywhere that Format does not write. A fresh
// or truncated file does, and it stays sparse: the inode tables and the data
// blocks are holes. Memory use does not depend on the size, apart from 32
// bytes of group descriptor per 128 MiB.
//
// # What it does not do
//
// It is not a general mke2fs. It has no options for block size, inode size,
// features, journal, reserved blocks or lazy initialization, it does not
// check or repair a file system, and it does not resize one. The kernel mount
// of the result is the real test of the format, and the unit tests only
// compare it with e2fsprogs when those tools are installed.
package ext4mk
