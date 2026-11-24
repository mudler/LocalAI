//go:build windows

package hostgrants

import (
	"os"

	"golang.org/x/sys/windows"
)

// noFollowFlag is 0 on Windows; the owner root is trusted and the mode/type
// checks in Open reject a substituted file.
const noFollowFlag = 0

// lockFile takes an exclusive, non-blocking lock on f using Windows LockFileEx.
func lockFile(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, new(windows.Overlapped))
}

// unlockFile releases the lock on f using Windows UnlockFileEx.
func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}
