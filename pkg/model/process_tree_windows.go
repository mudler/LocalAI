//go:build windows

package model

import (
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobProcessTree implements processTree with a Windows job object configured
// with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE.
type jobProcessTree struct {
	once sync.Once
	job  windows.Handle
}

func newProcessTree() processTree { return &jobProcessTree{} }

// assign wraps the process tree rooted at pid in a job object. A child joins
// its parent's job, so assigning the run.ps1 wrapper captures the backend it
// spawns as well. A failure means the host already placed us in a job we
// cannot escape (service managers, some CI runners); the caller then continues
// without the tree-kill guarantee.
func (t *jobProcessTree) assign(pid int) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("creating job object: %w", err)
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
		return fmt.Errorf("setting kill-on-close on job object: %w", err)
	}

	processHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid),
	)
	if err != nil {
		_ = windows.CloseHandle(job)
		return fmt.Errorf("opening backend process %d: %w", pid, err)
	}
	defer func() { _ = windows.CloseHandle(processHandle) }()
	if err := windows.AssignProcessToJobObject(job, processHandle); err != nil {
		_ = windows.CloseHandle(job)
		return fmt.Errorf("assigning backend process %d to job object: %w", pid, err)
	}

	t.job = job
	return nil
}

// terminate kills every process that still belongs to the job (the wrapper and
// any backend child that outlived it), then releases the handle. Safe on an
// empty job and idempotent, so concurrent stops of one backend are safe.
func (t *jobProcessTree) terminate() {
	t.once.Do(func() {
		if t.job == 0 {
			return
		}
		_ = windows.TerminateJobObject(t.job, 1)
		_ = windows.CloseHandle(t.job)
		t.job = 0
	})
}
