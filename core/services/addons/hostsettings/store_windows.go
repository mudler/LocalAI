//go:build windows

package hostsettings

import (
	"os"

	"golang.org/x/sys/windows"
)

// Windows limit: there is no O_NOFOLLOW, so a symbolic link is not refused at
// open. Open still checks that the file is a regular file.
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

// validFileMode cannot enforce Unix permission bits on Windows, and LocalAI
// sets no ACL, so only the regular-file check applies. Access depends on the
// ACL the owner directory inherits.
func validFileMode(m os.FileMode) bool {
	return m.IsRegular()
}
