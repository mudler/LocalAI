//go:build !windows

package recovery

import (
	"os"
	"path/filepath"
	"syscall"
)

// freeBytes returns free disk space on the given path (Unix version).
func freeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(filepath.ToSlash(path), &st); err != nil {
		return 0, err
	}
	if st.Bsize <= 0 {
		return 0, errors.New("the file system reports no block size")
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}