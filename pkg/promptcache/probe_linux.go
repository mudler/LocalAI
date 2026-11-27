//go:build linux

package promptcache

import (
	"os"

	"golang.org/x/sys/unix"
)

// dropCache asks the kernel to drop the clean pages of f from the page cache.
// The caller syncs a written file first: only clean pages are dropped.
func dropCache(f *os.File) error {
	if err := unix.Fdatasync(int(f.Fd())); err != nil {
		return err
	}
	return unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED)
}
