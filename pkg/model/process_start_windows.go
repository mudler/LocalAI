//go:build windows

package model

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	process "github.com/mudler/go-processmanager"
	"github.com/mudler/xlog"
	"golang.org/x/sys/windows"
)

// startProcessWindows starts the backend process with exec.Cmd (which wraps
// CreateProcess), assigns it to the platform's process-tree job object, then
// lets it run. The job assignment happens immediately after Start returns;
// there is a small race window before the process is in the job, but the
// launcher (powershell.exe or the backend binary) needs far longer than that
// to spawn children, and the OS reaps the whole tree once the job handle
// closes regardless.
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

	// Add the backend's lib directory to the front of PATH so DLLs are found,
	// mirroring run.ps1's "$env:PATH = \"$lib;$env:PATH\"". PATH entries are
	// joined with the path-list separator ';' — not the directory separator.
	// The existing entry is replaced in place so the child environment never
	// carries two PATH values.
	if libDir := filepath.Join(workDir, "lib"); dirExists(libDir) {
		patched := false
		for i, e := range env {
			if key, value, ok := strings.Cut(e, "="); ok && strings.EqualFold(key, "PATH") {
				env[i] = "PATH=" + libDir + string(os.PathListSeparator) + value
				patched = true
				break
			}
		}
		if !patched {
			env = append(env, "PATH="+libDir+string(os.PathListSeparator)+os.Getenv("PATH"))
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
	if err = os.WriteFile(filepath.Join(proc.StateDir(), "pid"), []byte(fmt.Sprint(pid)), os.ModePerm); err != nil {
		xlog.Debug("failed to write pid file", "error", err)
	}
	return pid, nil
}
