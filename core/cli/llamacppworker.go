package cli

import (
	"os"
	"syscall"

	"github.com/go-skynet/LocalAI/pkg/assets"
	"github.com/rs/zerolog/log"
)

type LLAMACPPWorkerCMD struct {
	Args              []string `arg:"" optional:"" name:"models" help:"Worker arguments: host port"`
	BackendAssetsPath string   `env:"LOCALAI_BACKEND_ASSETS_PATH,BACKEND_ASSETS_PATH" type:"path" default:"/tmp/localai/backend_data" help:"Path used to extract libraries that are required by some of the backends in runtime" group:"storage"`
}

func (r *LLAMACPPWorkerCMD) Run(ctx *Context) error {
	// Extract files from the embedded FS
	err := assets.ExtractFiles(ctx.BackendAssets, r.BackendAssetsPath)
	log.Debug().Msgf("Extracting backend assets files to %s", r.BackendAssetsPath)
	if err != nil {
		log.Warn().Msgf("Failed extracting backend assets files: %s (might be required for some backends to work properly, like gpt4all)", err)
	}

	return syscall.Exec(
		assets.ResolvePath(
			r.BackendAssetsPath,
			"util",
			"llama-cpp-rpc-server",
		),
		append([]string{
			assets.ResolvePath(
				r.BackendAssetsPath,
				"util",
				"llama-cpp-rpc-server",
			)}, r.Args...),
		os.Environ())
}
