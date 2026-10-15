//go:build windows

package model

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	process "github.com/mudler/go-processmanager"
)

// resolveLauncher rewrites the gallery-contract run.sh launch for a Windows
// host, which has no POSIX shell to execute that stub. On Windows, prefer
// running a native .exe directly instead of run.ps1 or run.sh: look for
// *-grpc.exe first (gRPC backend), then any .exe in the backend directory.
// Only fall back to run.ps1 via PowerShell when no .exe is present, and to
// the raw gallery contract path as last resort.
func resolveLauncher(grpcProcess, workDir string, args []string) (processName string, processArgs []string, err error) {
	// Prefer a native .exe directly: avoids PowerShell and runs the backend
	// binary without an intermediate wrapper. The --addr argument is added
	// by the caller and converted to --addr=<addr> in startProcessWindows.
	if entries, rdErr := os.ReadDir(workDir); rdErr == nil {
		var grpcExe, anyExe string
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".exe" {
				continue
			}
			name := entry.Name()
			anyExe = name
			if strings.HasSuffix(name, "-grpc.exe") {
				grpcExe = name
				break
			}
		}
		if grpcExe != "" {
			return filepath.Join(workDir, grpcExe), args, nil
		}
		if anyExe != "" {
			return filepath.Join(workDir, anyExe), args, nil
		}
	}

	// Fall back to run.ps1 via PowerShell (has environment setup).
	runScript := filepath.Join(workDir, "run.ps1")
	if _, statErr := os.Stat(runScript); statErr == nil {
		// os.StartProcess resolves the executable against ProcAttr.Dir on
		// Windows, so a bare "powershell.exe" would be looked up inside the
		// backend's workDir and every launch would fail with file-not-found.
		// Resolve the absolute path first (PATH lookup finds System32).
		powershell, lookErr := exec.LookPath("powershell.exe")
		if lookErr != nil {
			return "", nil, fmt.Errorf("could not locate powershell.exe to launch %s: %w", runScript, lookErr)
		}
		processArgs = append([]string{
			"-NoProfile",
			"-ExecutionPolicy", "Bypass",
			"-File", runScript,
		}, args...)
		return powershell, processArgs, nil
	}

	// Last resort: the raw gallery contract path.
	return filepath.Base(grpcProcess), args, nil
}

// processLauncherWindows implements processLauncher for Windows.
type processLauncherWindows struct{}

var _ processLauncher = processLauncherWindows{}

func (processLauncherWindows) launch(proc *process.Process, runtime *backendProcessRuntime, processName string, processArgs []string, workDir string, env []string) error {
	_, err := startProcessWindows(proc, runtime, processName, processArgs, workDir, env)
	return err
}

// newLauncher returns the platform-specific process launcher for Windows.
func newLauncher() processLauncher {
	return processLauncherWindows{}
}
