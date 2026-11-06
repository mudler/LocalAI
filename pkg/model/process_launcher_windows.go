//go:build windows

package model

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	process "github.com/mudler/go-processmanager"
)

// resolveLauncher rewrites the gallery-contract run.sh launch for a Windows
// host, which has no POSIX shell to execute that stub. On Windows, prefer
// launching llama-cpp-grpc.exe directly: it is the dedicated gRPC server that
// stays running for inference, whereas run.sh/run.ps1 default to
// llama-cpp-cpu-all.exe which loads the model and exits. We do not set
// LLAMACPP_GRPC_SERVERS to select grpc.exe because grpc-server.cpp also reads
// that variable as RPC server addresses (add_rpc_devices), so setting it to "1"
// would make the backend try to connect to endpoint "1" and crash.
//
// run.ps1/run.sh remain fallbacks for backends that don't ship
// llama-cpp-grpc.exe. The lib/ directory is added to PATH in
// startProcessWindows so DLLs are found, mirroring run.ps1's
// "$env:PATH = \"$lib;$env:PATH\"".
func resolveLauncher(grpcProcess, workDir string, args []string) (processName string, processArgs []string, err error) {
	// Prefer launching llama-cpp-grpc.exe directly.
	grpcBinary := filepath.Join(workDir, "llama-cpp-grpc.exe")
	if _, statErr := os.Stat(grpcBinary); statErr == nil {
		return grpcBinary, args, nil
	}

	// Fall back to run.ps1 via PowerShell: it handles backend binary selection
	// and environment setup correctly.
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

	// Fall back to run.sh via PowerShell (has environment setup).
	runScript = filepath.Join(workDir, "run.sh")
	if _, statErr := os.Stat(runScript); statErr == nil {
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

// dirExists reports whether the named directory exists.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}