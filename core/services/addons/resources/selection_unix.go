//go:build !windows

package resources

import (
	"os"
	"path/filepath"
	"syscall"
)

func lockSelections(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, selectionLock), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, ErrSelectionStore
	}
	return f, nil
}

func unlockSelections(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}