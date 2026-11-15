// SPDX-License-Identifier: MIT
//go:build darwin

package blob

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// barrierSync orders the writes of f before every write that is issued after
// it, without waiting for the drive to empty its cache. Go's File.Sync on
// macOS is F_FULLFSYNC, about 4 ms on an SSD; F_BARRIERFSYNC costs a fraction
// of that. It does not make the bytes durable by itself: a later fullSync (a
// real Sync) on any descriptor of the same volume flushes the cache and with
// it every write that the barriers ordered. A volume that does not know the
// command gets a full Sync instead.
func barrierSync(f *os.File) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return f.Sync()
	}
	var ferr error
	if err := rc.Control(func(fd uintptr) {
		for {
			_, ferr = unix.FcntlInt(fd, unix.F_BARRIERFSYNC, 0)
			if !errors.Is(ferr, syscall.EINTR) {
				return
			}
		}
	}); err != nil {
		return f.Sync()
	}
	if ferr != nil {
		return f.Sync()
	}
	return nil
}
