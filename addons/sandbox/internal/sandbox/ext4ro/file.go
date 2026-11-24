package ext4ro

import (
	"bytes"
	"errors"
	"fmt"
	"io"
)

// File gives random access to the content of a regular file. Holes and
// unwritten extents read as zeros. It implements io.ReaderAt.
type File struct {
	fs     *FS
	in     *inode
	inline []byte // content of an inline file, else nil
}

// Open returns the content of the regular file ino.
func (f *FS) Open(ino uint64) (*File, error) {
	in, err := f.load(ino)
	if err != nil {
		return nil, err
	}
	if in.typ() != sIFREG {
		return nil, fmt.Errorf("%w: inode %d", ErrNotRegular, ino)
	}
	fl := &File{fs: f, in: in}
	if in.Flags&flagInlineData != 0 {
		if fl.inline, err = f.inlineContent(in, false); err != nil {
			return nil, err
		}
	}
	return fl, nil
}

// Size returns the size of the file from the inode. It can be larger than
// the image: the blocks that the file does not map read as zeros.
func (fl *File) Size() int64 { return fl.in.Size }

// SectionReader returns a reader for the whole file.
func (fl *File) SectionReader() *io.SectionReader {
	return io.NewSectionReader(fl, 0, fl.in.Size)
}

// ReadAt implements io.ReaderAt. The work of a call is proportional to
// len(p): it allocates nothing and reads each run of blocks once. At most
// maxNodeVisits extent nodes are visited, then the call returns ErrLimit.
func (fl *File) ReadAt(p []byte, off int64) (int, error) {
	return fl.fs.readFileAt(fl.in, fl.inline, p, off)
}

func (f *FS) readFileAt(in *inode, inline []byte, p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("ext4ro: negative offset")
	}
	if off >= in.Size {
		return 0, io.EOF
	}
	want := len(p)
	if rest := in.Size - off; int64(want) > rest {
		want = int(rest)
	}
	var err error
	if in.Flags&flagInlineData != 0 {
		// The inline bytes are at most 64 KiB. The rest of the size reads
		// as zeros.
		clear(p[:want])
		if off < int64(len(inline)) {
			copy(p[:want], inline[off:])
		}
	} else {
		err = f.readMapped(in, p[:want], uint64(off))
	}
	if err != nil {
		return 0, err
	}
	if want < len(p) {
		return want, io.EOF
	}
	return want, nil
}

// readMapped fills p with the file bytes from off. The caller has checked
// that off+len(p) is not past the size of the file.
func (f *FS) readMapped(in *inode, p []byte, off uint64) error {
	budget := maxNodeVisits
	for done := uint64(0); done < uint64(len(p)); {
		pos := off + done
		lb, inBlk := pos/f.bs, pos%f.bs
		r, err := f.lookup(in, lb, &budget)
		if err != nil {
			return err
		}
		span := min(uint64(len(p))-done, r.n*f.bs-inBlk)
		dst := p[done : done+span]
		if r.hole || r.unwritten {
			clear(dst)
		} else if err := f.readAt(dst, r.phys*f.bs+inBlk); err != nil {
			return err
		}
		done += span
	}
	return nil
}

// ibodyXattrs lists the attributes in the body of the inode.
func (f *FS) ibodyXattrs(in *inode) ([]Xattr, error) {
	start := goodOldInode + in.extra
	if f.isz <= goodOldInode || start+4 > f.isz {
		return nil, nil
	}
	area := in.raw[start:]
	if len(area) < 4 || le.Uint32(area) != xattrMagic {
		if bytes.Count(area, []byte{0}) == len(area) {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: inode %d: the attribute area has no magic", ErrCorrupt, in.Ino)
	}
	return parseXattrs(area[4:], 0)
}

// inlineContent returns the bytes of an inline file, symlink or directory:
// the 60 bytes of i_block, then the value of "system.data". The result is cut
// to in.Size, unless all is set (a directory uses all of its data, as the
// kernel does). It can be shorter than in.Size: the kernel reads the
// missing part of an inline file as zeros, and mke2fs makes such files for
// a file that has only holes.
func (f *FS) inlineContent(in *inode, all bool) ([]byte, error) {
	var ea []byte
	xs, err := f.ibodyXattrs(in)
	if err != nil {
		return nil, err
	}
	for _, x := range xs {
		if x.Name == "system.data" {
			if x.Err != nil {
				return nil, x.Err
			}
			ea = x.Value
		}
	}
	out := make([]byte, 0, iBlockLen+len(ea))
	out = append(out, in.iblock()...)
	out = append(out, ea...)
	if !all && in.Size < int64(len(out)) {
		out = out[:in.Size]
	}
	return out, nil
}

// Readlink returns the target of the symbolic link ino. It reads a fast
// symlink (in i_block), a slow symlink (in a block) and an inline symlink.
// A target is at most maxSymlink bytes and has no NUL byte.
func (f *FS) Readlink(ino uint64) (string, error) {
	in, err := f.load(ino)
	if err != nil {
		return "", err
	}
	if in.typ() != sIFLNK {
		return "", fmt.Errorf("%w: inode %d", ErrNotSymlink, ino)
	}
	if in.Size == 0 {
		return "", fmt.Errorf("%w: inode %d: symlink of size 0", ErrCorrupt, ino)
	}
	if in.Size > maxSymlink {
		return "", fmt.Errorf("%w: inode %d: symlink of %d bytes", ErrLimit, ino, in.Size)
	}
	var b []byte
	switch {
	case in.Flags&flagInlineData != 0:
		if b, err = f.inlineContent(in, false); err == nil && int64(len(b)) != in.Size {
			err = fmt.Errorf("%w: inode %d: inline symlink of %d bytes has %d bytes of data", ErrCorrupt, ino, in.Size, len(b))
		}
	case f.isFastSymlink(in):
		if in.Size > iBlockLen {
			return "", fmt.Errorf("%w: inode %d: fast symlink of %d bytes", ErrCorrupt, ino, in.Size)
		}
		b = in.iblock()[:in.Size]
	default:
		b = make([]byte, in.Size)
		_, err = f.readFileAt(in, nil, b, 0)
	}
	if err != nil {
		return "", err
	}
	if bytes.IndexByte(b, 0) >= 0 {
		return "", fmt.Errorf("%w: inode %d: symlink target has a NUL byte", ErrCorrupt, ino)
	}
	return string(b), nil
}

// isFastSymlink uses the rule of the kernel: a symlink that has no blocks
// (apart from the attribute block) keeps its target in i_block.
func (f *FS) isFastSymlink(in *inode) bool {
	var ea uint64
	if in.fileACL != 0 {
		ea = f.bs / 512
	}
	return in.iblocks == ea
}
