package backend

import (
	"context"
	"fmt"

	"github.com/go-skynet/LocalAI/core/schema"

	"github.com/go-skynet/LocalAI/pkg/grpc/proto"
	model "github.com/go-skynet/LocalAI/pkg/model"
)

func ModelTranscription(audio, language string, ml *model.ModelLoader, c schema.Config, o *schema.StartupOptions) (*schema.Result, error) {

	opts := modelOpts(c, o, []model.Option{
		model.WithBackendString(model.WhisperBackend),
		model.WithModel(c.Model),
		model.WithContext(o.Context),
		model.WithThreads(uint32(c.Threads)),
		model.WithAssetDir(o.AssetsDestination),
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
		Threads:  uint32(c.Threads),
	})
}
