package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/utils"
)

func SoundGeneration(
	modelFile string,
	text string,
	duration *float32,
	temperature *float32,
	doSample *bool,
	sourceFile *string,
	sourceDivisor *int32,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	backendConfig config.BackendConfig,
) (string, *proto.Result, error) {

	opts := ModelOptions(backendConfig, appConfig, model.WithModel(modelFile))
	soundGenModel, err := loader.Load(opts...)
	if err != nil {
		return "", nil, err
	}

	if soundGenModel == nil {
		return "", nil, fmt.Errorf("could not load sound generation model")
	}

	if err := os.MkdirAll(appConfig.AudioDir, 0750); err != nil {
		return "", nil, fmt.Errorf("failed creating audio directory: %s", err)
	}

	fileName := utils.GenerateUniqueFileName(appConfig.AudioDir, "sound_generation", ".wav")
	filePath := filepath.Join(appConfig.AudioDir, fileName)

	res, err := soundGenModel.SoundGeneration(context.Background(), &proto.SoundGenerationRequest{
		Text:        text,
		Model:       modelFile,
		Dst:         filePath,
		Sample:      doSample,
		Duration:    duration,
		Temperature: temperature,
		Src:         sourceFile,
		SrcDivisor:  sourceDivisor,
	})

	// return RPC error if any
	if !res.Success {
		return "", nil, fmt.Errorf(res.Message)
	}

	return filePath, res, err
}
