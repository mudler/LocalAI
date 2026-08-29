//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package safefile

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// ReadRegularAt reads a direct child of dir without following symbolic links.
// Opening relative to a held directory descriptor closes the check/open race.
func ReadRegularAt(dir, name string) ([]byte, os.FileMode, error) {
	if name == "" || filepath.Base(name) != name {
		return nil, 0, fmt.Errorf("%q is not a direct directory entry", name)
	}
	dirFD, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = unix.Close(dirFD) }()

	fd, err := unix.Openat(dirFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, 0, err
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return nil, 0, fmt.Errorf("open %q: invalid file descriptor", name)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("%q is not a regular file", name)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, 0, err
	}
	return data, info.Mode().Perm(), nil
}
