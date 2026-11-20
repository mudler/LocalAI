package worker

import (
	"fmt"
	"runtime"
	"strings"
)

// backendsWithoutWindowsBuild lists the backends that the standalone worker
// commands start and that have no native Windows build. Only llama.cpp has
// one today.
var backendsWithoutWindowsBuild = map[string]bool{
	"mlx-distributed": true,
	"p2p-mlx":         true,
	"vllm":            true,
	"ds4-distributed": true,
}

// requireBackendBuild fails at once, with a clear message, when the worker
// command needs a backend that has no build for the host OS. Without it the
// command would install nothing usable and fail later with an error about a
// missing file or an invalid executable.
func requireBackendBuild(goos, command, backend string) error {
	if goos != "windows" {
		return nil
	}
	if !backendsWithoutWindowsBuild[command] {
		return nil
	}
	return fmt.Errorf("`local-ai worker %s` is not available on Windows: the %s backend has no Windows build. Run this worker on Linux or macOS, or use `local-ai worker llama-cpp-rpc` or `local-ai worker p2p-llama-cpp-rpc`, which work on Windows", command, backend)
}

// executableName returns the file name of a backend executable on goos.
func executableName(goos, name string) string {
	if goos == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		return name + ".exe"
	}
	return name
}

func hostExecutableName(name string) string { return executableName(runtime.GOOS, name) }

func requireHostBackendBuild(command, backend string) error {
	return requireBackendBuild(runtime.GOOS, command, backend)
}
