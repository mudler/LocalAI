package main

import (
	"encoding/json"
	"fmt"
	"strconv"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// diarizeSegmentJSON mirrors one element of parakeet_capi_diarize_pcm's
// "segments" array: {"speaker":0,"start":0.50,"end":5.52}.
type diarizeSegmentJSON struct {
	Speaker int     `json:"speaker"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
}

// diarizePCMDoc mirrors the document parakeet_capi_diarize_pcm returns.
// "speakers" is the model's CAPACITY (e.g. 8 for Nemotron-3-Diarization),
// not the count of speakers actually present, so it is not read here; the
// response's num_speakers is computed from distinct segment labels instead.
type diarizePCMDoc struct {
	Segments []diarizeSegmentJSON `json:"segments"`
}

// diarizeUtteranceJSON mirrors one element of
// parakeet_capi_transcribe_and_diarize_json's "utterances" array. Speaker is
// -1 when no diarized speaker overlaps the utterance.
type diarizeUtteranceJSON struct {
	Speaker int     `json:"speaker"`
	Text    string  `json:"text"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
}

// transcribeAndDiarizeDoc mirrors the document
// parakeet_capi_transcribe_and_diarize_json returns. Only "utterances" is
// consumed here; the per-word "words" detail belongs to a speaker-attributed
// transcript RPC, not Diarize.
type transcribeAndDiarizeDoc struct {
	Utterances []diarizeUtteranceJSON `json:"utterances"`
}

// speakerLabel renders a 0-based speaker index as the decimal string
// DiarizeSegment.speaker documents, or "unknown" for -1 (no diarized speaker
// overlaps this utterance; only transcribe_and_diarize_json can report this).
func speakerLabel(speaker int) string {
	if speaker < 0 {
		return "unknown"
	}
	return strconv.Itoa(speaker)
}

// unsupportedDiarizeFields names the DiarizeRequest fields Sortformer has no
// equivalent for: it is an end-to-end model with a fixed speaker capacity and
// no clustering stage, so there is no config knob to target a speaker count
// or a clustering distance. Logged rather than rejected, so a request naming
// one of these still gets the diarization it can have.
func unsupportedDiarizeFields(req *pb.DiarizeRequest) []string {
	var out []string
	if req.GetNumSpeakers() != 0 {
		out = append(out, "num_speakers")
	}
	if req.GetMinSpeakers() != 0 {
		out = append(out, "min_speakers")
	}
	if req.GetMaxSpeakers() != 0 {
		out = append(out, "max_speakers")
	}
	if req.GetClusteringThreshold() != 0 {
		out = append(out, "clustering_threshold")
	}
	return out
}

// Diarize labels who spoke when in the audio at req.Dst, using the loaded
// diarization model (p.diarCtx). When req.IncludeText is set and an ASR
// companion (p.ctxPtr) is loaded, each segment also carries its transcript
// (parakeet_capi_transcribe_and_diarize_json, one utterance per speaker
// turn); otherwise, or when no ASR companion is loaded, segments carry no
// text (parakeet_capi_diarize_pcm) and no error is raised.
func (p *ParakeetCpp) Diarize(req *pb.DiarizeRequest) (pb.DiarizeResponse, error) {
	if p.diarCtx == 0 {
		return pb.DiarizeResponse{}, status.Error(codes.FailedPrecondition,
			"parakeet-cpp: model is not a diarization model")
	}
	if CppDiarizePCM == nil {
		return pb.DiarizeResponse{}, status.Error(codes.Unimplemented,
			"parakeet-cpp: loaded libparakeet.so has no diarization support (parakeet_capi_diarize_pcm missing)")
	}
	if req.GetDst() == "" {
		return pb.DiarizeResponse{}, status.Error(codes.InvalidArgument,
			"parakeet-cpp: DiarizeRequest.dst (audio path) is required")
	}

	if dropped := unsupportedDiarizeFields(req); len(dropped) > 0 {
		xlog.Debug("parakeet-cpp: ignoring diarization request fields Sortformer has no equivalent for",
			"fields", dropped)
	}

	pcm, duration, err := decodeWavMono16k(req.GetDst())
	if err != nil {
		return pb.DiarizeResponse{}, err
	}
	if len(pcm) == 0 {
		return pb.DiarizeResponse{}, status.Error(codes.InvalidArgument, "parakeet-cpp: empty audio")
	}

	wantText := req.GetIncludeText() && p.ctxPtr != 0 && CppTranscribeAndDiarizeJSON != nil

	raw, err := p.diarizeCall(pcm, wantText)
	if err != nil {
		return pb.DiarizeResponse{}, err
	}
	segments, err := parseDiarizeDoc(raw, wantText)
	if err != nil {
		return pb.DiarizeResponse{}, err
	}

	segments = applyDurationFilters(segments, req.GetMinDurationOn(), req.GetMinDurationOff())
	renumberDiarizeSegments(segments)

	return pb.DiarizeResponse{
		Segments:    segments,
		NumSpeakers: distinctDiarizeSpeakers(segments),
		Duration:    duration,
	}, nil
}

// diarizeCall runs the single C call Diarize needs (transcribe_and_diarize_json
// when wantText, else diarize_pcm) under engineMu, and returns the raw JSON
// document. last_error is ctx-shared, so it is read under the same lock as the
// failing call, off p.diarCtx (the context both entry points share as their
// diarization side).
func (p *ParakeetCpp) diarizeCall(pcm []float32, wantText bool) (string, error) {
	p.engineMu.Lock()
	defer p.engineMu.Unlock()

	var cstr uintptr
	if wantText {
		cstr = CppTranscribeAndDiarizeJSON(p.ctxPtr, p.diarCtx, &pcm[0], int32(len(pcm)), 16000)
	} else {
		cstr = CppDiarizePCM(p.diarCtx, &pcm[0], int32(len(pcm)), 16000)
	}
	if cstr == 0 {
		msg := CppLastError(p.diarCtx)
		if msg == "" {
			msg = "unknown error"
		}
		return "", fmt.Errorf("parakeet-cpp: diarize failed: %s", msg)
	}
	raw := goStringFromCPtr(cstr)
	CppFreeString(cstr)
	return raw, nil
}

// parseDiarizeDoc decodes the raw JSON diarizeCall returned into
// DiarizeSegments (without ids: renumberDiarizeSegments assigns those after
// filtering).
func parseDiarizeDoc(raw string, wantText bool) ([]*pb.DiarizeSegment, error) {
	if wantText {
		var doc transcribeAndDiarizeDoc
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return nil, fmt.Errorf("parakeet-cpp: decode diarize json: %w", err)
		}
		segs := make([]*pb.DiarizeSegment, 0, len(doc.Utterances))
		for _, u := range doc.Utterances {
			segs = append(segs, &pb.DiarizeSegment{
				Start:   float32(u.Start),
				End:     float32(u.End),
				Speaker: speakerLabel(u.Speaker),
				Text:    u.Text,
			})
		}
		return segs, nil
	}

	var doc diarizePCMDoc
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, fmt.Errorf("parakeet-cpp: decode diarize json: %w", err)
	}
	segs := make([]*pb.DiarizeSegment, 0, len(doc.Segments))
	for _, s := range doc.Segments {
		segs = append(segs, &pb.DiarizeSegment{
			Start:   float32(s.Start),
			End:     float32(s.End),
			Speaker: speakerLabel(s.Speaker),
		})
	}
	return segs, nil
}

// applyDurationFilters applies the request's postprocessing knobs, in the
// order NeMo's diarization postprocessing does: merge first
// (min_duration_off), then drop short segments (min_duration_on) — dropping
// first would leave short gaps unmerged that the drop step just created.
// Segments are assumed sorted by start time, as parakeet_capi_diarize_pcm and
// parakeet_capi_transcribe_and_diarize_json document. A non-positive value
// disables that filter (the proto's "0 = backend default" reads here as "no
// filtering").
func applyDurationFilters(segs []*pb.DiarizeSegment, minOn, minOff float32) []*pb.DiarizeSegment {
	segs = mergeCloseSegments(segs, minOff)
	segs = dropShortSegments(segs, minOn)
	return segs
}

// mergeCloseSegments merges consecutive SAME-SPEAKER segments separated by a
// gap shorter than minOff into one segment spanning both (and concatenating
// any text). Segments from different speakers are never merged, regardless of
// gap: the gap only ever means "the same speaker paused", never "two speakers
// are actually one".
func mergeCloseSegments(segs []*pb.DiarizeSegment, minOff float32) []*pb.DiarizeSegment {
	if minOff <= 0 || len(segs) < 2 {
		return segs
	}
	out := make([]*pb.DiarizeSegment, 0, len(segs))
	out = append(out, segs[0])
	for _, s := range segs[1:] {
		prev := out[len(out)-1]
		if s.GetSpeaker() == prev.GetSpeaker() && s.GetStart()-prev.GetEnd() < minOff {
			if s.GetEnd() > prev.GetEnd() {
				prev.End = s.End
			}
			if s.GetText() != "" {
				if prev.GetText() != "" {
					prev.Text = prev.GetText() + " " + s.GetText()
				} else {
					prev.Text = s.GetText()
				}
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

// dropShortSegments discards segments shorter than minOn.
func dropShortSegments(segs []*pb.DiarizeSegment, minOn float32) []*pb.DiarizeSegment {
	if minOn <= 0 {
		return segs
	}
	out := make([]*pb.DiarizeSegment, 0, len(segs))
	for _, s := range segs {
		if s.GetEnd()-s.GetStart() < minOn {
			continue
		}
		out = append(out, s)
	}
	return out
}

// renumberDiarizeSegments assigns sequential ids (0..) to the final segment
// list, after filtering may have dropped or merged entries.
func renumberDiarizeSegments(segs []*pb.DiarizeSegment) {
	for i, s := range segs {
		s.Id = int32(i)
	}
}

// distinctDiarizeSpeakers counts the distinct speaker labels present in segs.
// This is what DiarizeResponse.num_speakers documents — the count of speakers
// actually present in the result — and is NOT the diarize_pcm JSON's
// top-level "speakers" field, which reports the model's fixed capacity.
func distinctDiarizeSpeakers(segs []*pb.DiarizeSegment) int32 {
	seen := make(map[string]struct{}, len(segs))
	for _, s := range segs {
		seen[s.GetSpeaker()] = struct{}{}
	}
	return int32(len(seen))
}
