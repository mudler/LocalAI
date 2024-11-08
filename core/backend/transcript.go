package backend

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"

	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
)

func ModelTranscription(audio, language string, translate bool, ml *model.ModelLoader, backendConfig config.BackendConfig, appConfig *config.ApplicationConfig) (*schema.TranscriptionResult, error) {

	if backendConfig.Backend == "" {
		backendConfig.Backend = model.WhisperBackend
	}

	opts := ModelOptions(backendConfig, appConfig)

	transcriptionModel, err := ml.Load(opts...)
	if err != nil {
		return nil, err
	}

	if transcriptionModel == nil {
		return nil, fmt.Errorf("could not load transcription model")
	}

	r, err := transcriptionModel.AudioTranscription(context.Background(), &proto.TranscriptRequest{
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
