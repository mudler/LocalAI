//go:build windows

package resources

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func lockSelections(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.FromSlash(filepath.Join(dir, selectionLock)), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, new(windows.Overlapped)); err != nil {
		_ = f.Close()
		return nil, ErrSelectionStore
	}
	return f, nil
}

func unlockSelections(f *os.File) error {
	return f.Close()
}
