package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/core/backend"
	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/rs/zerolog/log"
)

type SoundGenerationCMD struct {
	Text []string `arg:""`

	Backend                string   `short:"b" required:"" help:"Backend to run the SoundGeneration model"`
	Model                  string   `short:"m" required:"" help:"Model name to run the SoundGeneration"`
	Duration               string   `short:"d" help:"If specified, the length of audio to generate in seconds"`
	Temperature            string   `short:"t" help:"If specified, the temperature of the generation"`
	InputFile              string   `short:"i" help:"If specified, the input file to condition generation upon"`
	InputFileSampleDivisor string   `short:"f" help:"If InputFile and this divisor is specified, the first portion of the sample file will be used"`
	DoSample               bool     `short:"s" default:"true" help:"Enables sampling from the model. Better quality at the cost of speed. Defaults to enabled."`
	OutputFile             string   `short:"o" type:"path" help:"The path to write the output wav file"`
	ModelsPath             string   `env:"LOCALAI_MODELS_PATH,MODELS_PATH" type:"path" default:"${basepath}/models" help:"Path containing models used for inferencing" group:"storage"`
	BackendAssetsPath      string   `env:"LOCALAI_BACKEND_ASSETS_PATH,BACKEND_ASSETS_PATH" type:"path" default:"/tmp/localai/backend_data" help:"Path used to extract libraries that are required by some of the backends in runtime" group:"storage"`
	ExternalGRPCBackends   []string `env:"LOCALAI_EXTERNAL_GRPC_BACKENDS,EXTERNAL_GRPC_BACKENDS" help:"A list of external grpc backends" group:"backends"`
}

func parseToFloat32Ptr(input string) *float32 {
	f, err := strconv.ParseFloat(input, 32)
	if err != nil {
		return nil
	}
	f2 := float32(f)
	return &f2
}

func parseToInt32Ptr(input string) *int32 {
	i, err := strconv.ParseInt(input, 10, 32)
	if err != nil {
		return nil
	}
	i2 := int32(i)
	return &i2
}

func (t *SoundGenerationCMD) Run(ctx *cliContext.Context) error {
	outputFile := t.OutputFile
	outputDir := t.BackendAssetsPath
	if outputFile != "" {
		outputDir = filepath.Dir(outputFile)
	}

	text := strings.Join(t.Text, " ")

	externalBackends := make(map[string]string)
	// split ":" to get backend name and the uri
	for _, v := range t.ExternalGRPCBackends {
		backend := v[:strings.IndexByte(v, ':')]
		uri := v[strings.IndexByte(v, ':')+1:]
		externalBackends[backend] = uri
		fmt.Printf("TMP externalBackends[%q]=%q\n\n", backend, uri)
	}

	opts := &config.ApplicationConfig{
		ModelPath:            t.ModelsPath,
		Context:              context.Background(),
		AudioDir:             outputDir,
		AssetsDestination:    t.BackendAssetsPath,
		ExternalGRPCBackends: externalBackends,
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
	options.Backend = t.Backend

	var inputFile *string
	if t.InputFile != "" {
		inputFile = &t.InputFile
	}

	filePath, _, err := backend.SoundGeneration(t.Model, text,
		parseToFloat32Ptr(t.Duration), parseToFloat32Ptr(t.Temperature), &t.DoSample,
		inputFile, parseToInt32Ptr(t.InputFileSampleDivisor), ml, opts, options)

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
