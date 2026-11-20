package worker

import (
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
	WorkerFlags `embed:""`
}

const (
	llamaCPPRPCBinaryName = "llama-cpp-rpc-server"
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
	return llamaCPPRPCServerPath(backend.RunFile)
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

func (r *LLamaCPP) Run(ctx *cliContext.Context) error {

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

	path, argv, env := llamaCPPRPCCommand(runtime.GOOS, grpcProcess, strings.Fields(r.ExtraLLamaCPPArgs), os.Environ())
	return execBackend(path, argv, env)
}
