//go:build windows

package main

import (
	"os"
	"os/exec"
)

// The backend images target Linux and macOS. These keep the package
// building on Windows: the process is ended on its own, without a group.

func setChildAttrs(*exec.Cmd) {}

func termGroup(pid int) { killGroup(pid) }

func killGroup(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

func lockThreadForChild() {}

func groupRSS(int) uint64 { return 0 }
