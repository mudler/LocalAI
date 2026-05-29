package main

import (
	"context"
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
	CppAbiVersion     func() int32
	CppLoad           func(ggufPath string) uintptr
	CppFree           func(ctx uintptr)
	CppTranscribePath func(ctx uintptr, wavPath string, decoder int32) uintptr
	CppFreeString     func(s uintptr)
	CppLastError      func(ctx uintptr) string
)

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

// AudioTranscription runs parakeet_capi_transcribe_path on the wav at
// opts.Dst. For L0 we ask the default decoder (decoder=0) which selects
// the right head per architecture (transducer for tdt/rnnt/hybrid, CTC
// for ctc).
//
// For L0 only `dst` from the request is honoured; translate/diarize/
// prompt/temperature/language/threads/timestamp_granularities/stream
// are ignored — L1 wires word/segment timestamps via
// parakeet_capi_transcribe_path_json, L2 adds AudioTranscriptionStream.
//
// The response carries a single synthesised segment spanning the whole
// clip so downstream HTTP shapers (which expect at least one segment)
// stay happy; per-segment timings come in L1.
func (p *ParakeetCpp) AudioTranscription(_ context.Context, opts *pb.TranscriptRequest) (pb.TranscriptResult, error) {
	if p.ctxPtr == 0 {
		return pb.TranscriptResult{}, errors.New("parakeet-cpp: model not loaded")
	}
	if opts.Dst == "" {
		return pb.TranscriptResult{}, errors.New("parakeet-cpp: TranscriptRequest.dst (audio path) is required")
	}

	cstr := CppTranscribePath(p.ctxPtr, opts.Dst, 0)
	if cstr == 0 {
		msg := CppLastError(p.ctxPtr)
		if msg == "" {
			msg = "unknown error"
		}
		return pb.TranscriptResult{}, fmt.Errorf("parakeet-cpp: transcribe_path failed: %s", msg)
	}

	text := strings.TrimSpace(goStringFromCPtr(cstr))
	CppFreeString(cstr)

	return pb.TranscriptResult{
		Text: text,
		Segments: []*pb.TranscriptSegment{
			{Id: 0, Start: 0, End: 0, Text: text},
		},
	}, nil
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
