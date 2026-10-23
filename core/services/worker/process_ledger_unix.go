//go:build !windows

package worker

import "syscall"

// killProcessGroup sends SIGKILL to the process group led by pid.
func killProcessGroup(pid int) error { return syscall.Kill(-pid, syscall.SIGKILL) }
