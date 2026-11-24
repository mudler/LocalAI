//go:build windows

package ociimage

import "context"

// LockFile is not available on Windows: the backends need Linux.
func LockFile(ctx context.Context, p string) (func(), error) {
	return nil, ErrNoLock
}
