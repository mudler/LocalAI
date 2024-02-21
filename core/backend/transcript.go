package backend

import (
	"context"
	"fmt"

	config "github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/core/schema"

	"github.com/go-skynet/LocalAI/core/options"
	"github.com/go-skynet/LocalAI/pkg/grpc/proto"
	model "github.com/go-skynet/LocalAI/pkg/model"
)

func ModelTranscription(audio, language string, loader *model.ModelLoader, c config.Config, o *options.Option) (*schema.Result, error) {

	opts := modelOpts(c, o, []model.Option{
		model.WithBackendString(model.WhisperBackend),
		model.WithModel(c.Model),
		model.WithContext(o.Context),
		model.WithThreads(uint32(c.Threads)),
		model.WithAssetDir(o.AssetsDestination),
	})

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
