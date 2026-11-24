//go:build windows

package jobobject

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// AssignKillOnClose creates a job object with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE and puts pid in it. A child that pid
// starts later joins the same job. When the last handle to the job closes,
// Windows ends every process in it: the caller closes the handle to stop the
// tree, or Windows closes it when LocalAI exits or dies. TerminateJobObject on
// the handle stops the tree at once.
//
// The handle is not inheritable, so a child cannot keep the job open. On an
// error nothing is left open. A process that already runs in a job that does
// not allow nested jobs cannot be assigned.
func AssignKillOnClose(pid int) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("creating job object: %w", err)
	}
	var limit windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limit.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limit)), uint32(unsafe.Sizeof(limit))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("setting job object limits: %w", err)
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("opening process %d: %w", pid, err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("assigning process %d to job object: %w", pid, err)
	}
	return job, nil
}
