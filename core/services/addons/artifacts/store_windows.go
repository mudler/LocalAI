//go:build windows

package artifacts

import (
	"os"
	"path/filepath"
	"sync"
)

var (
	lockMu sync.Mutex
	locks  = map[string]bool{}
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
	path := f.Name()
	lockMu.Lock()
	defer lockMu.Unlock()
	if locks[path] {
		_ = f.Close()
		return nil, ErrStoreBusy
	}
	locks[path] = true
	return f, nil
}

func openPartialFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
}

func openBlobFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY, 0)
}