//go:build linux

package fetch

import (
	"errors"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// limitFileSize caps the size of every file that the process pid and the
// processes it starts from now on write (RLIMIT_FSIZE). A git process that
// writes past it gets SIGXFSZ, so a pack file can never outgrow the scratch
// cap, whatever the poll of the folder sees.
func limitFileSize(pid int, n int64) error {
	return unix.Prlimit(pid, unix.RLIMIT_FSIZE, &unix.Rlimit{Cur: uint64(n), Max: uint64(n)}, nil)
}

// killedForSize reports whether a git command ended on SIGXFSZ: it wrote a
// file past the size limit of limitFileSize.
func killedForSize(err error) bool {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return false
	}
	ws, ok := ee.Sys().(syscall.WaitStatus)
	return ok && ws.Signaled() && ws.Signal() == syscall.SIGXFSZ
}
