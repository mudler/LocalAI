//go:build windows

package model

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	process "github.com/mudler/go-processmanager"
)

// resolveLauncher rewrites the gallery-contract run.sh launch for a Windows
// host, which has no POSIX shell to execute that stub. The bundled run.ps1 is
// started through the system PowerShell. It picks the backend binary the same
// way run.sh does: llama-cpp-cpu-all.exe by default, llama-cpp-fallback.exe
// when that is absent, and the RPC build (llama-cpp-grpc.exe) only when
// LLAMACPP_GRPC_SERVERS is set. The RPC build is compiled without AVX, AVX2,
// AVX512, FMA, F16C and BMI2, so it must not be the default.
//
// The lib/ directory is added to PATH in prepareBackendEnv so DLLs are found,
// mirroring run.ps1's "$env:PATH = \"$lib;$env:PATH\"".
func resolveLauncher(grpcProcess, workDir string, args []string) (processName string, processArgs []string, err error) {
	processName, processArgs, err = resolveLauncherPath(grpcProcess, workDir, args)
	if err != nil {
		return "", nil, err
	}
	return processName, joinAddrArgs(processArgs), nil
}

// joinAddrArgs rewrites "--addr <address>" into "--addr=<address>", the form
// the Windows backends parse. Other arguments are kept as they are.
func joinAddrArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--addr" && i+1 < len(args) {
			out = append(out, "--addr="+args[i+1])
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func resolveLauncherPath(grpcProcess, workDir string, args []string) (processName string, processArgs []string, err error) {
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

	// Last resort for a backend that ships no run.ps1: run the gallery
	// contract path as it is. Resolve it against workDir: CreateProcess does
	// not search the child's working directory, so a bare basename would be
	// looked up next to local-ai.exe instead of in the backend directory.
	return filepath.Join(workDir, filepath.Base(grpcProcess)), args, nil
}

// prepareBackendEnv adds the backend's lib directory to the front of PATH so
// the Windows loader finds the backend's DLLs. run.ps1 does the same, but the
// raw-path fallback in resolveLauncher starts the backend directly. PATH
// entries are joined with the path-list separator ';', not the directory
// separator. An existing PATH entry is replaced in place so the child
// environment never carries two PATH values.
func prepareBackendEnv(workDir string, env []string) []string {
	libDir := filepath.Join(workDir, "lib")
	if !dirExists(libDir) {
		return env
	}
	for i, e := range env {
		if key, value, ok := strings.Cut(e, "="); ok && strings.EqualFold(key, "PATH") {
			env[i] = "PATH=" + libDir + string(os.PathListSeparator) + value
			return env
		}
	}
	return append(env, "PATH="+libDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// processLauncherWindows implements processLauncher for Windows.
//
// The backend is started through go-processmanager's Run(), as on other
// platforms. That keeps stdout and stderr captured in the state directory,
// closes Done() when the process exits, and waits on the process so its handle
// is released.
//
// Limit: go-processmanager passes an empty SysProcAttr on Windows, so the
// caller cannot set creation flags such as CREATE_NEW_PROCESS_GROUP or
// CREATE_SUSPENDED. The process is therefore assigned to the kill-on-close job
// object right after Run returns. A child that the launcher spawned before
// that assignment would not join the job; the launcher needs far longer than
// that to start its backend. Because the backend shares the console of
// local-ai.exe, a console Ctrl+C also reaches it.
type processLauncherWindows struct{}

var _ processLauncher = processLauncherWindows{}

func (processLauncherWindows) launch(proc *process.Process, runtime *backendProcessRuntime, _ string, _ []string, _ string, _ []string) error {
	if err := proc.Run(); err != nil {
		return err
	}
	if pid, err := strconv.Atoi(proc.CurrentPID()); err == nil {
		runtime.trackProcessTree(pid)
	}
	return nil
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
