package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/core/schema"

	"github.com/go-skynet/LocalAI/pkg/grpc/proto"
	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/go-skynet/LocalAI/pkg/utils"
)

type TextToSpeechBackendService struct {
	ml        *model.ModelLoader
	bcl       *config.BackendConfigLoader
	appConfig *config.ApplicationConfig
	// commandChannel  chan *schema.TTSRequest
	// responseChannel chan utils.ErrorOr[*string]
}

func NewTextToSpeechBackendService(ml *model.ModelLoader, bcl *config.BackendConfigLoader, appConfig *config.ApplicationConfig) *TextToSpeechBackendService {
	return &TextToSpeechBackendService{
		ml:        ml,
		bcl:       bcl,
		appConfig: appConfig,
	}
}

func (ttsbs *TextToSpeechBackendService) TextToAudioFile(request *schema.TTSRequest) <-chan utils.ErrorOr[*string] {
	responseChannel := make(chan utils.ErrorOr[*string])
	go func(request *schema.TTSRequest) {
		cfg, err := config.LoadBackendConfigFileByName(request.Model, ttsbs.bcl, ttsbs.appConfig)
		if err != nil {
			responseChannel <- utils.ErrorOr[*string]{Error: err}
			close(responseChannel)
			return
		}

		if request.Backend != "" {
			cfg.Backend = request.Backend
		}

		outFile, _, err := modelTTS(cfg.Backend, request.Input, cfg.Model, ttsbs.ml, ttsbs.appConfig, cfg)
		if err != nil {
			responseChannel <- utils.ErrorOr[*string]{Error: err}
			close(responseChannel)
			return
		}
		responseChannel <- utils.ErrorOr[*string]{Value: &outFile}
		close(responseChannel)
	}(request)
	return responseChannel
}

func modelTTS(backend, text, modelFile string, loader *model.ModelLoader, appConfig *config.ApplicationConfig, backendConfig *config.BackendConfig) (string, *proto.Result, error) {
	bb := backend
	if bb == "" {
		bb = model.PiperBackend
	}

	grpcOpts := gRPCModelOpts(backendConfig)

	opts := modelOpts(&config.BackendConfig{}, appConfig, []model.Option{
		model.WithBackendString(bb),
		model.WithModel(modelFile),
		model.WithContext(appConfig.Context),
		model.WithAssetDir(appConfig.AssetsDestination),
		model.WithLoadGRPCLoadModelOpts(grpcOpts),
	})
	piperModel, err := loader.BackendLoader(opts...)
	if err != nil {
		return "", nil, err
	}

	if piperModel == nil {
		return "", nil, fmt.Errorf("could not load piper model")
	}

	if err := os.MkdirAll(appConfig.AudioDir, 0755); err != nil {
		return "", nil, fmt.Errorf("failed creating audio directory: %s", err)
	}

	fileName := generateUniqueFileName(appConfig.AudioDir, "piper", ".wav")
	filePath := filepath.Join(appConfig.AudioDir, fileName)

	// If the model file is not empty, we pass it joined with the model path
	modelPath := ""
	if modelFile != "" {
		if bb != model.TransformersMusicGen {
			modelPath = filepath.Join(loader.ModelPath, modelFile)
			if err := utils.VerifyPath(modelPath, appConfig.ModelPath); err != nil {
				return "", nil, err
			}
		} else {
			modelPath = modelFile
		}
	}

	res, err := piperModel.TTS(context.Background(), &proto.TTSRequest{
		Text:  text,
		Model: modelPath,
		Dst:   filePath,
	})

	return filePath, res, err
}

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
