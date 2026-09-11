//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package safefile

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ReadRegularAt is the portable fallback for platforms without openat.
func ReadRegularAt(dir, name string) ([]byte, os.FileMode, error) {
	if name == "" || filepath.Base(name) != name {
		return nil, 0, fmt.Errorf("%q is not a direct directory entry", name)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(name)
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("%q is not a regular file", name)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = file.Close() }()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return nil, 0, fmt.Errorf("%q changed while opening", name)
	}
	data, err := io.ReadAll(file)
	return data, openedInfo.Mode().Perm(), err
}
