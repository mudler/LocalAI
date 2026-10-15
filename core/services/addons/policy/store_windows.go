//go:build windows

package policy

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
