package backend

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/trace"

	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
)

func ModelTranscription(audio, language string, translate, diarize bool, prompt string, ml *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (*schema.TranscriptionResult, error) {
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

	var startTime time.Time
	var audioSnippet map[string]any
	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems)
		startTime = time.Now()
		// Capture audio before the backend call — the backend may delete the file.
		audioSnippet = trace.AudioSnippet(audio)
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
		if appConfig.EnableTracing {
			errData := map[string]any{
				"audio_file": audio,
				"language":   language,
				"translate":  translate,
				"diarize":    diarize,
				"prompt":     prompt,
			}
			if audioSnippet != nil {
				maps.Copy(errData, audioSnippet)
			}
			trace.RecordBackendTrace(trace.BackendTrace{
				Timestamp: startTime,
				Duration:  time.Since(startTime),
				Type:      trace.BackendTraceTranscription,
				ModelName: modelConfig.Name,
				Backend:   modelConfig.Backend,
				Summary:   trace.TruncateString(audio, 200),
				Error:     err.Error(),
				Data:      errData,
			})
		}
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
			schema.TranscriptionSegment{
				Text:    s.Text,
				Id:      int(s.Id),
				Start:   time.Duration(s.Start),
				End:     time.Duration(s.End),
				Tokens:  tks,
				Speaker: s.Speaker,
			})
	}

	if appConfig.EnableTracing {
		data := map[string]any{
			"audio_file":     audio,
			"language":       language,
			"translate":      translate,
			"diarize":        diarize,
			"prompt":         prompt,
			"result_text":    tr.Text,
			"segments_count": len(tr.Segments),
		}
		if audioSnippet != nil {
			maps.Copy(data, audioSnippet)
		}
		trace.RecordBackendTrace(trace.BackendTrace{
			Timestamp: startTime,
			Duration:  time.Since(startTime),
			Type:      trace.BackendTraceTranscription,
			ModelName: modelConfig.Name,
			Backend:   modelConfig.Backend,
			Summary:   trace.TruncateString(audio+" -> "+tr.Text, 200),
			Data:      data,
		})
	}

	return tr, err
}
