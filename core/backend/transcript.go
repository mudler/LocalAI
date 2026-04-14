package backend

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/trace"

	grpcPkg "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
)

// TranscriptionRequest groups the parameters accepted by ModelTranscription.
// Use this so callers don't have to pass long positional arg lists when they
// only care about a subset of fields.
type TranscriptionRequest struct {
	Audio                  string
	Language               string
	Translate              bool
	Diarize                bool
	Prompt                 string
	Temperature            float32
	TimestampGranularities []string
}

func (r *TranscriptionRequest) toProto(threads uint32) *proto.TranscriptRequest {
	return &proto.TranscriptRequest{
		Dst:                    r.Audio,
		Language:               r.Language,
		Translate:              r.Translate,
		Diarize:                r.Diarize,
		Threads:                threads,
		Prompt:                 r.Prompt,
		Temperature:            r.Temperature,
		TimestampGranularities: r.TimestampGranularities,
	}
}

func loadTranscriptionModel(ml *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (grpcPkg.Backend, error) {
	if modelConfig.Backend == "" {
		modelConfig.Backend = model.WhisperBackend
	}
	opts := ModelOptions(modelConfig, appConfig)
	transcriptionModel, err := ml.Load(opts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return nil, err
	}
	if transcriptionModel == nil {
		return nil, fmt.Errorf("could not load transcription model")
	}
	return transcriptionModel, nil
}

func ModelTranscription(audio, language string, translate, diarize bool, prompt string, ml *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (*schema.TranscriptionResult, error) {
	return ModelTranscriptionWithOptions(TranscriptionRequest{
		Audio:     audio,
		Language:  language,
		Translate: translate,
		Diarize:   diarize,
		Prompt:    prompt,
	}, ml, modelConfig, appConfig)
}

func ModelTranscriptionWithOptions(req TranscriptionRequest, ml *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (*schema.TranscriptionResult, error) {
	transcriptionModel, err := loadTranscriptionModel(ml, modelConfig, appConfig)
	if err != nil {
		return nil, err
	}

	var startTime time.Time
	var audioSnippet map[string]any
	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems)
		startTime = time.Now()
		// Capture audio before the backend call — the backend may delete the file.
		audioSnippet = trace.AudioSnippet(req.Audio)
	}

	r, err := transcriptionModel.AudioTranscription(context.Background(), req.toProto(uint32(*modelConfig.Threads)))
	if err != nil {
		if appConfig.EnableTracing {
			errData := map[string]any{
				"audio_file": req.Audio,
				"language":   req.Language,
				"translate":  req.Translate,
				"diarize":    req.Diarize,
				"prompt":     req.Prompt,
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
				Summary:   trace.TruncateString(req.Audio, 200),
				Error:     err.Error(),
				Data:      errData,
			})
		}
		return nil, err
	}
	tr := transcriptResultFromProto(r)

	if appConfig.EnableTracing {
		data := map[string]any{
			"audio_file":     req.Audio,
			"language":       req.Language,
			"translate":      req.Translate,
			"diarize":        req.Diarize,
			"prompt":         req.Prompt,
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
			Summary:   trace.TruncateString(req.Audio+" -> "+tr.Text, 200),
			Data:      data,
		})
	}

	return tr, err
}

// TranscriptionStreamChunk is a streaming event emitted by
// ModelTranscriptionStream. Either Delta carries an incremental text fragment,
// or Final carries the completed transcription as the very last event.
type TranscriptionStreamChunk struct {
	Delta string
	Final *schema.TranscriptionResult
}

// ModelTranscriptionStream runs the gRPC streaming transcription RPC and
// invokes onChunk for each event the backend produces. Backends that don't
// support real streaming should still emit one terminal event with Final set,
// which the HTTP layer turns into a single delta + done SSE pair.
func ModelTranscriptionStream(req TranscriptionRequest, ml *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig, onChunk func(TranscriptionStreamChunk)) error {
	transcriptionModel, err := loadTranscriptionModel(ml, modelConfig, appConfig)
	if err != nil {
		return err
	}

	pbReq := req.toProto(uint32(*modelConfig.Threads))
	pbReq.Stream = true

	return transcriptionModel.AudioTranscriptionStream(context.Background(), pbReq, func(chunk *proto.TranscriptStreamResponse) {
		if chunk == nil {
			return
		}
		out := TranscriptionStreamChunk{Delta: chunk.Delta}
		if chunk.FinalResult != nil {
			out.Final = transcriptResultFromProto(chunk.FinalResult)
		}
		onChunk(out)
	})
}

func transcriptResultFromProto(r *proto.TranscriptResult) *schema.TranscriptionResult {
	if r == nil {
		return &schema.TranscriptionResult{}
	}
	tr := &schema.TranscriptionResult{
		Text:     r.Text,
		Language: r.Language,
		Duration: float64(r.Duration),
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
	return tr
}
