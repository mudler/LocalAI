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

// DiarizationRequest carries the diarization-specific knobs the HTTP
// layer collects. Speaker hints (NumSpeakers / MinSpeakers / MaxSpeakers)
// and clustering knobs are optional — backends ignore the ones they
// don't act on. IncludeText only matters for backends that emit
// per-segment transcripts as a by-product (e.g. vibevoice.cpp).
type DiarizationRequest struct {
	Audio                string
	Language             string
	NumSpeakers          int32
	MinSpeakers          int32
	MaxSpeakers          int32
	ClusteringThreshold  float32
	MinDurationOn        float32
	MinDurationOff       float32
	IncludeText          bool
}

func (r *DiarizationRequest) toProto(threads uint32) *proto.DiarizeRequest {
	return &proto.DiarizeRequest{
		Dst:                 r.Audio,
		Threads:             threads,
		Language:            r.Language,
		NumSpeakers:         r.NumSpeakers,
		MinSpeakers:         r.MinSpeakers,
		MaxSpeakers:         r.MaxSpeakers,
		ClusteringThreshold: r.ClusteringThreshold,
		MinDurationOn:       r.MinDurationOn,
		MinDurationOff:      r.MinDurationOff,
		IncludeText:         r.IncludeText,
	}
}

func loadDiarizationModel(ml *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (grpcPkg.Backend, error) {
	if modelConfig.Backend == "" {
		return nil, fmt.Errorf("diarization: model %q has no backend set; supported backends include vibevoice-cpp and sherpa-onnx", modelConfig.Name)
	}
	opts := ModelOptions(modelConfig, appConfig)
	m, err := ml.Load(opts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("could not load diarization model")
	}
	return m, nil
}

// ModelDiarization runs the Diarize RPC against the configured backend
// and returns a normalized schema.DiarizationResult.
func ModelDiarization(req DiarizationRequest, ml *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (*schema.DiarizationResult, error) {
	m, err := loadDiarizationModel(ml, modelConfig, appConfig)
	if err != nil {
		return nil, err
	}

	threads := uint32(0)
	if modelConfig.Threads != nil {
		threads = uint32(*modelConfig.Threads)
	}

	r, err := m.Diarize(context.Background(), req.toProto(threads))
	if err != nil {
		return nil, err
	}
	return diarizationResultFromProto(r), nil
}

// diarizationResultFromProto normalizes backend speaker labels to
// "SPEAKER_NN" — the convention pyannote/RTTM tooling expects — while
// keeping the original label available via the Speaker field. Each
// distinct backend label gets its own normalized id, in first-seen order.
func diarizationResultFromProto(r *proto.DiarizeResponse) *schema.DiarizationResult {
	if r == nil {
		return &schema.DiarizationResult{Segments: []schema.DiarizationSegment{}}
	}

	out := &schema.DiarizationResult{
		Task:     "diarize",
		Duration: float64(r.Duration),
		Language: r.Language,
		Segments: make([]schema.DiarizationSegment, 0, len(r.Segments)),
	}

	type speakerStats struct {
		idx      int
		duration float64
		segments int
	}
	stats := map[string]*speakerStats{}
	order := []string{}

	for i, s := range r.Segments {
		if s == nil {
			continue
		}
		raw := s.Speaker
		if raw == "" {
			raw = "0"
		}
		st, ok := stats[raw]
		if !ok {
			st = &speakerStats{idx: len(order)}
			stats[raw] = st
			order = append(order, raw)
		}
		dur := float64(s.End) - float64(s.Start)
		if dur > 0 {
			st.duration += dur
		}
		st.segments++

		out.Segments = append(out.Segments, schema.DiarizationSegment{
			Id:      i,
			Speaker: fmt.Sprintf("SPEAKER_%02d", st.idx),
			Label:   raw,
			Start:   float64(s.Start),
			End:     float64(s.End),
			Text:    s.Text,
		})
	}

	out.NumSpeakers = len(order)
	if out.NumSpeakers == 0 && r.NumSpeakers > 0 {
		out.NumSpeakers = int(r.NumSpeakers)
	}

	out.Speakers = make([]schema.DiarizationSpeaker, 0, len(order))
	for _, raw := range order {
		st := stats[raw]
		out.Speakers = append(out.Speakers, schema.DiarizationSpeaker{
			Id:                  fmt.Sprintf("SPEAKER_%02d", st.idx),
			Label:               raw,
			TotalSpeechDuration: st.duration,
			SegmentCount:        st.segments,
		})
	}
	sort.SliceStable(out.Speakers, func(i, j int) bool {
		return out.Speakers[i].Id < out.Speakers[j].Id
	})

	return out
}
