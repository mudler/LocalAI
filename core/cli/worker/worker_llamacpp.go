package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
)

type LLamaCPP struct {
	WorkerFlags  `embed:""`
	P2PJoinFlags `embed:""`
	P2PStateDir  string `name:"p2p-state-dir" env:"LOCALAI_P2P_STATE_DIR" type:"path" default:"${basepath}/p2p/llama-cpp-rpc" help:"Directory of the P2P identity and pairing of this worker (with --join). Keep it across restarts: the server knows the worker by this identity" group:"p2p"`
}

const (
	llamaCPPRPCBinaryName = "llama-cpp-rpc-server"
	// llamaCPPBinaryEnv is the variable the backend's run.sh reads to run a
	// different binary than the one it would pick. A run.sh without it
	// ignores the request and starts the gRPC server.
	llamaCPPBinaryEnv = "LLAMACPP_BINARY"
	llamaCPPGalleryName   = "llama-cpp"
)

func findLLamaCPPBackend(galleries string, systemState *system.SystemState, requireIntegrity bool) (string, error) {
	backends, err := gallery.ListSystemBackends(systemState)
	if err != nil {
		xlog.Warn("Failed listing system backends", "error", err)
		return "", err
	}
	xlog.Debug("System backends", "backends", backends)

	backend, ok := backends.Get(llamaCPPGalleryName)
	if !ok {
		ml := model.NewModelLoader(systemState)
		var gals []config.Gallery
		if err := json.Unmarshal([]byte(galleries), &gals); err != nil {
			xlog.Error("failed loading galleries", "error", err)
			return "", err
		}
		err := gallery.InstallBackendFromGallery(context.Background(), gals, systemState, ml, llamaCPPGalleryName, nil, true, requireIntegrity)
		if err != nil {
			xlog.Error("llama-cpp backend not found, failed to install it", "error", err)
			return "", err
		}
		// List again: the entry read before the install is empty, and its
		// directory would resolve to the current directory.
		backends, err = gallery.ListSystemBackends(systemState)
		if err != nil {
			return "", err
		}
		if backend, ok = backends.Get(llamaCPPGalleryName); !ok {
			return "", errors.New("llama-cpp backend not found after install")
		}
	}
	// Fail early when the RPC server is not part of the install, then hand
	// back the run file: llamaCPPRPCStart decides how to start it.
	if _, err := llamaCPPRPCServerPath(backend.RunFile); err != nil {
		return "", err
	}
	return backend.RunFile, nil
}

// llamaCPPRPCServerPath returns the RPC server executable next to the run
// file of the llama-cpp backend. On Windows the file has the .exe suffix.
func llamaCPPRPCServerPath(runFile string) (string, error) {
	if runFile == "" {
		return "", errors.New("llama-cpp backend not found, install it first")
	}
	rpcServer := filepath.Join(filepath.Dir(runFile), hostExecutableName(llamaCPPRPCBinaryName))
	if _, err := os.Stat(rpcServer); err != nil {
		return "", fmt.Errorf("the installed llama-cpp backend has no %s: install a llama-cpp build that includes the RPC server: %w", filepath.Base(rpcServer), err)
	}
	return rpcServer, nil
}

// llamaCPPRPCCommand returns the program, argv and environment that start
// the RPC server the way the backend's run.sh starts its other binaries: on
// Linux, with the backend's lib/ first on LD_LIBRARY_PATH and through the
// bundled lib/ld.so when there is one. GPU builds keep their runtime
// libraries there (libcudart for CUDA), so started directly they fail to load.
func llamaCPPRPCCommand(goos, rpcServer string, args, env []string) (string, []string, []string) {
	direct := append([]string{rpcServer}, args...)
	if goos != "linux" {
		return rpcServer, direct, env
	}
	libDir := filepath.Join(filepath.Dir(rpcServer), "lib")
	if info, err := os.Stat(libDir); err != nil || !info.IsDir() {
		return rpcServer, direct, env
	}

	const key = "LD_LIBRARY_PATH="
	paths := []string{libDir}
	merged := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, key); ok {
			if value != "" {
				paths = append(paths, value)
			}
		} else {
			merged = append(merged, entry)
		}
	}
	merged = append(merged, key+strings.Join(paths, ":"))

	loader := filepath.Join(libDir, "ld.so")
	if _, err := os.Stat(loader); err == nil {
		return loader, append([]string{loader}, direct...), merged
	}
	return rpcServer, direct, merged
}

// llamaCPPRunShSelectsBinary reports whether the run.sh at path lets the
// caller choose the binary through LLAMACPP_BINARY. Backends installed before
// that option existed do not, and would start the gRPC server with RPC
// arguments.
func llamaCPPRunShSelectsBinary(path string) bool {
	if filepath.Base(path) != "run.sh" {
		return false
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return bytes.Contains(content, []byte(llamaCPPBinaryEnv))
}

// llamaCPPRPCStart returns the program, argv and environment that start the
// RPC server of the llama-cpp backend whose run file is runFile.
//
// On Linux, when the backend's run.sh can run another binary, the server is
// started through it: run.sh then sets up the library path, the GPU runtime
// variables and the bundled loader, so nothing of that is repeated here.
// Otherwise (an older backend, Windows, macOS) the server is started with
// llamaCPPRPCCommand. macOS is left on the direct start because it has never
// gone through run.sh and nothing here tests it.
func llamaCPPRPCStart(goos, runFile string, args, env []string) (string, []string, []string) {
	rpcServer := filepath.Join(filepath.Dir(runFile), executableName(goos, llamaCPPRPCBinaryName))
	if goos != "linux" || !llamaCPPRunShSelectsBinary(runFile) {
		return llamaCPPRPCCommand(goos, rpcServer, args, env)
	}

	merged := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, llamaCPPBinaryEnv+"=") {
			merged = append(merged, entry)
		}
	}
	merged = append(merged, llamaCPPBinaryEnv+"="+llamaCPPRPCBinaryName)
	return runFile, append([]string{runFile}, args...), merged
}

func (r *LLamaCPP) Run(ctx *cliContext.Context) error {
	if r.Join != "" {
		return r.runP2P()
	}

	if len(os.Args) < 4 {
		return fmt.Errorf("usage: local-ai worker llama-cpp-rpc -- <llama-rpc-server-args>")
	}

	systemState, err := system.GetSystemState(
		system.WithBackendPath(r.BackendsPath),
		system.WithBackendSystemPath(r.BackendsSystemPath),
		system.WithDisableBackendInstall(r.DisableBackendInstall),
	)
	if err != nil {
		return err
	}
	grpcProcess, err := findLLamaCPPBackend(r.BackendGalleries, systemState, r.RequireBackendIntegrity)
	if err != nil {
		return err
	}

	path, argv, env := llamaCPPRPCStart(runtime.GOOS, grpcProcess, strings.Fields(r.ExtraLLamaCPPArgs), os.Environ())
	return execBackend(path, argv, env)
}
