//go:build windows

package recovery

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// freeBytes returns the disk space the caller can use on the given path
// (Windows version), the analogue of the Unix statfs Bavail*Bsize.
func freeBytes(path string) (uint64, error) {
	var avail uint64
	if err := windows.GetDiskFreeSpaceEx(windows.StringToUTF16Ptr(filepath.FromSlash(path)), &avail, nil, nil); err != nil {
		return 0, err
	}
	return avail, nil
}
