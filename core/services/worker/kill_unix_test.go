//go:build !windows

package worker

import "syscall"

// syscallKill ends a process the way a crash would, with no chance to clean up.
func syscallKill(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }
