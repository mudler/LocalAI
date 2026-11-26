//go:build linux

package main

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// setDeathSignal makes the kernel kill coli serve when the thread that
// started it dies, so a crashed or SIGKILLed backend does not leave a model
// loaded. The engine then sees its pipes close and exits too.
func setDeathSignal(a *syscall.SysProcAttr) { a.Pdeathsig = syscall.SIGKILL }

// lockThreadForChild pins the supervising goroutine to its thread: PDEATHSIG
// fires when that thread exits, not the process.
func lockThreadForChild() { runtime.LockOSThread() }

// groupRSS is the resident memory of every process in the group of pid
// (coli serve and its engine), in bytes.
func groupRSS(pgid int) uint64 {
	if pgid <= 0 {
		return 0
	}
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	var total uint64
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		// Fields after the command name, which may contain spaces.
		s := string(stat)
		i := strings.LastIndexByte(s, ')')
		if i < 0 {
			continue
		}
		f := strings.Fields(s[i+1:])
		// f[0] state, f[1] ppid, f[2] pgrp
		if len(f) < 3 || f[2] != strconv.Itoa(pgid) {
			continue
		}
		total += processRSS(pid)
	}
	return total
}

func processRSS(pid int) uint64 {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return 0
	}
	for _, l := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(l, "VmRSS:"); ok {
			if f := strings.Fields(rest); len(f) > 0 {
				kb, _ := strconv.ParseUint(f[0], 10, 64)
				return kb * 1024
			}
		}
	}
	return 0
}
