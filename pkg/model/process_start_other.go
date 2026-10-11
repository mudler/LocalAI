//go:build !windows

package model

import process "github.com/mudler/go-processmanager"

func startProcessWindows(proc *process.Process, runtime *backendProcessRuntime, processName string, processArgs []string, workDir string, env []string) (pid int, err error) {
	return 0, nil
}
