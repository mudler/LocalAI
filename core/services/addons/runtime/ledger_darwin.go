//go:build darwin

// SPDX-License-Identifier: MIT
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

// macOS has no pidfd. A process is identified by its PID, the kernel start
// time of the process (microsecond resolution, from kern.proc.pid) and the boot
// time of the machine (kern.boottime). A recycled PID has a different start
// time, so recovery never signals a process it did not record. Unlike a pidfd,
// the check and the signal are two steps: see recoverProcess.

// szomb is the SZOMB process state of <sys/proc.h>: exited, not yet reaped.
const szomb = 5

// requireHandleSupport has nothing to prove on macOS: identity is read from
// the kernel by PID and openLedger has already read the identity of this process.
func requireHandleSupport() error { return nil }

func formatTimeval(tv unix.Timeval) string {
	return strconv.FormatInt(int64(tv.Sec), 10) + "." + fmt.Sprintf("%06d", int64(tv.Usec))
}

// procState returns the start time and state of a process. A PID that does not
// exist gives an error that satisfies errors.Is(err, fs.ErrNotExist).
func procState(pid int) (start string, stat int8, err error) {
	if pid <= 0 {
		return "", 0, ErrRuntime
	}
	k, e := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if e != nil {
		// The kernel answers an unknown PID with an empty result, which the
		// library reports as EIO. Confirm with a null signal before treating
		// it as "gone": any other failure stays an error.
		if errors.Is(e, unix.EIO) && errors.Is(unix.Kill(pid, 0), unix.ESRCH) {
			return "", 0, fmt.Errorf("process %d: %w", pid, fs.ErrNotExist)
		}
		return "", 0, e
	}
	if int(k.Proc.P_pid) != pid || (k.Proc.P_starttime.Sec == 0 && k.Proc.P_starttime.Usec == 0) {
		return "", 0, ErrRuntime
	}
	return formatTimeval(k.Proc.P_starttime), k.Proc.P_stat, nil
}

func identity(pid int) (processIdentity, error) {
	boot, e := unix.SysctlTimeval("kern.boottime")
	if e != nil {
		return processIdentity{}, e
	}
	start, _, e := procState(pid)
	if e != nil {
		return processIdentity{}, e
	}
	return processIdentity{PID: pid, Boot: formatTimeval(*boot), Start: start}, nil
}

// recoverProcess kills the recorded process when, and only when, a process with
// that PID still has the recorded boot time and start time, then waits for it
// to exit. There is no handle to hold: between the identity read and the
// signal the kernel could in theory recycle the PID. macOS hands out PIDs in
// increasing order and wraps at 99999, so a recycle inside that window needs
// the whole PID space to cycle within microseconds; the window is not zero.
func recoverProcess(ctx context.Context, p processIdentity) error {
	if p.Pending || p.PID <= 0 || p.Start == "" || p.Boot == "" || p.Incarnation == "" {
		return ErrRuntime
	}
	now, e := identity(p.PID)
	if errors.Is(e, fs.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	if now.Boot != p.Boot || now.Start != p.Start {
		return nil
	}
	if e = unix.Kill(p.PID, unix.SIGKILL); e != nil {
		if errors.Is(e, unix.ESRCH) {
			return nil
		}
		return e
	}
	for {
		start, stat, e := procState(p.PID)
		switch {
		case errors.Is(e, fs.ErrNotExist):
			return nil
		case e != nil:
			return e
		case start != p.Start || stat == szomb:
			// Exited (zombie) or already replaced by another process.
			return nil
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		time.Sleep(10 * time.Millisecond)
	}
}
