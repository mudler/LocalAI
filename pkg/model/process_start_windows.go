//go:build windows

package model

import (
	"fmt"
	"os"
	"os/exec"

	process "github.com/mudler/go-processmanager"
	"github.com/mudler/xlog"
	"golang.org/x/sys/windows"
)

// windowsEnvironmentBlock builds a Windows environment block (UTF-16,
// null-terminated strings, double-null terminated) from a slice of
// "KEY=VALUE" strings. Pass nil to inherit the parent's environment.
func windowsEnvironmentBlock(env []string) (*uint16, error) {
	var b []uint16
	for _, e := range env {
		for _, c := range e + "\x00" {
			b = append(b, uint16(c))
		}
	}
	b = append(b, 0) // extra null terminator
	if len(b) == 0 {
		return nil, nil
	}
	return &b[0], nil
}

// startProcessWindows starts a process, assigns it to the platform's
// process-tree job object, then lets it run. On Windows we use os.StartProcess
// (which wraps CreateProcess correctly) instead of raw CreateProcess, because
// raw CreateProcess can fail with ERROR_INVALID_PARAMETER on some Windows
// configurations. The job object assignment happens immediately after
// StartProcess returns; there is a small race window before the process is in
// the job, but this matches the behavior of os/exec.Cmd on other platforms.
func startProcessWindows(proc *process.Process, runtime *backendProcessRuntime, processName string, processArgs []string, workDir string, env []string) (pid int, err error) {
	// Build command: --addr converted to --addr=<addr> for Windows backends
	var winArgs []string
	for i := 0; i < len(processArgs); i++ {
		if processArgs[i] == "--addr" && i+1 < len(processArgs) {
			winArgs = append(winArgs, "--addr="+processArgs[i+1])
			i++
		} else {
			winArgs = append(winArgs, processArgs[i])
		}
	}

	cmd := exec.Command(processName, winArgs...)
	cmd.Dir = workDir
	cmd.Env = env
	cmd.SysProcAttr = &windows.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
	}

	if err := cmd.Start(); err != nil {
		return 0, err
	}

	pid = cmd.Process.Pid

	// Assign to the job object after start, so any child processes join the job.
	runtime.trackProcessTree(pid)

	// Write the pidfile so the process.Process handle (used for IsAlive/Stop)
	// can locate the process.
	if err = os.WriteFile(proc.StateDir()+"/pid", []byte(fmt.Sprint(pid)), os.ModePerm); err != nil {
		xlog.Debug("failed to write pid file", "error", err)
	}
	return pid, nil
}
