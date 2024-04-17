package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-skynet/LocalAI/core/backend"
	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/go-skynet/LocalAI/pkg/model"
)

type TTSCMD struct {
	Text []string `arg:""`

	Backend           string `short:"b" default:"piper" help:"Backend to run the TTS model"`
	Model             string `short:"m" required:"" help:"Model name to run the TTS"`
	Voice             string `short:"v" help:"Voice name to run the TTS"`
	OutputFile        string `short:"o" type:"path" help:"The path to write the output wav file"`
	ModelsPath        string `env:"LOCALAI_MODELS_PATH,MODELS_PATH" type:"path" default:"${basepath}/models" help:"Path containing models used for inferencing" group:"storage"`
	BackendAssetsPath string `env:"LOCALAI_BACKEND_ASSETS_PATH,BACKEND_ASSETS_PATH" type:"path" default:"/tmp/localai/backend_data" help:"Path used to extract libraries that are required by some of the backends in runtime" group:"storage"`
}

func (t *TTSCMD) Run(ctx *Context) error {
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

	defer ml.StopAllGRPC()

	ttsbs := backend.NewTextToSpeechBackendService(ml, config.NewBackendConfigLoader(), opts)

	request := &schema.TTSRequest{
		Model:   t.Model,
		Input:   text,
		Backend: t.Backend,
		Voice:   t.Voice,
	}

	resultsChannel := ttsbs.TextToAudioFile(request)

	rawResult := <-resultsChannel

	if rawResult.Error != nil {
		return rawResult.Error
	}
	if outputFile != "" {
		if err := os.Rename(*rawResult.Value, outputFile); err != nil {
			return err
		}
		fmt.Printf("Generated file %q\n", outputFile)
	} else {
		fmt.Printf("Generated file %q\n", *rawResult.Value)
	}
	return nil
}
