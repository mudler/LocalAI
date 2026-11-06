//go:build linux

package main

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// setChildAttrs makes the kernel kill the engine when this process dies, so
// a crashed or SIGKILLed backend cannot leave an engine holding GPU memory.
// Closing the engine's stdin (which also happens when we die) ends its serve
// loop too, but only between requests; PDEATHSIG covers a request in flight.
func setChildAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}

// lockThreadForChild pins the calling goroutine to its OS thread for good.
// PDEATHSIG fires when the thread that created the child exits, not the
// process; the supervising goroutine keeps that thread alive until the child
// is reaped.
func lockThreadForChild() { runtime.LockOSThread() }

// processRSS returns the resident memory of a process in bytes, or 0.
func processRSS(pid int) uint64 {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return 0
	}
	for _, l := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(l, "VmRSS:"); ok {
			f := strings.Fields(rest)
			if len(f) > 0 {
				kb, _ := strconv.ParseUint(f[0], 10, 64)
				return kb * 1024
			}
		}
	}
	return 0
}
