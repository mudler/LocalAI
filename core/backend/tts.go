package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/rs/zerolog/log"

	"github.com/go-skynet/LocalAI/pkg/concurrency"
	"github.com/go-skynet/LocalAI/pkg/grpc/proto"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/go-skynet/LocalAI/pkg/utils"
)

type TextToSpeechBackendService struct {
	ml        *model.ModelLoader
	bcl       *config.BackendConfigLoader
	appConfig *config.ApplicationConfig
}

func NewTextToSpeechBackendService(ml *model.ModelLoader, bcl *config.BackendConfigLoader, appConfig *config.ApplicationConfig) *TextToSpeechBackendService {
	return &TextToSpeechBackendService{
		ml:        ml,
		bcl:       bcl,
		appConfig: appConfig,
	}
}

func (ttsbs *TextToSpeechBackendService) TextToAudioFile(request *schema.TTSRequest) *concurrency.JobResult[*schema.TTSRequest, string] {
	jr, wjr := concurrency.NewJobResult[*schema.TTSRequest, string](request)

	go func(wjr *concurrency.WritableJobResult[*schema.TTSRequest, string]) {
		if request.Model == "" {
			wjr.SetResult("", fmt.Errorf("model is required, no default available"))
			return
		}
		bc, err := ttsbs.bcl.LoadBackendConfigFileByName(request.Model, ttsbs.appConfig.ModelPath,
			config.LoadOptionDebug(ttsbs.appConfig.Debug),
			config.LoadOptionThreads(ttsbs.appConfig.Threads),
			config.LoadOptionContextSize(ttsbs.appConfig.ContextSize),
			config.LoadOptionF16(ttsbs.appConfig.F16),
		)
		if err != nil || bc == nil {
			log.Error().Err(err).Str("modelName", request.Model).Str("modelPath", ttsbs.appConfig.ModelPath).Msg("unable to load backend config")
			wjr.SetResult("", err)
			return
		}

		if request.Backend != "" { // Allow users to specify a backend to use that overrides config.
			bc.Backend = request.Backend
		}
		// TODO consider merging the below function in, but leave it seperated for diff reasons in the first PR
		dst, err := ttsbs.modelTTS(request.Backend, request.Input, bc.Model, request.Voice, request.Language, *bc)
		log.Debug().Str("dst", dst).Err(err).Msg("modelTTS result in goroutine")
		wjr.SetResult(dst, err)
	}(wjr)

	return jr
}

func (ttsbs *TextToSpeechBackendService) modelTTS(
	backend string,
	text string,
	modelFile string,
	voice string,
	language string,
	backendConfig config.BackendConfig,
) (string, error) {
	bb := backend
	if bb == "" {
		bb = model.PiperBackend
	}

	grpcOpts := gRPCModelOpts(backendConfig)

	opts := modelOpts(config.BackendConfig{}, ttsbs.appConfig, []model.Option{
		model.WithBackendString(bb),
		model.WithModel(modelFile),
		model.WithContext(ttsbs.appConfig.Context),
		model.WithAssetDir(ttsbs.appConfig.AssetsDestination),
		model.WithLoadGRPCLoadModelOpts(grpcOpts),
	})
	ttsModel, err := ttsbs.ml.BackendLoader(opts...)
	if err != nil {
		return "", err
	}

	if ttsModel == nil {
		return "", fmt.Errorf("could not load piper model")
	}

	if ttsbs.appConfig.AudioDir == "" {
		return "", fmt.Errorf("ApplicationConfig.AudioDir not set, cannot continue")
	}

	// Shouldn't be needed anymore. Consider removing later
	if err := os.MkdirAll(ttsbs.appConfig.AudioDir, 0750); err != nil {
		return "", fmt.Errorf("failed` creating audio directory: %s", err)
	}

	fileName := generateUniqueFileName(ttsbs.appConfig.AudioDir, "tts", ".wav")
	filePath := filepath.Join(ttsbs.appConfig.AudioDir, fileName)

	log.Debug().Str("filePath", filePath).Msg("computed output filePath")

	// If the model file is not empty, we pass it joined with the model path
	modelPath := ""
	if modelFile != "" {
		// If the model file is not empty, we pass it joined with the model path
		// Checking first that it exists and is not outside ModelPath
		// TODO: we should actually first check if the modelFile is looking like
		// a FS path
		mp := filepath.Join(ttsbs.appConfig.ModelPath, modelFile)
		if _, err := os.Stat(mp); err == nil {
			if err := utils.VerifyPath(mp, ttsbs.appConfig.ModelPath); err != nil {
				return "", err
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

	// return RPC error if any
	if !res.Success {
		return "", fmt.Errorf(res.Message)
	}

	return filePath, err
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
