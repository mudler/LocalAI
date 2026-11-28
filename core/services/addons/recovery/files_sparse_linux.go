//go:build linux

// SPDX-License-Identifier: MIT
package recovery

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func copySparseData(in, out *os.File, size int64) error {
	for offset := int64(0); offset < size; {
		data, err := unix.Seek(int(in.Fd()), offset, unix.SEEK_DATA)
		if errors.Is(err, unix.ENXIO) {
			return nil // Everything remaining is a hole.
		}
		if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOTSUP) {
			return copyZeroBlocks(in, out, size)
		}
		if err != nil {
			return err
		}
		hole, err := unix.Seek(int(in.Fd()), data, unix.SEEK_HOLE)
		if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOTSUP) {
			return copyZeroBlocks(in, out, size)
		}
		if err != nil {
			return err
		}
		if _, err = in.Seek(data, io.SeekStart); err != nil {
			return err
		}
		if _, err = out.Seek(data, io.SeekStart); err != nil {
			return err
		}
		// Copy only data extents: copy_file_range may expand holes on some
		// filesystems when asked to copy the entire file in one go.
		if _, err = io.CopyN(out, in, min(hole, size)-data); err != nil {
			return err
		}
		offset = hole
	}
	return nil
}
