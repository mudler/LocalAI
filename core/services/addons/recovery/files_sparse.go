// SPDX-License-Identifier: MIT
package recovery

import (
	"bytes"
	"io"
	"os"
)

func copySparse(in, out *os.File) error {
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err = copySparseData(in, out, info.Size()); err != nil {
		return err
	}
	// Seeking across a trailing hole does not extend the destination.
	return out.Truncate(info.Size())
}

// copyZeroBlocks is the fallback for filesystems without sparse seeks. Reset
// both files because an unsupported seek may follow already copied extents.
func copyZeroBlocks(in, out *os.File, size int64) error {
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := out.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := out.Truncate(0); err != nil {
		return err
	}
	var buf, zero [32 * 1024]byte
	for remaining := size; remaining > 0; {
		n := min(int64(len(buf)), remaining)
		if _, err := io.ReadFull(in, buf[:n]); err != nil {
			return err
		}
		var err error
		if bytes.Equal(buf[:n], zero[:n]) {
			_, err = out.Seek(n, io.SeekCurrent)
		} else {
			_, err = out.Write(buf[:n])
		}
		if err != nil {
			return err
		}
		remaining -= n
	}
	return nil
}
