// SPDX-License-Identifier: MIT
package launcher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

var errLauncherLocked = errors.New("another launcher already owns the runtime for this user")

// userLock is the per-user launcher lock. It prevents a second launcher from
// starting a role that overlaps the first one's runtime. The kernel drops it
// when the holder dies, so a crash cannot leave a stale lock behind. The
// descriptor is close-on-exec: a runtime child never keeps the lock alive.
type userLock struct{ file *os.File }

// acquireUserLock takes the lock file "launcher.lock" in dir without waiting.
func acquireUserLock(dir string) (*userLock, error) {
	fd, err := unix.Open(filepath.Join(dir, "launcher.lock"), unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cannot open the launcher lock securely: %w", err)
	}
	f := os.NewFile(uintptr(fd), "launcher lock")
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || int64(st.Uid) != int64(os.Geteuid()) || st.Nlink != 1 {
		f.Close()
		return nil, errors.New("the launcher lock is not an owned regular file")
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return nil, fmt.Errorf("cannot restrict the launcher lock: %w", err)
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, errLauncherLocked
		}
		return nil, fmt.Errorf("cannot lock the launcher lock: %w", err)
	}
	return &userLock{file: f}, nil
}

// Release gives the lock up. It is safe to call twice.
func (l *userLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	f := l.file
	l.file = nil
	return f.Close()
}
