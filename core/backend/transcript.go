package backend

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"

	"github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
)

func ModelTranscription(audio, language string, translate bool, ml *model.ModelLoader, backendConfig config.BackendConfig, appConfig *config.ApplicationConfig) (*schema.TranscriptionResult, error) {

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

	r, err := whisperModel.AudioTranscription(context.Background(), &proto.TranscriptRequest{
		Dst:       audio,
		Language:  language,
		Translate: translate,
		Threads:   uint32(*backendConfig.Threads),
	})
	if err != nil {
		return nil, err
	}
	tr := &schema.TranscriptionResult{
		Text: r.Text,
	}
	for _, s := range r.Segments {
		var tks []int
		for _, t := range s.Tokens {
			tks = append(tks, int(t))
		}
		tr.Segments = append(tr.Segments,
			schema.Segment{
				Text:   s.Text,
				Id:     int(s.Id),
				Start:  time.Duration(s.Start),
				End:    time.Duration(s.End),
				Tokens: tks,
			})
	}
	return tr, err
}
