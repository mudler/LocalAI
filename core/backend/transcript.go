package backend

import (
	"context"
	"fmt"

	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/core/schema"

	"github.com/go-skynet/LocalAI/pkg/concurrency"
	"github.com/go-skynet/LocalAI/pkg/grpc/proto"
	"github.com/go-skynet/LocalAI/pkg/model"
)

type TranscriptionBackendService struct {
	ml        *model.ModelLoader
	bcl       *config.BackendConfigLoader
	appConfig *config.ApplicationConfig
}

func NewTranscriptionBackendService(ml *model.ModelLoader, bcl *config.BackendConfigLoader, appConfig *config.ApplicationConfig) *TranscriptionBackendService {
	return &TranscriptionBackendService{
		ml:        ml,
		bcl:       bcl,
		appConfig: appConfig,
	}
}

func (tbs *TranscriptionBackendService) Transcribe(request *schema.OpenAIRequest) <-chan concurrency.ErrorOr[*schema.TranscriptionResult] {
	responseChannel := make(chan concurrency.ErrorOr[*schema.TranscriptionResult])
	go func(request *schema.OpenAIRequest) {
		bc, request, err := tbs.bcl.LoadBackendConfigForModelAndOpenAIRequest(request.Model, request, tbs.appConfig)
		if err != nil {
			responseChannel <- concurrency.ErrorOr[*schema.TranscriptionResult]{Error: fmt.Errorf("failed reading parameters from request:%w", err)}
			close(responseChannel)
			return
		}

		tr, err := modelTranscription(request.File, request.Language, tbs.ml, bc, tbs.appConfig)
		if err != nil {
			responseChannel <- concurrency.ErrorOr[*schema.TranscriptionResult]{Error: err}
			close(responseChannel)
			return
		}
		responseChannel <- concurrency.ErrorOr[*schema.TranscriptionResult]{Value: tr}
		close(responseChannel)
	}(request)
	return responseChannel
}

func modelTranscription(audio, language string, ml *model.ModelLoader, backendConfig *config.BackendConfig, appConfig *config.ApplicationConfig) (*schema.TranscriptionResult, error) {

	opts := modelOpts(backendConfig, appConfig, []model.Option{
		model.WithBackendString(model.WhisperBackend),
		model.WithModel(backendConfig.Model),
		model.WithContext(appConfig.Context),
		model.WithThreads(uint32(*backendConfig.Threads)),
		model.WithAssetDir(appConfig.AssetsDestination),
	})

	whisperModel, err := ml.BackendLoader(opts...)
	if err != nil {
		return nil, err
	}

	if whisperModel == nil {
		return nil, fmt.Errorf("could not load whisper model")
	}

	return whisperModel.AudioTranscription(context.Background(), &proto.TranscriptRequest{
		Dst:      audio,
		Language: language,
		Threads:  uint32(*backendConfig.Threads),
	})
}
