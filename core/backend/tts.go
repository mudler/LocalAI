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

func ModelTTS(
	text,
	voice,
	language string,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	modelConfig config.ModelConfig,
) (string, *proto.Result, error) {
	opts := ModelOptions(modelConfig, appConfig)
	ttsModel, err := loader.Load(opts...)
	if err != nil {
		return "", nil, err
	}
	defer loader.Close()

	if ttsModel == nil {
		return "", nil, fmt.Errorf("could not load tts model %q", modelConfig.Model)
	}

	audioDir := filepath.Join(appConfig.GeneratedContentDir, "audio")
	if err := os.MkdirAll(audioDir, 0750); err != nil {
		return "", nil, fmt.Errorf("failed creating audio directory: %s", err)
	}

	fileName := utils.GenerateUniqueFileName(audioDir, "tts", ".wav")
	filePath := filepath.Join(audioDir, fileName)

	// We join the model name to the model path here. This seems to only be done for TTS and is HIGHLY suspect.
	// This should be addressed in a follow up PR soon.
	// Copying it over nearly verbatim, as TTS backends are not functional without this.
	modelPath := ""
	// Checking first that it exists and is not outside ModelPath
	// TODO: we should actually first check if the modelFile is looking like
	// a FS path
	mp := filepath.Join(loader.ModelPath, modelConfig.Model)
	if _, err := os.Stat(mp); err == nil {
		if err := utils.VerifyPath(mp, appConfig.SystemState.Model.ModelsPath); err != nil {
			return "", nil, err
		}
		modelPath = mp
	} else {
		modelPath = modelConfig.Model // skip this step if it fails?????
	}

	res, err := ttsModel.TTS(context.Background(), &proto.TTSRequest{
		Text:     text,
		Model:    modelPath,
		Voice:    voice,
		Dst:      filePath,
		Language: &language,
	})
	if err != nil {
		return "", nil, err
	}

	// return RPC error if any
	if !res.Success {
		return "", nil, fmt.Errorf("error during TTS: %s", res.Message)
	}

	return filePath, res, err
}
