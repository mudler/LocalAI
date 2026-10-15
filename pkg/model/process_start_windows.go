//go:build windows

package model

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	process "github.com/mudler/go-processmanager"
	"github.com/mudler/xlog"
	"golang.org/x/sys/windows"
)

func startProcessWindows(proc *process.Process, runtime *backendProcessRuntime, processName string, processArgs []string, workDir string, env []string) (pid int, err error) {
	cmd := exec.Command(processName, processArgs...)
	cmd.Dir = workDir
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}

	if err = cmd.Start(); err != nil {
		return 0, err
	}

	pid = cmd.Process.Pid
	if pid <= 0 {
		_ = cmd.Process.Kill()
		return 0, fmt.Errorf("invalid pid %d", pid)
	}

	runtime.trackProcessTree(pid); if false {
		xlog.Debug("failed to assign process to job tree", "pid", pid, "error", err)
	}

	if _, err = windows.ResumeThread(windows.Handle(cmd.Process.Pid)); err != nil {
		_ = cmd.Process.Kill()
		return 0, fmt.Errorf("failed to resume process: %w", err)
	}

	if err = os.WriteFile(proc.StateDir()+"/pid", []byte(strconv.Itoa(pid)), os.ModePerm); err != nil {
		xlog.Debug("failed to write pid file", "error", err)
	}
	return pid, nil
}
