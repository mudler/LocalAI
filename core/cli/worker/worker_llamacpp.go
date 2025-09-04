package worker

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/cli/signals"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/rs/zerolog/log"
)

type LLamaCPP struct {
	WorkerFlags `embed:""`
}

const (
	llamaCPPRPCBinaryName = "llama-cpp-rpc-server"
)

func findLLamaCPPBackend(systemState *system.SystemState) (string, error) {
	backends, err := gallery.ListSystemBackends(systemState)
	if err != nil {
		log.Warn().Msgf("Failed listing system backends: %s", err)
		return "", err
	}
	log.Debug().Msgf("System backends: %v", backends)

	backend, ok := backends.Get("llama-cpp")
	if !ok {
		return "", errors.New("llama-cpp backend not found, install it first")
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

	if len(os.Args) < 4 {
		return fmt.Errorf("usage: local-ai worker llama-cpp-rpc -- <llama-rpc-server-args>")
	}

	systemState, err := system.GetSystemState(
		system.WithBackendPath(r.BackendsPath),
		system.WithBackendSystemPath(r.BackendsSystemPath),
	)
	if err != nil {
		return err
	}
	grpcProcess, err := findLLamaCPPBackend(systemState)
	if err != nil {
		return err
	}

	args := strings.Split(r.ExtraLLamaCPPArgs, " ")

	args = append([]string{grpcProcess}, args...)

	signals.Handler(nil)

	return syscall.Exec(
		grpcProcess,
		args,
		os.Environ())
}
