//go:build linux

package chv

import (
	"errors"
	"io"
	"os"
	"syscall"
)

// ficlone is _IOW(0x94, 9, int) from linux/fs.h.
const ficlone = 0x40049409

// SEEK_DATA and SEEK_HOLE.
const (
	seekData = 3
	seekHole = 4
)

// cloneFile copies src to dst. It makes a reflink when the file system
// supports one, and otherwise copies only the data ranges, so a sparse
// upper image stays sparse.
func cloneFile(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- a path the add-on builds under its own data or state folder
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- a path the add-on builds under its own data or state folder
	if err != nil {
		return err
	}
	if err := fileIoctl(out, ficlone, in); err == nil {
		return out.Close()
	}
	if err := sparseCopy(in, out, st.Size()); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func fileIoctl(dst *os.File, req uintptr, src *os.File) error {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, dst.Fd(), req, src.Fd())
	if e != 0 {
		return e
	}
	return nil
}

func sparseCopy(in, out *os.File, size int64) error {
	var off int64
	for off < size {
		data, err := in.Seek(off, seekData)
		if errors.Is(err, syscall.ENXIO) {
			break // only a hole is left
		}
		if err != nil {
			return err
		}
		hole, err := in.Seek(data, seekHole)
		if err != nil {
			return err
		}
		if _, err := io.Copy(io.NewOffsetWriter(out, data), io.NewSectionReader(in, data, hole-data)); err != nil {
			return err
		}
		off = hole
	}
	return out.Truncate(size)
}
