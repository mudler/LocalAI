package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// purego-bound entry points from libparakeet.so. Names match
// parakeet_capi.h exactly so a `nm libparakeet.so | grep parakeet_capi`
// is enough to spot drift.
//
// Functions that return char* are declared as uintptr so we can call
// parakeet_capi_free_string on the same pointer after copying — the
// C-API contract is "caller owns and must free the returned buffer".
var (
	CppAbiVersion         func() int32
	CppLoad               func(ggufPath string) uintptr
	CppFree               func(ctx uintptr)
	CppTranscribePath     func(ctx uintptr, wavPath string, decoder int32) uintptr
	CppTranscribePathJSON func(ctx uintptr, wavPath string, decoder int32) uintptr
	CppFreeString         func(s uintptr)
	CppLastError          func(ctx uintptr) string
)

// transcriptJSON mirrors the document returned by
// parakeet_capi_transcribe_path_json (see parakeet_capi.h):
//
//	{"text":"...",
//	 "words":[{"w":"...","start":0.480,"end":0.640,"conf":0.9100}, ...],
//	 "tokens":[{"id":123,"t":0.480,"conf":0.9100}, ...]}
//
// "start"/"end"/"t" are seconds; "conf" is confidence in (0,1].
type transcriptJSON struct {
	Text   string            `json:"text"`
	Words  []transcriptWord  `json:"words"`
	Tokens []transcriptToken `json:"tokens"`
}

type transcriptWord struct {
	W     string  `json:"w"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Conf  float64 `json:"conf"`
}

type transcriptToken struct {
	ID   int32   `json:"id"`
	T    float64 `json:"t"`
	Conf float64 `json:"conf"`
}

// ParakeetCpp owns a single loaded parakeet_ctx. The C engine is a
// thread-unsafe singleton (mirrors whisper.cpp / vibevoice.cpp), so we
// serialize calls through base.SingleThread.
type ParakeetCpp struct {
	base.SingleThread
	ctxPtr uintptr
}

// Load is the LocalAI gRPC entry point for LoadModel: it calls
// parakeet_capi_load with the GGUF path and stashes the resulting
// opaque context pointer for AudioTranscription.
func (p *ParakeetCpp) Load(opts *pb.ModelOptions) error {
	if opts.ModelFile == "" {
		return errors.New("parakeet-cpp: ModelFile is required")
	}

	ctx := CppLoad(opts.ModelFile)
	if ctx == 0 {
		// No ctx to ask for last_error (the C-API's last-error buffer
		// lives on the ctx that was never returned). Surface the path
		// so the operator at least knows which load failed.
		return fmt.Errorf("parakeet-cpp: parakeet_capi_load failed for %q", opts.ModelFile)
	}
	p.ctxPtr = ctx
	return nil
}

// AudioTranscription runs parakeet_capi_transcribe_path_json on the wav at
// opts.Dst with the default decoder (decoder=0, which selects the right head
// per architecture: transducer for tdt/rnnt/hybrid, CTC for ctc) and shapes
// the per-word timestamps into a LocalAI TranscriptResult.
//
// Parakeet emits word- and token-level timestamps but no native segment
// boundaries, so we synthesise a single whole-clip segment spanning the first
// word start to the last word end. Word-level timings are attached only when
// the caller opts in via timestamp_granularities=["word"] (matching the
// OpenAI API, whose default is segment-level); token ids always populate
// Segment.Tokens.
//
// translate/diarize/prompt/temperature/language/threads are not applicable to
// parakeet and are ignored; streaming is handled by AudioTranscriptionStream
// (L2).
func (p *ParakeetCpp) AudioTranscription(_ context.Context, opts *pb.TranscriptRequest) (pb.TranscriptResult, error) {
	if p.ctxPtr == 0 {
		return pb.TranscriptResult{}, errors.New("parakeet-cpp: model not loaded")
	}
	if opts.Dst == "" {
		return pb.TranscriptResult{}, errors.New("parakeet-cpp: TranscriptRequest.dst (audio path) is required")
	}

	cstr := CppTranscribePathJSON(p.ctxPtr, opts.Dst, 0)
	if cstr == 0 {
		msg := CppLastError(p.ctxPtr)
		if msg == "" {
			msg = "unknown error"
		}
		return pb.TranscriptResult{}, fmt.Errorf("parakeet-cpp: transcribe_path_json failed: %s", msg)
	}

	raw := goStringFromCPtr(cstr)
	CppFreeString(cstr)

	var doc transcriptJSON
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return pb.TranscriptResult{}, fmt.Errorf("parakeet-cpp: decode transcript json: %w", err)
	}

	text := strings.TrimSpace(doc.Text)

	words := make([]*pb.TranscriptWord, 0, len(doc.Words))
	for _, w := range doc.Words {
		words = append(words, &pb.TranscriptWord{
			Start: secondsToNanos(w.Start),
			End:   secondsToNanos(w.End),
			Text:  w.W,
		})
	}

	tokens := make([]int32, 0, len(doc.Tokens))
	for _, t := range doc.Tokens {
		tokens = append(tokens, t.ID)
	}

	// Single whole-clip segment, spanning the first word start to the last
	// word end (0/0 when the clip produced no words).
	var segStart, segEnd int64
	if len(words) > 0 {
		segStart = words[0].Start
		segEnd = words[len(words)-1].End
	}
	seg := &pb.TranscriptSegment{
		Id:     0,
		Start:  segStart,
		End:    segEnd,
		Text:   text,
		Tokens: tokens,
	}
	if wordsRequested(opts.TimestampGranularities) {
		seg.Words = words
	}

	return pb.TranscriptResult{
		Text:     text,
		Segments: []*pb.TranscriptSegment{seg},
	}, nil
}

// wordsRequested reports whether the caller asked for word-level timestamps.
// The OpenAI transcription API gates word timings behind
// timestamp_granularities[] containing "word" and defaults to segment-level
// otherwise; we follow that contract.
func wordsRequested(granularities []string) bool {
	for _, g := range granularities {
		if strings.EqualFold(strings.TrimSpace(g), "word") {
			return true
		}
	}
	return false
}

// secondsToNanos converts the C-API's fractional-second timestamps into the
// int64 nanoseconds LocalAI carries on TranscriptSegment/TranscriptWord — the
// same nanosecond convention the whisper backend uses.
func secondsToNanos(sec float64) int64 {
	return int64(sec * 1e9)
}

// AudioTranscriptionStream is a placeholder for L0. L2 wires
// parakeet_capi_stream_{begin,feed,finalize} into a real cache-aware
// streaming flow with <EOU>/<EOB> events; until then the streaming gRPC
// endpoint surfaces this error rather than silently emitting nothing
// (the server would otherwise hang on its result channel).
func (p *ParakeetCpp) AudioTranscriptionStream(_ context.Context, _ *pb.TranscriptRequest, results chan *pb.TranscriptStreamResponse) error {
	close(results)
	return errors.New("parakeet-cpp: streaming not implemented in L0")
}

// Free releases the underlying parakeet_ctx. Called by LocalAI when the
// model is unloaded.
func (p *ParakeetCpp) Free() error {
	if p.ctxPtr != 0 {
		CppFree(p.ctxPtr)
		p.ctxPtr = 0
	}
	return nil
}

// goStringFromCPtr copies a NUL-terminated C string into Go memory.
// cptr is the raw pointer returned by purego from the C-API (a malloc'd
// buffer the caller owns); callers must free it via CppFreeString after
// the copy lands.
//
// The uintptr->unsafe.Pointer conversion below trips go vet's unsafeptr
// check, which can't distinguish a C-owned heap pointer from Go-managed
// memory. It is safe here: the pointer addresses a malloc'd C buffer the
// Go GC neither tracks nor moves, and we dereference it immediately to
// copy the bytes out — the same pattern (and the same tolerated warning)
// as the whisper backend's unsafe.Slice over segsPtr.
func goStringFromCPtr(cptr uintptr) string {
	if cptr == 0 {
		return ""
	}
	p := unsafe.Pointer(cptr)
	n := 0
	for *(*byte)(unsafe.Add(p, n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(p), n))
}
