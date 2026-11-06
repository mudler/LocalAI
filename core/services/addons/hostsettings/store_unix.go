//go:build !windows

package hostsettings

import (
	"os"
	"syscall"
)

const noFollowFlag = syscall.O_NOFOLLOW

func lockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

func syncDir(dir *os.File) error {
	return dir.Sync()
}

// validFileMode rejects a policy file anyone but its owner can read or write.
func validFileMode(m os.FileMode) bool {
	return m.IsRegular() && m.Perm()&0o077 == 0
}
