package ext4ro

import (
	"context"
	"fmt"
	"strings"
)

// ErrDirBudget is the error of a read that goes over the byte budget that
// was passed to [FS.ReadDirContext]. It wraps [ErrLimit].
var ErrDirBudget = fmt.Errorf("%w: directory byte budget", ErrLimit)

// DirEntry is a live entry of a directory.
type DirEntry struct {
	Ino      uint64
	Name     string
	FileType uint8 // the file type byte of the record, 0 without the filetype feature
}

// ReadDir lists the live entries of the directory ino, without "." and "..".
// It scans every block once, so it also reads an htree directory: the dx
// root and the dx nodes look like blocks that hold one empty record.
// Records with inode 0 (deleted entries, the checksum tail of a metadata_csum
// block, dx blocks) are skipped.
//
// An entry whose inode is outside the range of InodeInRange, or whose name
// is empty or holds "/" or a NUL byte, is corrupt. More than limit live
// entries give ErrLimit. The order is the order on the disk. The reader does
// not remove duplicate names.
func (f *FS) ReadDir(ino uint64, limit int) ([]DirEntry, error) {
	return f.ReadDirContext(context.Background(), ino, limit, nil)
}

// ReadDirContext is ReadDir with a context and a byte budget. The read stops
// with the error of ctx when it ends. When dirBytes is not nil, every directory
// block that the read takes from the disk comes off *dirBytes, and a read that
// would go below zero gives ErrDirBudget. A caller that lists many directories
// passes one budget to all of them: on a forged image several directory
// inodes can map the same blocks, so the sum of their sizes can be far
// larger than the image.
func (f *FS) ReadDirContext(ctx context.Context, ino uint64, limit int, dirBytes *int64) ([]DirEntry, error) {
	in, err := f.load(ino)
	if err != nil {
		return nil, err
	}
	if in.typ() != sIFDIR {
		return nil, fmt.Errorf("%w: inode %d", ErrNotDir, ino)
	}
	if limit < 0 {
		return nil, fmt.Errorf("ext4ro: negative entry cap %d", limit)
	}
	var out []DirEntry
	if in.Flags&flagInlineData != 0 {
		return f.readInlineDir(in, limit)
	}
	if in.Size < 0 || in.Size > maxDirBytes {
		return nil, fmt.Errorf("%w: directory %d has %d bytes", ErrLimit, ino, in.Size)
	}
	size := uint64(in.Size)
	if size == 0 || size%f.bs != 0 {
		return nil, fmt.Errorf("%w: directory %d has size %d", ErrCorrupt, ino, size)
	}
	nblk := size / f.bs
	if dirBytes != nil && int64(size) > *dirBytes { // #nosec G115 -- size is at most maxDirBytes here
		return nil, fmt.Errorf("%w: directory %d has %d bytes, more than the %d bytes left", ErrDirBudget, ino, size, *dirBytes)
	}
	budget := maxNodeVisits
	buf := make([]byte, f.bs)
	for lb := uint64(0); lb < nblk; {
		r, err := f.lookup(in, lb, &budget)
		if err != nil {
			return nil, err
		}
		if r.hole || r.unwritten {
			return nil, fmt.Errorf("%w: directory %d has no block at %d", ErrCorrupt, ino, lb)
		}
		for k := uint64(0); k < r.n && lb < nblk; k, lb = k+1, lb+1 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if dirBytes != nil {
				if *dirBytes < int64(f.bs) { // #nosec G115 -- the block size is at most 64 KiB
					return nil, fmt.Errorf("%w: all the bytes are read", ErrDirBudget)
				}
				*dirBytes -= int64(f.bs) // #nosec G115 -- as above
			}
			if err := f.readAt(buf, (r.phys+k)*f.bs); err != nil {
				return nil, err
			}
			if out, err = f.parseDirBlock(buf, out, limit); err != nil {
				return nil, fmt.Errorf("directory %d, block %d: %w", ino, lb, err)
			}
		}
	}
	return out, nil
}

// readInlineDir reads a directory that keeps its entries in the inode: the
// parent inode, then records in the rest of i_block, then more records in
// the value of "system.data". Each part is a block of its own.
func (f *FS) readInlineDir(in *inode, limit int) ([]DirEntry, error) {
	whole, err := f.inlineDirData(in)
	if err != nil {
		return nil, err
	}
	var out []DirEntry
	first := min(len(whole), iBlockLen)
	if out, err = f.parseDirBlock(whole[4:first], out, limit); err != nil {
		return nil, fmt.Errorf("inline directory %d: %w", in.Ino, err)
	}
	if len(whole) > iBlockLen {
		if out, err = f.parseDirBlock(whole[iBlockLen:], out, limit); err != nil {
			return nil, fmt.Errorf("inline directory %d: %w", in.Ino, err)
		}
	}
	return out, nil
}

// inlineDirData is inlineContent for a directory.
func (f *FS) inlineDirData(in *inode) ([]byte, error) {
	return f.inlineContent(in, true)
}

// recLen decodes the rec_len field of a directory record. A 64 KiB block
// needs the special encoding, because the field has 16 bits.
func (f *FS) recLen(v uint16) int {
	if f.bs < 65536 {
		return int(v)
	}
	if v == 65535 || v == 0 {
		return 65536
	}
	return int(v&65532) | int(v&3)<<16
}

// parseDirBlock appends the live records of one block to out.
func (f *FS) parseDirBlock(b []byte, out []DirEntry, limit int) ([]DirEntry, error) {
	for off := 0; off < len(b); {
		if len(b)-off < 8 {
			return nil, fmt.Errorf("%w: record at %d does not fit its block", ErrCorrupt, off)
		}
		ino := uint64(le.Uint32(b[off:]))
		rl := f.recLen(le.Uint16(b[off+4:]))
		nl := int(b[off+6])
		if rl < 8 || rl&3 != 0 || rl > len(b)-off || nl+8 > rl {
			return nil, fmt.Errorf("%w: record at %d has rec_len %d and name_len %d", ErrCorrupt, off, rl, nl)
		}
		if ino != 0 {
			name := b[off+8 : off+8+nl]
			if string(name) != "." && string(name) != ".." {
				if nl == 0 || strings.ContainsAny(string(name), "/\x00") {
					return nil, fmt.Errorf("%w: record at %d has the name %q", ErrCorrupt, off, name)
				}
				if !f.InodeInRange(ino) {
					return nil, fmt.Errorf("%w: entry %q has inode %d, outside the file system (%d to %d)", ErrCorrupt, name, ino, f.firstIno, f.inodes)
				}
				if len(out) >= limit {
					return nil, fmt.Errorf("%w: directory has more than %d entries", ErrLimit, limit)
				}
				out = append(out, DirEntry{Ino: ino, Name: string(name), FileType: b[off+7]})
			}
		}
		off += rl
	}
	return out, nil
}
