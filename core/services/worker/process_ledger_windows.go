//go:build windows

package worker

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processQueryLimitedInformation is PROCESS_QUERY_LIMITED_INFORMATION from
// winnt.h; x/sys/windows does not export it.
const processQueryLimitedInformation = 0x1000

// procStartTime returns the creation time of pid (100 ns units since 1601),
// or "" when the process cannot be opened.
func procStartTime(pid int) string {
	created, ok := processCreationTime(pid)
	if !ok {
		return ""
	}
	return strconv.FormatUint(created, 10)
}

func processCreationTime(pid int) (uint64, bool) {
	h, err := windows.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return 0, false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return 0, false
	}
	return uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime), true
}

// killProcessGroup ends pid and every process it started. Windows has no
// process groups. A backend runs inside a kill-on-close job object (the
// process manager creates it, see pkg/model), which already ends the tree
// when the worker dies, and a backend whose job cannot be created does not
// start. This is a second line of defence, for example for a process left by
// an older worker. The tree comes from a process snapshot, with the PID reuse
// guard of processTreeOf.
func killProcessGroup(pid int) error {
	if pid == os.Getpid() {
		return errors.New("refusing to kill the worker itself")
	}
	snapshot, err := windowsProcessSnapshot()
	if err != nil {
		return err
	}
	tree := processTreeOf(pid, snapshot)
	var rootErr error
	for i, member := range tree {
		if member == os.Getpid() {
			continue
		}
		err := terminatePID(member)
		if i == 0 {
			rootErr = err
		}
	}
	return rootErr
}

func terminatePID(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("opening process %d: %w", pid, err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if err := windows.TerminateProcess(h, 1); err != nil {
		return fmt.Errorf("terminating process %d: %w", pid, err)
	}
	return nil
}

func windowsProcessSnapshot() ([]processSnapshotEntry, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("taking a process snapshot: %w", err)
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snap, &entry); err != nil {
		return nil, fmt.Errorf("reading the process snapshot: %w", err)
	}
	var out []processSnapshotEntry
	for {
		pid := int(entry.ProcessID)
		created, _ := processCreationTime(pid)
		out = append(out, processSnapshotEntry{PID: pid, PPID: int(entry.ParentProcessID), Created: created})
		if err := windows.Process32Next(snap, &entry); err != nil {
			break
		}
	}
	return out, nil
}
