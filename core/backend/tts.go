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
	backend,
	text,
	modelFile,
	voice,
	language string,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	backendConfig config.BackendConfig,
) (string, *proto.Result, error) {
	bb := backend
	if bb == "" {
		bb = model.PiperBackend
	}

	grpcOpts := GRPCModelOpts(backendConfig)

	opts := modelOpts(config.BackendConfig{}, appConfig, []model.Option{
		model.WithBackendString(bb),
		model.WithModel(modelFile),
		model.WithContext(appConfig.Context),
		model.WithAssetDir(appConfig.AssetsDestination),
		model.WithLoadGRPCLoadModelOpts(grpcOpts),
	})
	ttsModel, err := loader.BackendLoader(opts...)
	if err != nil {
		return "", nil, err
	}

	if ttsModel == nil {
		return "", nil, fmt.Errorf("could not load piper model")
	}

	if err := os.MkdirAll(appConfig.AudioDir, 0750); err != nil {
		return "", nil, fmt.Errorf("failed creating audio directory: %s", err)
	}

	fileName := utils.GenerateUniqueFileName(appConfig.AudioDir, "tts", ".wav")
	filePath := filepath.Join(appConfig.AudioDir, fileName)

	// If the model file is not empty, we pass it joined with the model path
	modelPath := ""
	if modelFile != "" {
		// If the model file is not empty, we pass it joined with the model path
		// Checking first that it exists and is not outside ModelPath
		// TODO: we should actually first check if the modelFile is looking like
		// a FS path
		mp := filepath.Join(loader.ModelPath, modelFile)
		if _, err := os.Stat(mp); err == nil {
			if err := utils.VerifyPath(mp, appConfig.ModelPath); err != nil {
				return "", nil, err
			}
			modelPath = mp
		} else {
			modelPath = modelFile
		}
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
		return "", nil, fmt.Errorf(res.Message)
	}

	return filePath, res, err
}
