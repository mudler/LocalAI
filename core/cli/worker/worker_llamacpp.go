package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

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
	}
	backendPath := filepath.Dir(backend.RunFile)

	if backendPath == "" {
		return "", errors.New("llama-cpp backend not found, install it first")
	}

	grpcProcess := filepath.Join(
		backendPath,
		llamaCPPRPCBinaryName,
	)

	return grpcProcess, nil
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

	args := strings.Split(r.ExtraLLamaCPPArgs, " ")

	args = append([]string{grpcProcess}, args...)

	return syscall.Exec(
		grpcProcess,
		args,
		os.Environ())
}
