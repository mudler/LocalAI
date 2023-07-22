package backend

import (
	"context"
	"fmt"

	config "github.com/go-skynet/LocalAI/api/config"

	"github.com/go-skynet/LocalAI/api/options"
	"github.com/go-skynet/LocalAI/pkg/grpc/proto"
	"github.com/go-skynet/LocalAI/pkg/grpc/whisper/api"
	model "github.com/go-skynet/LocalAI/pkg/model"
)

func ModelTranscription(audio, language string, loader *model.ModelLoader, c config.Config, o *options.Option) (*api.Result, error) {
	opts := []model.Option{
		model.WithBackendString(model.WhisperBackend),
		model.WithModelFile(c.Model),
		model.WithContext(o.Context),
		model.WithThreads(uint32(c.Threads)),
		model.WithAssetDir(o.AssetsDestination),
	}

	for k, v := range o.ExternalGRPCBackends {
		opts = append(opts, model.WithExternalBackend(k, v))
	}

	whisperModel, err := o.Loader.BackendLoader(opts...)
	if err != nil {
		return nil, err
	}

	if whisperModel == nil {
		return nil, fmt.Errorf("could not load whisper model")
	}

	return whisperModel.AudioTranscription(context.Background(), &proto.TranscriptRequest{
		Dst:      audio,
		Language: language,
		Threads:  uint32(c.Threads),
	})
}
