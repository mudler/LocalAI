//go:build !windows

package worker

import "syscall"

func syscallKill(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }
