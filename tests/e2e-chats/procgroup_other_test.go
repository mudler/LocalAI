//go:build !windows

package e2echats_test

import "syscall"

// setProcessGroup puts the spawned server in its own process group so a
// signal can reach it without the test runner; Windows has no process
// groups of this kind (the attribute does not exist there).
func setProcessGroup(attr *syscall.SysProcAttr) {
	attr.Setpgid = true
}

// killProcessGroup signals the spawned server's whole process group
// (negative pid). sig is SIGTERM for the graceful stop and SIGKILL for the
// forced one.
func killProcessGroup(pid int, sig syscall.Signal) error {
	return syscall.Kill(-pid, sig)
}
