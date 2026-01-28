package backend

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"

	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
)

func ModelTranscription(audio, language string, translate bool, diarize bool, prompt, responseFormat string, ml *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (*schema.TranscriptionResult, error) {
	if modelConfig.Backend == "" {
		modelConfig.Backend = model.WhisperBackend
	}

	opts := ModelOptions(modelConfig, appConfig)

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
		Diarize:   diarize,
		Threads:   uint32(*modelConfig.Threads),
		Prompt:    prompt,
	})
	if err != nil {
		return nil, err
	}
	tr := new(schema.TranscriptionResult)
	if responseFormat == "" { // maintain backwards compatibility since previously response_format was not expected
		tr.Text = r.Text
	}
	for i, s := range r.Segments {
		var tks []int
		for _, t := range s.Tokens {
			tks = append(tks, int(t))
		}
		tr.Segments = append(tr.Segments,
			schema.TranscriptionSegment{
				Text:   s.Text,
				Id:     int(s.Id),
				Start:  time.Duration(s.Start),
				End:    time.Duration(s.End),
				Tokens: tks,
			})
		if responseFormat == "lrc" {
			tr.Output += fmt.Sprintf("[%s] %s/\n", fmtIntDuration(s.Start), s.Text)
		} else if responseFormat == "srt" {
			tr.Output += fmt.Sprintf("%d\n%s --> %s\n%s\n\n", i+1, fmtIntDuration(s.Start), fmtIntDuration(s.End), strings.TrimSpace(s.Text))
		}
	}
	return tr, err
}

func fmtIntDuration(i int64) string {
	d := time.Duration(i)
	return fmt.Sprintf("%02d:%02d:%02d", int(d.Seconds()/3600), int(d.Seconds()/60), int(d.Seconds())%60)
}
