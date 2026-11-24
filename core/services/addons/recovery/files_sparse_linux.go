//go:build linux

package recovery

import (
	"errors"
	"io"
	"os"
	"syscall"
)

// SEEK_DATA and SEEK_HOLE from linux/fs.h.
const (
	seekData = 3
	seekHole = 4
)

// copyFileData writes only the data ranges of in, so a sparse source — the
// overlay image of a virtual machine, for example — stays sparse in the
// backup. A file system without SEEK_DATA gets a plain copy.
func copyFileData(in, out *os.File, size int64) error {
	for off := int64(0); off < size; {
		data, err := in.Seek(off, seekData)
		if err != nil {
			if errors.Is(err, syscall.ENXIO) {
				break // everything from off is a hole
			}
			if off == 0 {
				// SEEK_DATA is unsupported on this file system.
				if _, serr := in.Seek(0, io.SeekStart); serr != nil {
					return serr
				}
				_, cerr := io.Copy(out, in)
				return cerr
			}
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
