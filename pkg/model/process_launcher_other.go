//go:build !windows

package model

import (
	"path/filepath"

	process "github.com/mudler/go-processmanager"
)

// resolveLauncher runs the gallery-contract run.sh stub directly: every
// non-Windows host ships a POSIX shell that executes it as-is, so neither the
// executable nor the argument vector needs rewriting. The executable is the
// stub's basename because os.StartProcess resolves it against ProcAttr.Dir.
func resolveLauncher(grpcProcess, _ string, args []string) (processName string, processArgs []string, err error) {
	return filepath.Base(grpcProcess), args, nil
}

// processLauncherOther implements processLauncher for non-Windows platforms.
// It starts the process with Run().
type processLauncherOther struct{}

var _ processLauncher = processLauncherOther{}

func (processLauncherOther) launch(proc *process.Process, runtime *backendProcessRuntime, processName string, processArgs []string, workDir string, env []string) error {
	return proc.Run()
}

// newLauncher returns the platform-specific process launcher for non-Windows.
func newLauncher() processLauncher {
	return processLauncherOther{}
}

// prepareBackendEnv returns the backend environment unchanged: the dynamic
// loader path is set by the run.sh stub on these platforms.
func prepareBackendEnv(_ string, env []string) []string {
	return env
}
