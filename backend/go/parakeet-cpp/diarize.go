package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"

	gguf "github.com/gpustack/gguf-parser-go"
	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Speaker diarization (nvidia/Nemotron-3-Diarization and compatible Sortformer
// models, parakeet.cpp ABI >= 7).
//
// A parakeet_ctx holds either an ASR model or a diarization model;
// parakeet_capi_load detects which from the GGUF. The backend serves
// diarization in two shapes:
//
//   - the model file IS a diarization GGUF: the Diarize RPC works, transcription
//     does not (there is no ASR to run);
//   - an ASR model with a diarization GGUF attached through the diar_model
//     option (the same key nemo-speech-cpp uses): transcripts carry a speaker
//     per segment, and Diarize can fill each segment's text (include_text).
//
// Diarization runs the checkpoint's own mode, NeMo's diarize() default, which
// for Nemotron-3-Diarization is cache-aware streaming in 21.12 s chunks.
// Speakers are 0-based and numbered in order of first appearance; the model's
// speaker capacity (8) is fixed by the checkpoint.

// CppDiarizePcm is parakeet_capi_diarize_pcm: 16 kHz mono float PCM in, a
// malloc'd JSON document out (uintptr, freed via CppFreeString):
//
//	{"speakers":8,"segments":[{"speaker":0,"start":0.50,"end":5.52}, ...]}
//
// Present only in libparakeet.so with ABI >= 7; nil disables diarization.
var CppDiarizePcm func(ctx uintptr, samples []float32, nSamples int32, sampleRate int32) uintptr

// Streaming diarization (parakeet_capi_diarize_stream_*), used by live
// transcription. begin_latency takes a PARAKEET_DIAR_LATENCY_* mode; feed and
// active return a malloc'd parakeet_diar_segment array through out/nOut (free
// with CppFreeDiarSegments) and 0 on success. Present only in newer
// libparakeet.so; nil disables speaker labels on live transcripts.
var (
	CppDiarizeStreamBeginLatency func(ctx uintptr, latency int32) uintptr
	CppDiarizeStreamFeed         func(s uintptr, pcm []float32, nSamples int32, isLast int32, out unsafe.Pointer, nOut unsafe.Pointer) int32
	CppDiarizeStreamActive       func(s uintptr, out unsafe.Pointer, nOut unsafe.Pointer) int32
	CppDiarizeStreamFree         func(s uintptr)
	CppFreeDiarSegments          func(segs uintptr)
)

// cDiarSegment mirrors parakeet_diar_segment {int speaker; float start, end}.
type cDiarSegment struct {
	Speaker int32
	Start   float32
	End     float32
}

// Latency modes of parakeet_capi_diarize_stream_begin_latency
// (PARAKEET_DIAR_LATENCY_*), by diar_latency option value.
var diarLatencyModes = map[string]int32{
	"model":     0, // the checkpoint's configuration (21.12 s for Nemotron-3)
	"low":       1, // 1.04 s
	"very_low":  2, // 0.64 s
	"ultra_low": 3, // 0.32 s
}

// defaultLiveDiarLatency is used for live transcription when diar_latency is
// unset: the model card's recommended low-latency configuration.
const defaultLiveDiarLatency = "low"

// liveDiarizationAvailable reports whether the library can diarize live.
func liveDiarizationAvailable() bool {
	return CppDiarizeStreamBeginLatency != nil && CppDiarizeStreamFeed != nil &&
		CppDiarizeStreamActive != nil && CppDiarizeStreamFree != nil && CppFreeDiarSegments != nil
}

// takeDiarSegments copies a C parakeet_diar_segment array and frees it.
func takeDiarSegments(ptr uintptr, n int32) []diarSegmentDoc {
	if ptr == 0 || n <= 0 {
		return nil
	}
	// C-owned malloc'd array, not Go-GC memory; copied out before freeing.
	cs := unsafe.Slice((*cDiarSegment)(unsafe.Pointer(ptr)), int(n)) //nolint:govet // see goStringFromCPtr
	out := make([]diarSegmentDoc, len(cs))
	for i, c := range cs {
		out[i] = diarSegmentDoc{Speaker: int(c.Speaker), Start: float64(c.Start), End: float64(c.End)}
	}
	CppFreeDiarSegments(ptr)
	return out
}

// liveDiarizer runs a streaming diarization session next to a live ASR
// stream. It keeps every closed segment (a live session is bounded by its
// caller, and segments are small) so the final transcript can be relabelled
// with complete information.
type liveDiarizer struct {
	p      *ParakeetCpp
	stream uintptr
	closed []diarSegmentDoc
	active []diarSegmentDoc
}

func (p *ParakeetCpp) newLiveDiarizer(latency int32) (*liveDiarizer, error) {
	p.diarMu.Lock()
	defer p.diarMu.Unlock()
	if p.diarCtx == 0 {
		return nil, grpcerrors.ModelNotLoaded("parakeet-cpp")
	}
	s := CppDiarizeStreamBeginLatency(p.diarCtx, latency)
	if s == 0 {
		return nil, fmt.Errorf("parakeet-cpp: diarize stream begin failed: %s", CppLastError(p.diarCtx))
	}
	return &liveDiarizer{p: p, stream: s}, nil
}

// feed pushes PCM (and, with last, flushes the stream), recording the segments
// that closed and those still open.
func (d *liveDiarizer) feed(pcm []float32, last bool) error {
	d.p.diarMu.Lock()
	defer d.p.diarMu.Unlock()
	if d.p.diarCtx == 0 {
		return grpcerrors.ModelNotLoaded("parakeet-cpp")
	}
	var ptr uintptr
	var n int32
	isLast := int32(0)
	if last {
		isLast = 1
	}
	if CppDiarizeStreamFeed(d.stream, pcm, int32(len(pcm)), isLast, unsafe.Pointer(&ptr), unsafe.Pointer(&n)) != 0 {
		return fmt.Errorf("parakeet-cpp: diarize stream feed failed: %s", CppLastError(d.p.diarCtx))
	}
	d.closed = append(d.closed, takeDiarSegments(ptr, n)...)
	ptr, n = 0, 0
	if CppDiarizeStreamActive(d.stream, unsafe.Pointer(&ptr), unsafe.Pointer(&n)) != 0 {
		return fmt.Errorf("parakeet-cpp: diarize stream active failed: %s", CppLastError(d.p.diarCtx))
	}
	d.active = takeDiarSegments(ptr, n)
	return nil
}

// speakersFor labels words with what is known so far. Diarization trails the
// audio by its latency, so an open segment is assumed to go on through the
// words it has not reached yet; a word is only mislabelled right at a turn
// that diarization has not seen.
func (d *liveDiarizer) speakersFor(words []transcriptWord) []int {
	segs := append([]diarSegmentDoc(nil), d.closed...)
	var lastEnd float64
	for _, w := range words {
		lastEnd = max(lastEnd, w.End)
	}
	for _, a := range d.active {
		a.End = max(a.End, lastEnd)
		segs = append(segs, a)
	}
	return assignSpeakers(words, segs)
}

func (d *liveDiarizer) free() {
	if d == nil || d.stream == 0 {
		return
	}
	d.p.diarMu.Lock()
	defer d.p.diarMu.Unlock()
	CppDiarizeStreamFree(d.stream)
	d.stream = 0
}

// speakerTurns groups labelled words into one segment per speaker turn.
func speakerTurns(words []transcriptWord, speakers []int) []*pb.TranscriptSegment {
	var out []*pb.TranscriptSegment
	start := 0
	for i := 1; i <= len(words); i++ {
		if i < len(words) && speakers[i] == speakers[start] {
			continue
		}
		parts := make([]string, 0, i-start)
		for _, w := range words[start:i] {
			parts = append(parts, w.W)
		}
		out = append(out, &pb.TranscriptSegment{
			Id:      int32(len(out)),
			Start:   secondsToNanos(words[start].Start),
			End:     secondsToNanos(words[i-1].End),
			Text:    strings.TrimSpace(strings.Join(parts, " ")),
			Speaker: speakerLabel(speakers[start]),
		})
		start = i
	}
	return out
}

// majoritySpeaker is the speaker covering most of the words' duration, or -1.
func majoritySpeaker(words []transcriptWord, speakers []int) int {
	dur := map[int]float64{}
	best, bestDur := -1, 0.0
	for i, w := range words {
		if speakers[i] < 0 {
			continue
		}
		dur[speakers[i]] += max(w.End-w.Start, 1e-3)
		if dur[speakers[i]] > bestDur {
			best, bestDur = speakers[i], dur[speakers[i]]
		}
	}
	return best
}

// diarModelArch is the parakeet.arch GGUF value of a diarization model.
const diarModelArch = "diarization"

// speakerSnapSeconds mirrors parakeet.cpp's merge_asr_diarization: a word that
// overlaps no speaker segment takes the nearest segment's speaker when that
// segment is this close. ASR word boundaries and diarization boundaries can
// disagree by a frame or two; a word farther than this has no speaker.
const speakerSnapSeconds = 0.5

type diarJSON struct {
	Speakers int              `json:"speakers"`
	Segments []diarSegmentDoc `json:"segments"`
}

type diarSegmentDoc struct {
	Speaker int     `json:"speaker"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
}

// parakeetArch reads parakeet.arch ("tdt", "ctc", ..., "diarization") from a
// parakeet.cpp GGUF. general.architecture is "parakeet" for every model, so it
// cannot tell ASR from diarization.
func parakeetArch(path string) (string, error) {
	f, err := gguf.ParseGGUFFile(path, gguf.UseMMap(), gguf.SkipLargeMetadata())
	if err != nil {
		return "", fmt.Errorf("parakeet-cpp: parse gguf %q: %w", path, err)
	}
	kv, found := f.Header.MetadataKV.Index([]string{"parakeet.arch"})
	if found == 0 {
		return "", fmt.Errorf("parakeet-cpp: %q has no parakeet.arch key", path)
	}
	arch := kv["parakeet.arch"]
	// ValueString panics on a mistyped key; a hand-edited GGUF is exactly where
	// that happens, and a load-time check must report, not crash.
	if arch.ValueType != gguf.GGUFMetadataValueTypeString {
		return "", fmt.Errorf("parakeet-cpp: %q has a non-string parakeet.arch", path)
	}
	return arch.ValueString(), nil
}

// optString reads a string model option (key:value form), splitting on the
// first colon so values may contain one. Returns "" when absent.
func optString(opts *pb.ModelOptions, key string) string {
	for _, o := range opts.GetOptions() {
		k, v, ok := strings.Cut(o, ":")
		if ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// resolveModelPath makes a relative option path absolute against the models
// directory, as nemo-speech-cpp does for its auxiliary models.
func resolveModelPath(base, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}

// loadDiarModel attaches the diar_model option's GGUF to an ASR model.
func (p *ParakeetCpp) loadDiarModel(path string) error {
	if CppDiarizePcm == nil {
		return fmt.Errorf("parakeet-cpp: diar_model is set but libparakeet.so has no " +
			"diarization C-API (needs parakeet.cpp ABI >= 7)")
	}
	arch, err := parakeetArch(path)
	if err != nil {
		return err
	}
	if arch != diarModelArch {
		return fmt.Errorf("parakeet-cpp: diar_model %q is a %q model, not a diarization model", path, arch)
	}
	ctx := CppLoad(path)
	if ctx == 0 {
		return fmt.Errorf("parakeet-cpp: parakeet_capi_load failed for diar_model %q", path)
	}
	p.diarCtx = ctx
	xlog.Info("parakeet-cpp: speaker diarization attached", "diar_model", path)
	return nil
}

// diarizerCtx is the context that diarizes: the model itself when it is a
// diarization model, else the attached diar_model, else 0.
func (p *ParakeetCpp) diarizerCtx() uintptr {
	if p.isDiarModel {
		return p.ctxPtr
	}
	return p.diarCtx
}

// runDiarization diarizes 16 kHz mono PCM. diarMu serializes calls on the
// diarization context, which is separate from the ASR engine (engineMu).
func (p *ParakeetCpp) runDiarization(pcm []float32) (diarJSON, error) {
	p.diarMu.Lock()
	defer p.diarMu.Unlock()
	ctx := p.diarizerCtx()
	if ctx == 0 {
		return diarJSON{}, grpcerrors.ModelNotLoaded("parakeet-cpp")
	}
	if len(pcm) == 0 {
		return diarJSON{}, nil
	}
	cstr := CppDiarizePcm(ctx, pcm, int32(len(pcm)), 16000)
	if cstr == 0 {
		return diarJSON{}, fmt.Errorf("parakeet-cpp: diarize failed: %s", CppLastError(ctx))
	}
	raw := goStringFromCPtr(cstr)
	CppFreeString(cstr)
	var doc diarJSON
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return diarJSON{}, fmt.Errorf("parakeet-cpp: decode diarization json: %w", err)
	}
	return doc, nil
}

// assignSpeakers gives each word the speaker whose segments overlap it most,
// falling back to the nearest segment within speakerSnapSeconds; -1 when none.
// Same rule as parakeet.cpp's merge_asr_diarization, so the backend's speaker
// tags match the library's own speaker-attributed ASR.
func assignSpeakers(words []transcriptWord, segs []diarSegmentDoc) []int {
	out := make([]int, len(words))
	for i, w := range words {
		best, bestOverlap := -1, 0.0
		for _, s := range segs {
			if ov := min(w.End, s.End) - max(w.Start, s.Start); ov > bestOverlap {
				best, bestOverlap = s.Speaker, ov
			}
		}
		if best < 0 {
			bestDist := speakerSnapSeconds
			for _, s := range segs {
				dist := s.Start - w.End
				if s.End <= w.Start {
					dist = w.Start - s.End
				}
				if dist >= 0 && dist <= bestDist {
					best, bestDist = s.Speaker, dist
				}
			}
		}
		out[i] = best
	}
	return out
}

// speakerLabel is the TranscriptSegment/DiarizeSegment speaker string: the
// 0-based index, or "" for no speaker.
func speakerLabel(spk int) string {
	if spk < 0 {
		return ""
	}
	return strconv.Itoa(spk)
}

// splitAtSpeakerChanges splits each word group wherever the speaker changes, so
// every segment has exactly one speaker. speakers is indexed like the
// concatenation of groups. Returns the new groups and each group's speaker.
func splitAtSpeakerChanges(groups [][]transcriptWord, speakers []int) ([][]transcriptWord, []int) {
	var outGroups [][]transcriptWord
	var outSpk []int
	k := 0
	for _, g := range groups {
		start := 0
		for i := 1; i <= len(g); i++ {
			if i == len(g) || speakers[k+i] != speakers[k+start] {
				outGroups = append(outGroups, g[start:i])
				outSpk = append(outSpk, speakers[k+start])
				start = i
			}
		}
		k += len(g)
	}
	return outGroups, outSpk
}

// diarizeSegments maps diarization segments (and, when words are given, the
// transcript words attributed to each speaker) to DiarizeSegments, and counts
// the distinct speakers. A word belongs to the segment of its assigned speaker
// that contains its midpoint.
func diarizeSegments(doc diarJSON, words []transcriptWord) ([]*pb.DiarizeSegment, int32) {
	var speakers []int
	if len(words) > 0 {
		speakers = assignSpeakers(words, doc.Segments)
	}
	var out []*pb.DiarizeSegment
	seen := map[int]bool{}
	for i, s := range doc.Segments {
		seg := &pb.DiarizeSegment{
			Id:      int32(i),
			Start:   float32(s.Start),
			End:     float32(s.End),
			Speaker: speakerLabel(s.Speaker),
		}
		if len(words) > 0 {
			var parts []string
			for j, w := range words {
				mid := (w.Start + w.End) / 2
				if speakers[j] == s.Speaker && mid >= s.Start && mid < s.End {
					parts = append(parts, w.W)
				}
			}
			seg.Text = strings.Join(parts, " ")
		}
		out = append(out, seg)
		seen[s.Speaker] = true
	}
	return out, int32(len(seen))
}

// unsupportedDiarizeFields names the DiarizeRequest fields Sortformer cannot
// honour, so they are logged rather than silently dropped. The model is
// end-to-end: there is no speaker-count input and no clustering stage, and the
// segment thresholds come from the checkpoint.
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
	if req.GetMinDurationOn() != 0 {
		out = append(out, "min_duration_on")
	}
	if req.GetMinDurationOff() != 0 {
		out = append(out, "min_duration_off")
	}
	return out
}

// Diarize answers "who spoke when" for the audio at req.Dst. With
// include_text, and an ASR model loaded alongside the diar_model, each segment
// also carries the words its speaker said in it.
func (p *ParakeetCpp) Diarize(req *pb.DiarizeRequest) (pb.DiarizeResponse, error) {
	if p.ctxPtr == 0 {
		return pb.DiarizeResponse{}, grpcerrors.ModelNotLoaded("parakeet-cpp")
	}
	if p.diarizerCtx() == 0 || CppDiarizePcm == nil {
		return pb.DiarizeResponse{}, status.Error(codes.Unimplemented,
			"parakeet-cpp: this model has no diarization; load a diarization GGUF "+
				"or attach one to an ASR model with the diar_model option")
	}
	if req.GetDst() == "" {
		return pb.DiarizeResponse{}, status.Error(codes.InvalidArgument,
			"parakeet-cpp: DiarizeRequest.dst (audio path) is required")
	}
	if f := unsupportedDiarizeFields(req); len(f) > 0 {
		xlog.Warn("parakeet-cpp: ignoring diarization options Sortformer has no equivalent for",
			"fields", strings.Join(f, ","))
	}

	pcm, duration, err := decodeWavMono16k(req.GetDst())
	if err != nil {
		return pb.DiarizeResponse{}, err
	}
	doc, err := p.runDiarization(pcm)
	if err != nil {
		return pb.DiarizeResponse{}, err
	}

	var words []transcriptWord
	if req.GetIncludeText() {
		if p.isDiarModel {
			xlog.Warn("parakeet-cpp: include_text needs an ASR model with diar_model attached; " +
				"returning segments without text")
		} else {
			tr, err := p.transcribeDoc(pcm, req.GetLanguage())
			if err != nil {
				return pb.DiarizeResponse{}, err
			}
			words = tr.Words
		}
	}
	segs, n := diarizeSegments(doc, words)
	return pb.DiarizeResponse{
		Segments:    segs,
		NumSpeakers: n,
		Duration:    duration,
		Language:    req.GetLanguage(),
	}, nil
}
