//go:build windows

package model

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// createWindowsJobObject wraps a backend process's whole tree in a Windows
// job object configured with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE. On Windows
// the backend is launched through run.ps1, so go-processmanager tracks only
// the PowerShell wrapper's PID and killing it would orphan the actual backend
// binary. A child process joins its parent's job, so assigning the wrapper on
// launch captures the backend as well, and terminating the job reaps it.
func createWindowsJobObject(pid int) (uintptr, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("creating job object: %w", err)
	}

	// Terminate every member once the last job handle closes. This is what
	// reaps the backend tree even when local-ai.exe itself is killed hard
	// (taskkill /F, crash): the OS closes our job handle and fires the kill.
	var limit windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limit.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limit)), uint32(unsafe.Sizeof(limit)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("setting kill-on-close on job object: %w", err)
	}

	// The wrapper and its backend child spawn identically on every host, so no
	// breakaway flags are needed. A failure here means the host placed the
	// backend in a job it cannot escape (service managers, some CI runners);
	// the caller logs that and continues without the tree-kill guarantee.
	processHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid),
	)
	if err != nil {
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("opening backend process %d: %w", pid, err)
	}
	defer func() { _ = windows.CloseHandle(processHandle) }()
	if err := windows.AssignProcessToJobObject(job, processHandle); err != nil {
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("assigning backend process %d to job object: %w", pid, err)
	}
	return uintptr(job), nil
}

// terminateWindowsJobObject kills every process that still belongs to the job
// (the wrapper and any backend child that outlived it), then releases the
// handle. Safe on an empty job: TerminateJobObject succeeds without members,
// and closing the handle is the KILL_ON_JOB_CLOSE trigger for stragglers.
func terminateWindowsJobObject(handle uintptr) {
	if handle == 0 {
		return
	}
	_ = windows.TerminateJobObject(windows.Handle(handle), 1)
	_ = windows.CloseHandle(windows.Handle(handle))
}
