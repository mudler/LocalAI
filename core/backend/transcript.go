package backend

import (
	"context"
	"fmt"

	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/core/schema"

	"github.com/go-skynet/LocalAI/pkg/grpc/proto"
	model "github.com/go-skynet/LocalAI/pkg/model"
)

func ModelTranscription(audio, language string, ml *model.ModelLoader, backendConfig config.BackendConfig, appConfig *config.ApplicationConfig) (*schema.TranscriptionResult, error) {

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
