package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/rs/zerolog/log"

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
	backendConfig config.BackendConfig,
) (string, *proto.Result, error) {
	opts := ModelOptions(*&backendConfig, appConfig, []model.Option{
		model.WithDefaultBackendString(model.PiperBackend),
	})
	ttsModel, err := loader.BackendLoader(opts...)
	if err != nil {
		return "", nil, err
	}

	if ttsModel == nil {
		return "", nil, fmt.Errorf("could not load tts model %q", backendConfig.Model)
	}

	if err := os.MkdirAll(appConfig.AudioDir, 0750); err != nil {
		return "", nil, fmt.Errorf("failed creating audio directory: %s", err)
	}

	log.Warn().Str("config.Model", backendConfig.Model).Msg("ModelTTS before backend call")

	fileName := utils.GenerateUniqueFileName(appConfig.AudioDir, "tts", ".wav")
	filePath := filepath.Join(appConfig.AudioDir, fileName)

	res, err := ttsModel.TTS(context.Background(), &proto.TTSRequest{
		Text:     text,
		Model:    backendConfig.Model,
		Voice:    voice,
		Dst:      filePath,
		Language: &language,
	})
	if err != nil {
		return "", nil, err
	}

	// return RPC error if any
	if !res.Success {
		return "", nil, fmt.Errorf(res.Message)
	}

	return filePath, res, err
}
