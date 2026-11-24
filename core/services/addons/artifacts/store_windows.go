//go:build windows

package artifacts

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func lockDir(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil { // #nosec G302 -- directory mode: the owner needs the search bit
		return nil, err
	}
	f, err := os.OpenFile(filepath.FromSlash(filepath.Join(dir, lockName)), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, new(windows.Overlapped)); err != nil {
		_ = f.Close()
		return nil, ErrStoreBusy
	}
	return f, nil
}

func openPartialFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
}

func openBlobFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY, 0)
}
