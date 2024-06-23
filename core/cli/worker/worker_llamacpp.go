package worker

import (
	"fmt"
	"os"
	"syscall"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/pkg/assets"
	"github.com/mudler/LocalAI/pkg/library"
	"github.com/rs/zerolog/log"
)

type LLamaCPP struct {
	Args        []string `arg:"" optional:"" name:"models" help:"Model configuration URLs to load"`
	WorkerFlags `embed:""`
}

func (r *LLamaCPP) Run(ctx *cliContext.Context) error {
	// Extract files from the embedded FS
	err := assets.ExtractFiles(ctx.BackendAssets, r.BackendAssetsPath)
	log.Debug().Msgf("Extracting backend assets files to %s", r.BackendAssetsPath)
	if err != nil {
		log.Warn().Msgf("Failed extracting backend assets files: %s (might be required for some backends to work properly, like gpt4all)", err)
	}

	if len(os.Args) < 4 {
		return fmt.Errorf("usage: local-ai worker llama-cpp-rpc -- <llama-rpc-server-args>")
	}

	grpcProcess := assets.ResolvePath(
		r.BackendAssetsPath,
		"util",
		"llama-cpp-rpc-server",
	)

	args := os.Args[4:]
	args, grpcProcess = library.LoadLDSO(r.BackendAssetsPath, args, grpcProcess)

	args = append([]string{grpcProcess}, args...)
	return syscall.Exec(
		grpcProcess,
		args,
		os.Environ())
}
