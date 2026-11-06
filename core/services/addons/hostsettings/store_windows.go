//go:build windows

package hostsettings

import (
	"os"

	"golang.org/x/sys/windows"
)

// Windows has no O_NOFOLLOW; the owner root is trusted, and the mode and type
// checks in Open still reject a substituted file.
const noFollowFlag = 0

func lockFile(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, new(windows.Overlapped))
}

func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}

// Directory handles cannot be flushed on Windows; the rename is already
// durable once os.Rename returns.
func syncDir(*os.File) error {
	return nil
}

// validFileMode cannot enforce Unix permission bits on Windows; the owner-root
// ACL is the guard there, so only the regular-file check applies.
func validFileMode(m os.FileMode) bool {
	return m.IsRegular()
}
