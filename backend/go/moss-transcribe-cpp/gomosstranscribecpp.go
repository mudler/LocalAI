package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/utils"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// purego-bound entry points from libmoss-transcribe.so. Names match
// moss_transcribe_capi.h exactly so a `nm libmoss-transcribe.so | grep
// moss_transcribe_capi` is enough to spot drift.
//
// The transcribe_* functions return char* declared here as uintptr so we can
// call moss_transcribe_capi_free_string on the same pointer after copying: the
// C-API contract is "caller owns and must free the returned buffer".
var (
	CppAbiVersion     func() int32
	CppLoad           func(ggufPath string) uintptr
	CppFree           func(ctx uintptr)
	CppTranscribePath func(ctx uintptr, wavPath string, maxNew int32) uintptr
	CppTranscribePcm  func(ctx uintptr, samples []float32, nSamples int32, sampleRate int32, maxNew int32) uintptr
	CppFreeString     func(s uintptr)
	CppLastError      func(ctx uintptr) string
)

// MossTranscribeCpp owns a single loaded moss_transcribe_ctx. MOSS is an
// offline transcription + diarization + timestamping engine: one model, one
// context, no streaming. The C engine holds a single mutable context and is
// not reentrant, so we embed base.SingleThread — LocalAI serialises every RPC
// through the server-level lock, and only one transcription touches the engine
// at a time.
type MossTranscribeCpp struct {
	base.SingleThread
	ctxPtr uintptr
	maxNew int32
}

// Load is the LocalAI gRPC entry point for LoadModel: it calls
// moss_transcribe_capi_load with the GGUF path and stashes the resulting
// opaque context pointer for AudioTranscription.
func (m *MossTranscribeCpp) Load(opts *pb.ModelOptions) error {
	if opts.ModelFile == "" {
		return errors.New("moss-transcribe-cpp: ModelFile is required")
	}

	// max_new_tokens caps the generated tokens per transcription; <=0 uses the
	// GGUF's default_max_new_tokens (the C-API's own default). Exposed as a
	// model YAML option: (key:value form, like the sibling ggml backends).
	m.maxNew = int32(optInt(opts, "max_new_tokens", 0))

	ctx := CppLoad(opts.ModelFile)
	if ctx == 0 {
		// No ctx to ask for last_error (the C-API's last-error buffer lives on
		// the ctx that was never returned). Surface the path so the operator at
		// least knows which load failed.
		return fmt.Errorf("moss-transcribe-cpp: moss_transcribe_capi_load failed for %q", opts.ModelFile)
	}
	m.ctxPtr = ctx
	return nil
}

// optInt reads an integer model option (key:value form) from ModelOptions,
// returning def when absent or unparseable. The options array carries the
// model YAML's options: entries (see core/config; siblings such as parakeet-cpp
// parse the same key:value form via strings.Cut on ":").
func optInt(opts *pb.ModelOptions, key string, def int) int {
	for _, o := range opts.GetOptions() {
		k, v, ok := strings.Cut(o, ":")
		if ok && strings.TrimSpace(k) == key {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				return n
			}
		}
	}
	return def
}

// AudioTranscription converts the audio at opts.Dst to a 16 kHz mono WAV and
// hands the path to moss_transcribe_capi_transcribe_path. The model emits its
// own speaker-labelled, time-aligned transcript in the compact
// "[start][Sxx]text[end]..." format (seconds); we parse it into LocalAI
// TranscriptSegments carrying int64-nanosecond timestamps and the per-segment
// speaker label.
//
// MOSS does joint transcription + diarization + timestamps in one pass, so
// translate/language/prompt/temperature/threads are not applicable and are
// ignored. Streaming is not supported (offline model).
func (m *MossTranscribeCpp) AudioTranscription(ctx context.Context, opts *pb.TranscriptRequest) (pb.TranscriptResult, error) {
	if m.ctxPtr == 0 {
		return pb.TranscriptResult{}, grpcerrors.ModelNotLoaded("moss-transcribe-cpp")
	}
	if opts.Dst == "" {
		return pb.TranscriptResult{}, errors.New("moss-transcribe-cpp: TranscriptRequest.dst (audio path) is required")
	}
	if err := ctx.Err(); err != nil {
		return pb.TranscriptResult{}, status.Error(codes.Canceled, "transcription cancelled")
	}

	// The C loader understands WAV; convert any input (MP3, etc.) to 16 kHz
	// mono WAV first - the same normalisation every other audio backend
	// (whisper, parakeet-cpp) does via utils.AudioToWav before handing the file
	// to the engine.
	converted, cleanup, err := convertToWavMono16k(opts.Dst)
	if err != nil {
		return pb.TranscriptResult{}, err
	}
	defer cleanup()

	cstr := CppTranscribePath(m.ctxPtr, converted, m.maxNew)
	if cstr == 0 {
		return pb.TranscriptResult{}, fmt.Errorf("moss-transcribe-cpp: transcribe_path failed: %s", CppLastError(m.ctxPtr))
	}
	raw := goStringFromCPtr(cstr)
	CppFreeString(cstr)

	return transcriptResultFromRaw(raw), nil
}

// Free releases the underlying moss_transcribe_ctx. Called by LocalAI when the
// model is unloaded.
func (m *MossTranscribeCpp) Free() error {
	if m.ctxPtr != 0 {
		CppFree(m.ctxPtr)
		m.ctxPtr = 0
	}
	return nil
}

// convertToWavMono16k converts an arbitrary audio file to a 16 kHz mono WAV in
// a fresh temp dir and returns the path together with a cleanup func the caller
// must defer. WAV inputs already at 16 kHz/mono/16-bit are passed through by
// utils.AudioToWav (hardlink/copy), everything else is transcoded via ffmpeg.
func convertToWavMono16k(path string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "moss-transcribe")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	converted := filepath.Join(dir, "converted.wav")
	if err := utils.AudioToWav(path, converted); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return converted, cleanup, nil
}

// goStringFromCPtr copies a NUL-terminated C string into Go memory. cptr is the
// raw pointer returned by purego from the C-API (a malloc'd buffer the caller
// owns); callers must free it via CppFreeString after the copy lands.
//
// The uintptr->unsafe.Pointer conversion below trips go vet's unsafeptr check,
// which can't distinguish a C-owned heap pointer from Go-managed memory. It is
// safe here: the pointer addresses a malloc'd C buffer the Go GC neither tracks
// nor moves, and we dereference it immediately to copy the bytes out (the same
// pattern the whisper / parakeet-cpp backends use).
func goStringFromCPtr(cptr uintptr) string {
	if cptr == 0 {
		return ""
	}
	p := unsafe.Pointer(cptr) //nolint:govet // C-owned malloc'd buffer, not Go-GC memory (see doc above)
	n := 0
	for *(*byte)(unsafe.Add(p, n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(p), n))
}
