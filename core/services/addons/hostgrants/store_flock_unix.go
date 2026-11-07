//go:build !windows

package hostgrants

import (
	"os"
	"syscall"
)

// noFollowFlag is O_NOFOLLOW on Unix; it rejects symlinks when opening files.
const noFollowFlag = syscall.O_NOFOLLOW

// lockFile takes an exclusive, non-blocking flock on f.
func lockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// unlockFile releases the flock on f.
func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
