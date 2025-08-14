package worker

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/rs/zerolog/log"
)

type LLamaCPP struct {
	WorkerFlags `embed:""`
}

const (
	llamaCPPRPCBinaryName = "llama-cpp-rpc-server"
)

func findLLamaCPPBackend(backendSystemPath string) (string, error) {
	backends, err := gallery.ListSystemBackends(backendSystemPath)
	if err != nil {
		log.Warn().Msgf("Failed listing system backends: %s", err)
		return "", err
	}
	log.Debug().Msgf("System backends: %v", backends)

	backendPath := ""
	backend, ok := backends.Get("llama-cpp")
	if !ok {
		return "", errors.New("llama-cpp backend not found, install it first")
	}
	backendPath = filepath.Dir(backend.RunFile)

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

	grpcProcess, err := findLLamaCPPBackend(r.BackendsPath)
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
