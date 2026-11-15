//go:build linux

// SPDX-License-Identifier: MIT
package runtime

import (
	"context"
	"errors"
	"math"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// requireHandleSupport proves pidfd support, which recovery needs.
func requireHandleSupport() error {
	fd, e := unix.PidfdOpen(os.Getpid(), 0)
	if e != nil {
		return e
	}
	_ = unix.Close(fd)
	return nil
}

func identity(pid int) (processIdentity, error) {
	boot, e := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if e != nil {
		return processIdentity{}, e
	}
	b, e := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if e != nil {
		return processIdentity{}, e
	}
	end := strings.LastIndex(string(b), ")")
	if end < 0 {
		return processIdentity{}, ErrRuntime
	}
	f := strings.Fields(string(b)[end+1:])
	if len(f) < 20 {
		return processIdentity{}, ErrRuntime
	}
	if _, e = strconv.ParseUint(f[19], 10, 64); e != nil {
		return processIdentity{}, ErrRuntime
	}
	return processIdentity{PID: pid, Boot: strings.TrimSpace(string(boot)), Start: f[19]}, nil
}
func recoverProcess(ctx context.Context, p processIdentity) error {
	if p.Pending || p.PID <= 0 || p.Start == "" || p.Boot == "" || p.Incarnation == "" {
		return ErrRuntime
	}
	// Open the handle BEFORE reading identity. If the PID exits/recycles while
	// checking /proc, this handle still refers only to the process we opened.
	fd, e := unix.PidfdOpen(p.PID, 0)
	if errors.Is(e, unix.ESRCH) {
		return nil
	}
	if e != nil {
		return e
	}
	defer func() { _ = unix.Close(fd) }()
	now, e := identity(p.PID)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if now.Boot != p.Boot || now.Start != p.Start {
		return nil
	}
	if e = unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0); e != nil && !errors.Is(e, unix.ESRCH) {
		return e
	}
	if fd < 0 || fd > math.MaxInt32 {
		return ErrRuntime
	}
	for {
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, e := unix.Poll(fds, 10)
		if e != nil && !errors.Is(e, unix.EINTR) {
			return e
		}
		if n > 0 && fds[0].Revents&unix.POLLIN != 0 {
			return nil
		}
		if e = ctx.Err(); e != nil {
			return e
		}
	}
}
