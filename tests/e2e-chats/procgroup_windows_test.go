//go:build windows

package e2echats_test

import (
	"os"
	"syscall"
)

// setProcessGroup is a no-op on Windows: syscall.SysProcAttr has no Setpgid
// there, and TerminateProcess does not need a process group.
func setProcessGroup(attr *syscall.SysProcAttr) {}

// killProcessGroup stops the spawned server. Windows has no signals: both
// the graceful and the forced stop collapse to TerminateProcess, and the
// backend tree dies with the server through its job object.
func killProcessGroup(pid int, sig syscall.Signal) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
