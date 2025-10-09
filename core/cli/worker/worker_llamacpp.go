package worker

import (
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
	"github.com/rs/zerolog/log"
)

type LLamaCPP struct {
	WorkerFlags `embed:""`
}

const (
	llamaCPPRPCBinaryName = "llama-cpp-rpc-server"
	llamaCPPGalleryName   = "llama-cpp"
)

func findLLamaCPPBackend(galleries string, systemState *system.SystemState) (string, error) {
	backends, err := gallery.ListSystemBackends(systemState)
	if err != nil {
		log.Warn().Msgf("Failed listing system backends: %s", err)
		return "", err
	}
	log.Debug().Msgf("System backends: %v", backends)

	backend, ok := backends.Get(llamaCPPGalleryName)
	if !ok {
		ml := model.NewModelLoader(systemState, true)
		var gals []config.Gallery
		if err := json.Unmarshal([]byte(galleries), &gals); err != nil {
			log.Error().Err(err).Msg("failed loading galleries")
			return "", err
		}
		err := gallery.InstallBackendFromGallery(gals, systemState, ml, llamaCPPGalleryName, nil, true)
		if err != nil {
			log.Error().Err(err).Msg("llama-cpp backend not found, failed to install it")
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
	grpcProcess, err := findLLamaCPPBackend(r.BackendGalleries, systemState)
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
