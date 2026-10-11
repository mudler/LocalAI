//go:build windows

package model

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// resolveLauncher rewrites the gallery-contract run.sh launch for a Windows
// host, which has no POSIX shell to execute that stub. When the backend image
// ships the run.ps1 companion, the launch becomes
// "powershell.exe -NoProfile -ExecutionPolicy Bypass -File run.ps1"; run.sh
// stays in the image so discovery, validation and upgrades stay uniform with
// the other platforms. Without run.ps1 the raw executable is spawned as
// elsewhere.
func resolveLauncher(grpcProcess, workDir string, args []string) (processName string, processArgs []string, err error) {
	runScript := filepath.Join(workDir, "run.ps1")
	if _, statErr := os.Stat(runScript); statErr != nil {
		return filepath.Base(grpcProcess), args, nil
	}
	// os.StartProcess resolves the executable against ProcAttr.Dir on Windows,
	// so a bare "powershell.exe" would be looked up inside the backend's
	// workDir and every launch would fail with file-not-found. Resolve the
	// absolute path first (PATH lookup finds System32).
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
