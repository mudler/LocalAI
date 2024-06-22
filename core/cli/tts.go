package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/backend"
	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/rs/zerolog/log"
)

type TTSCMD struct {
	Text []string `arg:""`

	Backend           string `short:"b" default:"piper" help:"Backend to run the TTS model"`
	Model             string `short:"m" required:"" help:"Model name to run the TTS"`
	Voice             string `short:"v" help:"Voice name to run the TTS"`
	Language          string `short:"l" help:"Language to use with the TTS"`
	OutputFile        string `short:"o" type:"path" help:"The path to write the output wav file"`
	ModelsPath        string `env:"LOCALAI_MODELS_PATH,MODELS_PATH" type:"path" default:"${basepath}/models" help:"Path containing models used for inferencing" group:"storage"`
	BackendAssetsPath string `env:"LOCALAI_BACKEND_ASSETS_PATH,BACKEND_ASSETS_PATH" type:"path" default:"/tmp/localai/backend_data" help:"Path used to extract libraries that are required by some of the backends in runtime" group:"storage"`
}

func (t *TTSCMD) Run(ctx *cliContext.Context) error {
	outputFile := t.OutputFile
	outputDir := t.BackendAssetsPath
	if outputFile != "" {
		outputDir = filepath.Dir(outputFile)
	}

	text := strings.Join(t.Text, " ")

	opts := &config.ApplicationConfig{
		ModelPath:         t.ModelsPath,
		Context:           context.Background(),
		AudioDir:          outputDir,
		AssetsDestination: t.BackendAssetsPath,
	}
	ml := model.NewModelLoader(opts.ModelPath)

	defer func() {
		err := ml.StopAllGRPC()
		if err != nil {
			log.Error().Err(err).Msg("unable to stop all grpc processes")
		}
	}()

	options := config.BackendConfig{}
	options.SetDefaults()

	filePath, _, err := backend.ModelTTS(t.Backend, text, t.Model, t.Voice, t.Language, ml, opts, options)
	if err != nil {
		return err
	}
	if outputFile != "" {
		if err := os.Rename(filePath, outputFile); err != nil {
			return err
		}
		fmt.Printf("Generate file %s\n", outputFile)
	} else {
		fmt.Printf("Generate file %s\n", filePath)
	}
	return nil
}
