package worker

import (
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

func findLLamaCPPBackend(backendSystemPath string) (string, error) {
	backends, err := gallery.ListSystemBackends(backendSystemPath)
	if err != nil {
		log.Warn().Msgf("Failed listing system backends: %s", err)
		return "", err
	}
	log.Debug().Msgf("System backends: %v", backends)

	backendPath := ""
	for b, path := range backends {
		if b == "llama-cpp" {
			backendPath = filepath.Dir(path)
			break
		}
	}

	if backendPath == "" {
		return "", fmt.Errorf("llama-cpp backend not found")
	}

	grpcProcess := filepath.Join(
		backendPath,
		"grpc-server",
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
