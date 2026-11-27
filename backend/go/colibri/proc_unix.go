//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// termGroup and killGroup signal the whole process group of coli serve, so
// the engine process it starts ends with it.
func termGroup(pid int) {
	if pid > 0 {
		_ = syscall.Kill(-pid, syscall.SIGTERM)
	}
}

func killGroup(pid int) {
	if pid > 0 {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
}

// setChildAttrs puts coli serve in its own process group (so a signal can
// reach the engine it starts), plus the per-OS extras in setDeathSignal.
func setChildAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	setDeathSignal(cmd.SysProcAttr)
}
