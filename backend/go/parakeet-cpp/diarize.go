package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
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
	Segments []diarizeSegmentJSON       `json:"segments"`
	Names    map[string]speakerNameJSON `json:"names"`
}

// speakerNameJSON mirrors one value of the "names" map the named C-API functions add:
// {"0":{"name":"Ada","score":0.93}}.
type speakerNameJSON struct {
	Name  string  `json:"name"`
	Score float32 `json:"score"`
}

// nameFor returns the registered name of a diarization slot, or "" for an unknown slot, a slot
// with no matching voice, or speaker -1 (no diarized speaker).
func nameFor(names map[string]speakerNameJSON, speaker int) (string, float32) {
	if speaker < 0 || len(names) == 0 {
		return "", 0
	}
	n, ok := names[strconv.Itoa(speaker)]
	if !ok || n.Name == "" {
		return "", 0
	}
	return n.Name, n.Score
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
	Utterances []diarizeUtteranceJSON     `json:"utterances"`
	Names      map[string]speakerNameJSON `json:"names"`
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
		return pb.DiarizeResponse{}, status.Errorf(codes.InvalidArgument, "parakeet-cpp: decode audio: %s", err)
	}
	if len(pcm) == 0 {
		return pb.DiarizeResponse{}, status.Error(codes.InvalidArgument, "parakeet-cpp: empty audio")
	}

	wantText := req.GetIncludeText() && p.ctxPtr != 0 && CppTranscribeAndDiarizeJSON != nil

	var reg uintptr
	if len(req.GetKnownVoices()) > 0 && p.spkCtx != 0 {
		reg, err = p.buildSpeakerRegistry(req.GetKnownVoices())
		if err != nil {
			return pb.DiarizeResponse{}, err
		}
		defer p.freeSpeakerRegistry(reg)
	}

	raw, err := p.diarizeCall(pcm, wantText, reg)
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
// document. p.diarCtx (and, on the include_text path, p.ctxPtr) is re-checked
// under the lock before the C call: Diarize's own p.diarCtx==0/wantText checks
// run before this lock is taken, so a Free() racing in between (which zeroes
// those fields under the same engineMu) would otherwise reach the C side with
// a freed context. last_error is ctx-shared, so it is read under the same
// lock as the failing call.
func (p *ParakeetCpp) diarizeCall(pcm []float32, wantText bool, reg uintptr) (string, error) {
	p.engineMu.Lock()
	defer p.engineMu.Unlock()

	if p.diarCtx == 0 || (wantText && p.ctxPtr == 0) || (reg != 0 && p.spkCtx == 0) {
		return "", grpcerrors.ModelNotLoaded("parakeet-cpp")
	}

	var cstr uintptr
	switch {
	case reg != 0 && wantText:
		if CppTranscribeAndDiarizeNamedJSON == nil {
			return "", status.Error(codes.Unimplemented,
				"parakeet-cpp: naming speakers needs libparakeet.so ABI 10 (parakeet_capi_transcribe_and_diarize_named_json)")
		}
		// This C function takes no threshold or margin, so the text path uses the C side's
		// defaults rather than speaker_threshold / speaker_margin.
		cstr = CppTranscribeAndDiarizeNamedJSON(p.ctxPtr, p.diarCtx, p.spkCtx, reg, &pcm[0], int32(len(pcm)), 16000)
	case reg != 0:
		if CppDiarizeNamedPCMJSON == nil {
			return "", status.Error(codes.Unimplemented,
				"parakeet-cpp: naming speakers needs libparakeet.so ABI 10 (parakeet_capi_diarize_named_pcm_json)")
		}
		cstr = CppDiarizeNamedPCMJSON(p.diarCtx, p.spkCtx, reg, &pcm[0], int32(len(pcm)), 16000, p.speakerAccept, p.speakerMargin)
	case wantText:
		cstr = CppTranscribeAndDiarizeJSON(p.ctxPtr, p.diarCtx, &pcm[0], int32(len(pcm)), 16000)
	default:
		cstr = CppDiarizePCM(p.diarCtx, &pcm[0], int32(len(pcm)), 16000)
	}
	if cstr == 0 {
		return "", fmt.Errorf("parakeet-cpp: diarize failed: %s", diarizeLastError(p, wantText, reg != 0))
	}
	raw := goStringFromCPtr(cstr)
	CppFreeString(cstr)
	return raw, nil
}

// diarizeLastError reads last_error off p.diarCtx and, on the include_text
// path, p.ctxPtr too — the failing call is CppTranscribeAndDiarizeJSON there,
// and either side of the pairing may be the one that set it — then joins
// whichever came back non-empty. Called under the same engineMu as the
// failing call (last_error is ctx-shared state).
func diarizeLastError(p *ParakeetCpp, wantText, named bool) string {
	var msgs []string
	if m := CppLastError(p.diarCtx); m != "" {
		msgs = append(msgs, m)
	}
	if wantText {
		if m := CppLastError(p.ctxPtr); m != "" {
			msgs = append(msgs, m)
		}
	}
	if named {
		if m := CppLastError(p.spkCtx); m != "" {
			msgs = append(msgs, m)
		}
	}
	if len(msgs) == 0 {
		return "unknown error"
	}
	return strings.Join(msgs, "; ")
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
			name, score := nameFor(doc.Names, u.Speaker)
			segs = append(segs, &pb.DiarizeSegment{
				Start:     float32(u.Start),
				End:       float32(u.End),
				Speaker:   speakerLabel(u.Speaker),
				Text:      u.Text,
				Name:      name,
				NameScore: score,
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
		name, score := nameFor(doc.Names, s.Speaker)
		segs = append(segs, &pb.DiarizeSegment{
			Start:     float32(s.Start),
			End:       float32(s.End),
			Speaker:   speakerLabel(s.Speaker),
			Name:      name,
			NameScore: score,
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

// mergeCloseSegments merges SAME-SPEAKER segments separated by a gap shorter
// than minOff into one segment spanning both (and concatenating any text).
// Segments from different speakers are never merged, regardless of gap: the
// gap only ever means "the same speaker paused", never "two speakers are
// actually one".
//
// Merging runs per speaker rather than on the single start-sorted list: two
// segments of the same speaker are not necessarily adjacent in that list once
// another speaker's turn falls between them (A, B, A), and a start-sorted
// walk would then never compare the two A's at all. Grouping by speaker first
// keeps each group's own start order (segs is assumed start-sorted, as
// parakeet_capi_diarize_pcm and parakeet_capi_transcribe_and_diarize_json
// document), merges within the group, then the merged segments are re-sorted
// by start so interleaved speakers come back out in timeline order.
func mergeCloseSegments(segs []*pb.DiarizeSegment, minOff float32) []*pb.DiarizeSegment {
	if minOff <= 0 || len(segs) < 2 {
		return segs
	}

	bySpeaker := make(map[string][]*pb.DiarizeSegment)
	var order []string // first-seen speaker order, for a deterministic group walk
	for _, s := range segs {
		if _, ok := bySpeaker[s.GetSpeaker()]; !ok {
			order = append(order, s.GetSpeaker())
		}
		bySpeaker[s.GetSpeaker()] = append(bySpeaker[s.GetSpeaker()], s)
	}

	out := make([]*pb.DiarizeSegment, 0, len(segs))
	for _, speaker := range order {
		group := bySpeaker[speaker]
		merged := make([]*pb.DiarizeSegment, 0, len(group))
		merged = append(merged, group[0])
		for _, s := range group[1:] {
			prev := merged[len(merged)-1]
			if s.GetStart()-prev.GetEnd() < minOff {
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
			merged = append(merged, s)
		}
		out = append(out, merged...)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].GetStart() < out[j].GetStart() })
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
