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
	text string,
	duration *float32,
	temperature *float32,
	doSample *bool,
	sourceFile *string,
	sourceDivisor *int32,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	modelConfig config.ModelConfig,
) (string, *proto.Result, error) {

	opts := ModelOptions(modelConfig, appConfig)
	soundGenModel, err := loader.Load(opts...)
	if err != nil {
		return "", nil, err
	}
	defer loader.Close()

	if soundGenModel == nil {
		return "", nil, fmt.Errorf("could not load sound generation model")
	}

	if err := os.MkdirAll(appConfig.GeneratedContentDir, 0750); err != nil {
		return "", nil, fmt.Errorf("failed creating audio directory: %s", err)
	}

	audioDir := filepath.Join(appConfig.GeneratedContentDir, "audio")
	if err := os.MkdirAll(audioDir, 0750); err != nil {
		return "", nil, fmt.Errorf("failed creating audio directory: %s", err)
	}

	fileName := utils.GenerateUniqueFileName(audioDir, "sound_generation", ".wav")
	filePath := filepath.Join(audioDir, fileName)

	res, err := soundGenModel.SoundGeneration(context.Background(), &proto.SoundGenerationRequest{
		Text:        text,
		Model:       modelConfig.Model,
		Dst:         filePath,
		Sample:      doSample,
		Duration:    duration,
		Temperature: temperature,
		Src:         sourceFile,
		SrcDivisor:  sourceDivisor,
	})

	// return RPC error if any
	if !res.Success {
		return "", nil, fmt.Errorf("error during sound generation: %s", res.Message)
	}

	return filePath, res, err
}
