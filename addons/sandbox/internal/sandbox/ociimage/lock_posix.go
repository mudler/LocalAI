//go:build !windows

package ociimage

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

// LockFile takes an exclusive lock on p, polling until ctx ends. It creates p
// when it is missing. The returned function releases the lock.
func LockFile(ctx context.Context, p string) (func(), error) {
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o600) // #nosec G304 -- a path the add-on builds under its own data or state folder
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
