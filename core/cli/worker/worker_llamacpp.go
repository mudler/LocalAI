package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/assets"
	"github.com/mudler/LocalAI/pkg/library"
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
	// Extract files from the embedded FS
	err := assets.ExtractFiles(ctx.BackendAssets, r.BackendAssetsPath)
	log.Debug().Msgf("Extracting backend assets files to %s", r.BackendAssetsPath)
	if err != nil {
		log.Warn().Msgf("Failed extracting backend assets files: %s (might be required for some backends to work properly)", err)
	}

	if len(os.Args) < 4 {
		return fmt.Errorf("usage: local-ai worker llama-cpp-rpc -- <llama-rpc-server-args>")
	}

	grpcProcess, err := findLLamaCPPBackend(r.BackendAssetsPath)
	if err != nil {
		return err
	}

	args := strings.Split(r.ExtraLLamaCPPArgs, " ")
	args, grpcProcess = library.LoadLDSO(r.BackendAssetsPath, args, grpcProcess)

	args = append([]string{grpcProcess}, args...)
	return syscall.Exec(
		grpcProcess,
		args,
		os.Environ())
}
