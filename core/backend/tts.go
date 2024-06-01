package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-skynet/LocalAI/core/config"

	"github.com/go-skynet/LocalAI/pkg/grpc/proto"
	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/go-skynet/LocalAI/pkg/utils"
)

func generateUniqueFileName(dir, baseName, ext string) string {
	counter := 1
	fileName := baseName + ext

	for {
		filePath := filepath.Join(dir, fileName)
		_, err := os.Stat(filePath)
		if os.IsNotExist(err) {
			return fileName
		}

		counter++
		fileName = fmt.Sprintf("%s_%d%s", baseName, counter, ext)
	}
}

func ModelTTS(
	backend,
	text,
	modelFile,
	voice ,
	language string,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	backendConfig config.BackendConfig,
) (string, *proto.Result, error) {
	bb := backend
	if bb == "" {
		bb = model.PiperBackend
	}

	grpcOpts := gRPCModelOpts(backendConfig)

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

	fileName := generateUniqueFileName(appConfig.AudioDir, "tts", ".wav")
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
		Text:  text,
		Model: modelPath,
		Voice: voice,
		Dst:   filePath,
		Language: &language,
	})

	// return RPC error if any
	if !res.Success {
		return "", nil, fmt.Errorf(res.Message)
	}

	return filePath, res, err
}
