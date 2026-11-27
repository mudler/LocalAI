//go:build darwin

package promptcache

import (
	"os"

	"golang.org/x/sys/unix"
)

// dropCache turns the page cache off for reads and writes through f.
func dropCache(f *os.File) error {
	_, err := unix.FcntlInt(f.Fd(), unix.F_NOCACHE, 1)
	return err
}
