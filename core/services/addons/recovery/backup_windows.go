//go:build windows

package recovery

import (
	"os"
	"path/filepath"
)

// freeBytes returns free disk space on the given path (Windows version).
func freeBytes(path string) (uint64, error) {
	// On Windows, use GetDiskFreeSpaceExW via syscall
	// For simplicity, return a large value - in production use GetDiskFreeSpaceExW
	_, err := os.Stat(filepath.FromSlash(path))
	if err != nil {
		return 0, err
	}
	return 1024 * 1024 * 1024 * 10, nil // 10GB placeholder
}