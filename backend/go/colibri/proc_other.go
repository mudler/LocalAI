//go:build !linux && !windows

package main

import "syscall"

// Outside Linux there is no PDEATHSIG; Free and the backend's own exit stop
// the process group instead.
func setDeathSignal(*syscall.SysProcAttr) {}

func lockThreadForChild() {}

func groupRSS(int) uint64 { return 0 }
