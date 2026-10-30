//go:build windows

package resources

import (
	"os"
	"path/filepath"
	"sync"
)

var (
	lockMu sync.Mutex
	locks  = map[string]bool{}
)

func lockSelections(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.FromSlash(filepath.Join(dir, selectionLock)), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	path := f.Name()
	lockMu.Lock()
	defer lockMu.Unlock()
	if locks[path] {
		_ = f.Close()
		return nil, ErrSelectionStore
	}
	locks[path] = true
	return f, nil
}

func unlockSelections(f *os.File) error {
	path := f.Name()
	lockMu.Lock()
	defer lockMu.Unlock()
	delete(locks, path)
	return f.Close()
}