//go:build !windows

package workerledger

import (
	"errors"
	"syscall"
)

// killProcessGroup sends SIGKILL to the process group led by pid.
func killProcessGroup(pid int) error { return syscall.Kill(-pid, syscall.SIGKILL) }

// GroupAlive reports whether any process, a zombie included, is still a member
// of the group with id pid. EPERM means the group exists but is not ours to
// signal, which still counts as alive.
func GroupAlive(pid int) bool {
	err := syscall.Kill(-pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
