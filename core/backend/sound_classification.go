package backend

import (
	"context"
	"fmt"
	"sort"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"

	grpcPkg "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
)

// SoundDetectionRequest carries the knobs the HTTP layer collects for an
// audio-tagging / sound-event-classification call. Audio is the path to the
// uploaded clip on disk; TopK and Threshold are optional (0 = backend default).
type SoundDetectionRequest struct {
	Audio     string
	TopK      int32
	Threshold float32
}

// modelIdentity: see the note on TranscriptionRequest.toProto.
func (r *SoundDetectionRequest) toProto(modelIdentity string) *proto.SoundDetectionRequest {
	return &proto.SoundDetectionRequest{
		ModelIdentity: modelIdentity,
		Src:           r.Audio,
		TopK:          r.TopK,
		Threshold:     r.Threshold,
	}
}

func loadSoundDetectionModel(ml *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (grpcPkg.Backend, error) {
	if modelConfig.Backend == "" {
		return nil, fmt.Errorf("sound classification: model %q has no backend set; supported backends include ced", modelConfig.Name)
	}
	opts := ModelOptions(modelConfig, appConfig)
	m, err := ml.Load(opts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("could not load sound classification model")
	}
	return m, nil
}

// ModelSoundDetection runs the SoundDetection RPC against the configured
// backend and returns a normalized schema.SoundClassificationResult.
func ModelSoundDetection(ctx context.Context, req SoundDetectionRequest, ml *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (*schema.SoundClassificationResult, error) {
	m, err := loadSoundDetectionModel(ml, modelConfig, appConfig)
	if err != nil {
		return nil, err
	}

	r, err := m.SoundDetection(ctx, req.toProto(modelConfig.Model))
	if err != nil {
		return nil, err
	}
	return soundClassificationResultFromProto(modelConfig.Name, r), nil
}

// soundClassificationResultFromProto maps the backend detections to the
// HTTP-facing schema, keeping the backend's score-descending order.
func soundClassificationResultFromProto(modelName string, r *proto.SoundDetectionResponse) *schema.SoundClassificationResult {
	out := &schema.SoundClassificationResult{
		Model:      modelName,
		Detections: []schema.SoundClassification{},
	}
	if r == nil {
		return out
	}
	for _, d := range r.Detections {
		if d == nil {
			continue
		}
		out.Detections = append(out.Detections, schema.SoundClassification{
			Index: int(d.Index),
			Label: d.Label,
			Score: d.Score,
		})
	}
	sort.SliceStable(out.Detections, func(i, j int) bool {
		return out.Detections[i].Score > out.Detections[j].Score
	})
	return out
}
