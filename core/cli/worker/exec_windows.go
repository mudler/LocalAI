//go:build windows

package worker

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"unsafe"

	"github.com/mudler/xlog"
	"golang.org/x/sys/windows"
)

// execBackend runs the backend as a child process and exits with its exit
// code. Windows cannot replace a running process image (there is no exec), so
// the worker stays as the parent:
//
//   - The child shares the console, so Ctrl+C and Ctrl+Break reach it
//     directly. The worker stops through its normal signal handling.
//   - The child is put in a kill-on-close job object, so it ends when the
//     worker ends: on Ctrl+C, on a service stop, and when the worker is
//     killed.
func execBackend(path string, argv, env []string) error {
	var args []string
	if len(argv) > 1 {
		args = argv[1:]
	}
	cmd := exec.Command(path, args...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", path, err)
	}
	if err := assignKillOnCloseJob(cmd.Process.Pid); err != nil {
		xlog.Warn("Could not tie the backend to the worker; it can outlive a killed worker", "error", err)
	}
	err := cmd.Wait()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.ExitCode())
	}
	if err != nil {
		return err
	}
	os.Exit(0)
	return nil
}

// assignKillOnCloseJob puts pid in a job object that ends all its processes
// when the last handle closes. The handle is never closed explicitly: the OS
// closes it when the worker exits.
func assignKillOnCloseJob(pid int) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	var limit windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limit.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limit)), uint32(unsafe.Sizeof(limit))); err != nil {
		_ = windows.CloseHandle(job)
		return err
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		_ = windows.CloseHandle(job)
		return err
	}
	return nil
}
