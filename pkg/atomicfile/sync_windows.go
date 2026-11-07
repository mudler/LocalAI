//go:build windows

package atomicfile

import (
	"errors"
	"syscall"
)

// dirSyncDenied reports whether err is the "Access is denied" a Windows
// directory-handle Sync fails with; the file data is already durable then.
func dirSyncDenied(err error) bool {
	return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
}
