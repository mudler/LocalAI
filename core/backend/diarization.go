package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/voicerecognition"

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
	Audio                  string
	Language               string
	NumSpeakers            int32
	MinSpeakers            int32
	MaxSpeakers            int32
	ClusteringThreshold    float32
	MinDurationOn          float32
	MinDurationOff         float32
	IncludeText            bool
	IncludeSpeakerProfiles bool
	// KnownVoices are registered voices a speaker-identifying backend may use
	// to name the speakers. Empty for every other backend and model.
	KnownVoices []voicerecognition.KnownVoice
}

// modelIdentity: see the note on TranscriptionRequest.toProto.
func (r *DiarizationRequest) toProto(threads uint32, modelIdentity string) *proto.DiarizeRequest {
	known := make([]*proto.KnownVoice, 0, len(r.KnownVoices))
	for _, v := range r.KnownVoices {
		known = append(known, &proto.KnownVoice{Id: v.ID, Name: v.Name, Embedding: v.Embedding, Model: v.Model, EncoderFamily: v.Family, EncoderWeights: v.Weights})
	}
	return &proto.DiarizeRequest{
		ModelIdentity:          modelIdentity,
		Dst:                    r.Audio,
		Threads:                threads,
		Language:               r.Language,
		NumSpeakers:            r.NumSpeakers,
		MinSpeakers:            r.MinSpeakers,
		MaxSpeakers:            r.MaxSpeakers,
		ClusteringThreshold:    r.ClusteringThreshold,
		MinDurationOn:          r.MinDurationOn,
		MinDurationOff:         r.MinDurationOff,
		IncludeText:            r.IncludeText,
		IncludeSpeakerProfiles: r.IncludeSpeakerProfiles,
		KnownVoices:            known,
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
func ModelDiarization(ctx context.Context, req DiarizationRequest, ml *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (*schema.DiarizationResult, error) {
	m, err := loadDiarizationModel(ml, modelConfig, appConfig)
	if err != nil {
		return nil, err
	}

	threads := uint32(0)
	if modelConfig.Threads != nil {
		threads = uint32(*modelConfig.Threads)
	}

	req.KnownVoices = compatiblePortableVoices(ctx, m, req.KnownVoices)
	r, err := m.Diarize(ctx, req.toProto(threads, modelConfig.Model))
	if err != nil {
		return nil, err
	}
	out := diarizationResultFromProto(r)
	if req.IncludeSpeakerProfiles {
		trusted, err := speakerEncoderFromBackend(ctx, m)
		if err != nil {
			return nil, err
		}
		profiles, err := decodeSpeakerProfiles(r.GetSpeakerProfilesJson(), trusted)
		if err != nil {
			return nil, err
		}
		out.SpeakerProfiles = profiles
	}
	return out, nil
}

// diarizationResultFromProto normalizes backend speaker labels to
// "SPEAKER_NN" — the convention pyannote/RTTM tooling expects — while
// keeping the original label available via the Label field. Each
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
		name     string
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
		if st.name == "" {
			st.name = s.Name
		}

		out.Segments = append(out.Segments, schema.DiarizationSegment{
			Id:        i,
			Speaker:   fmt.Sprintf("SPEAKER_%02d", st.idx),
			Label:     raw,
			Start:     float64(s.Start),
			End:       float64(s.End),
			Text:      s.Text,
			Name:      s.Name,
			NameScore: s.NameScore,
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
			Name:                st.name,
			TotalSpeechDuration: st.duration,
			SegmentCount:        st.segments,
		})
	}
	sort.SliceStable(out.Speakers, func(i, j int) bool {
		return out.Speakers[i].Id < out.Speakers[j].Id
	})

	return out
}

// ModelSpeakerEncoder obtains trusted metadata from the configured loaded model.
// HTTP enrollment must use this, never metadata supplied by the caller.
func ModelSpeakerEncoder(ctx context.Context, ml *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (schema.SpeakerEncoder, error) {
	m, err := loadDiarizationModel(ml, modelConfig, appConfig)
	if err != nil {
		return schema.SpeakerEncoder{}, err
	}
	return speakerEncoderFromBackend(ctx, m)
}
func speakerEncoderFromBackend(ctx context.Context, m grpcPkg.Backend) (schema.SpeakerEncoder, error) {
	r, err := m.Status(ctx)
	if err != nil {
		return schema.SpeakerEncoder{}, err
	}
	e := r.GetSpeakerEncoder()
	trusted := schema.SpeakerEncoder{Identity: e.GetIdentity(), Dimension: int(e.GetDimension()), Family: e.GetFamily()}
	if err := (schema.SpeakerProfiles{Version: 1, Encoder: trusted}).Validate(trusted); err != nil {
		return schema.SpeakerEncoder{}, status.Error(codes.Unimplemented, "backend does not expose trusted speaker encoder metadata")
	}
	return trusted, nil
}
func decodeSpeakerProfiles(raw string, trusted schema.SpeakerEncoder) (*schema.SpeakerProfiles, error) {
	if raw == "" {
		return nil, status.Error(codes.Unimplemented, "backend does not support speaker profiles")
	}
	var profiles schema.SpeakerProfiles
	if err := json.Unmarshal([]byte(raw), &profiles); err != nil {
		return nil, fmt.Errorf("decode speaker profiles: %w", err)
	}
	if err := profiles.Validate(trusted); err != nil {
		return nil, err
	}
	return &profiles, nil
}

// Portable registrations require exact loaded identity and dimension. Legacy
// candidates use the trusted dimension when available; older backends without
// metadata retain their native dimension check. A portable voice with other
// weights is dropped here unless it carries an encoder family: the backend
// compares families, accepts another quantization of the same encoder with a
// warning, and refuses another encoder by name.
func compatiblePortableVoices(ctx context.Context, m grpcPkg.Backend, voices []voicerecognition.KnownVoice) []voicerecognition.KnownVoice {
	if len(voices) == 0 {
		return voices
	}
	trusted, err := speakerEncoderFromBackend(ctx, m)
	out := make([]voicerecognition.KnownVoice, 0, len(voices))
	for _, v := range voices {
		if err == nil && len(v.Embedding) != trusted.Dimension {
			continue
		}
		if strings.HasPrefix(v.Model, "sha256:") && (err != nil || len(v.Embedding) != trusted.Dimension ||
			(v.Model != trusted.Identity && v.Family == "")) {
			continue
		}
		out = append(out, v)
	}
	return out
}
