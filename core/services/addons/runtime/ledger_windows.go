//go:build windows

// SPDX-License-Identifier: MIT
package runtime

import (
	"context"
	"errors"
	"os"
	"strconv"

	"github.com/mudler/LocalAI/pkg/fsperm"
	"golang.org/x/sys/windows"
)

// On Windows a process is identified by its PID and its creation time. The
// creation time is an absolute FILETIME (100 ns units since 1601), not a time
// since boot, so it tells a recycled PID apart across reboots as well: Boot
// is a fixed marker. Recovery opens a process handle first, reads the
// creation time through that handle and terminates through it, so, as with a
// pidfd on Linux, a recycled PID cannot be hit between the check and the kill.

// windowsBoot is the Boot value of every Windows identity.
const windowsBoot = "windows-absolute-start"

// stillActive is STILL_ACTIVE, the exit code of a process that runs.
const stillActive = 259

// ledgerNoFollow is 0: Windows has no O_NOFOLLOW. checkStateRoot refuses a
// state root that is a link, and the files are created by the owner.
const ledgerNoFollow = 0

// checkStateRoot refuses a state root that is a symbolic link or a junction.
// Windows has no POSIX permission bits: in place of the private-mode check of
// the other platforms, the folder gets an access control list that grants
// access to the current user only. A new folder, or one made by an older
// LocalAI version, has the list of its parent until then.
func checkStateRoot(root string) error {
	info, e := os.Lstat(root)
	if e != nil || !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return ErrRuntime
	}
	if _, e = fsperm.RequirePrivate(root, info); e != nil {
		return ErrRuntime
	}
	return nil
}

func lockLedger(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, new(windows.Overlapped))
}

// syncLedgerDir has nothing to do: FlushFileBuffers needs a writable handle
// and a directory handle is not one. The ledger file itself was flushed
// before the rename.
func syncLedgerDir(string) error { return nil }

// requireHandleSupport has nothing to prove: process handles are always
// available on Windows.
func requireHandleSupport() error { return nil }

// creationTime reads the creation time of the process behind h.
func creationTime(h windows.Handle) (string, error) {
	var created, exited, kernel, user windows.Filetime
	if e := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); e != nil {
		return "", e
	}
	t := uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)
	if t == 0 {
		return "", ErrRuntime
	}
	return strconv.FormatUint(t, 10), nil
}

// openProcess opens pid. A PID that does not exist gives os.ErrNotExist.
func openProcess(pid int, access uint32) (windows.Handle, error) {
	if pid <= 0 {
		return 0, ErrRuntime
	}
	h, e := windows.OpenProcess(access, false, uint32(pid))
	if errors.Is(e, windows.ERROR_INVALID_PARAMETER) {
		return 0, os.ErrNotExist
	}
	return h, e
}

func identity(pid int) (processIdentity, error) {
	h, e := openProcess(pid, windows.PROCESS_QUERY_LIMITED_INFORMATION)
	if e != nil {
		return processIdentity{}, e
	}
	defer func() { _ = windows.CloseHandle(h) }()
	start, e := creationTime(h)
	if e != nil {
		return processIdentity{}, e
	}
	return processIdentity{PID: pid, Boot: windowsBoot, Start: start}, nil
}

func recoverProcess(ctx context.Context, p processIdentity) error {
	if p.Pending || p.PID <= 0 || p.Start == "" || p.Boot != windowsBoot || p.Incarnation == "" {
		return ErrRuntime
	}
	h, e := openProcess(p.PID, windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	defer func() { _ = windows.CloseHandle(h) }()
	now, e := creationTime(h)
	if e != nil {
		return e
	}
	if now != p.Start {
		return nil // the PID belongs to another process now
	}
	var code uint32
	if e = windows.GetExitCodeProcess(h, &code); e != nil {
		return e
	}
	if code != stillActive {
		return nil // exited; another holder keeps its handle open
	}
	if e = windows.TerminateProcess(h, 1); e != nil {
		return e
	}
	for {
		ev, e := windows.WaitForSingleObject(h, 10)
		if e != nil {
			return e
		}
		if ev == windows.WAIT_OBJECT_0 {
			return nil
		}
		if e = ctx.Err(); e != nil {
			return e
		}
	}
}
