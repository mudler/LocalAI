//go:build windows

package atomicfile

import (
	"errors"
	"syscall"
)

// SyncDenied reports whether err is the "Access is denied" a Windows Sync
// fails with: FlushFileBuffers needs a writable handle, so a Sync through a
// read-only file handle (or any directory handle) fails even though the data
// is durable. Callers that already flushed through a writable handle use this
// to tolerate the denied re-sync instead of failing a durable operation.
func SyncDenied(err error) bool {
	return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
}
