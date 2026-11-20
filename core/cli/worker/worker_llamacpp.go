package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

	args := append([]string{grpcProcess}, strings.Fields(r.ExtraLLamaCPPArgs)...)

	return execBackend(grpcProcess, args, os.Environ())
}
