package main

import (
	"fmt"
	"path/filepath"
	"unsafe"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
)

// capiABIVersion is the magpie_tts_capi.h surface this backend binds. Bumped
// upstream on any breaking signature/semantics change; refuse to run on a
// mismatch instead of crashing inside a miscompiled call.
const capiABIVersion = 1

var (
	// magpie_tts_capi_abi_version() int
	CppAbiVersion func() int
	// magpie_tts_capi_load(gguf_path) -> ctx (NULL on failure)
	CppLoad func(path string) uintptr
	// magpie_tts_capi_free(ctx)
	CppFree func(ctx uintptr)
	// magpie_tts_capi_synthesize(ctx, text, language, speaker, out_n) -> float*
	// 22050 Hz mono f32 PCM in [-1,1]; NULL on failure (see last_error).
	CppSynthesize func(ctx uintptr, text, language, speaker string, outN unsafe.Pointer) uintptr
	// magpie_tts_capi_free_audio(ptr)
	CppFreeAudio func(ptr uintptr)
	// magpie_tts_capi_last_error(ctx) -> const char* (ctx-owned, "" if none)
	CppLastError func(ctx uintptr) string
)

// MagpieTtsCpp serves the Magpie TTS Multilingual 357M GGUF through the
// magpie-tts.cpp C-API. The context is stateful (per-call error buffer, reused
// graph allocator) and NOT safe for concurrent synthesize calls, so
// base.SingleThread serializes everything behind the server-level lock.
type MagpieTtsCpp struct {
	base.SingleThread
	ctx  uintptr
	opts loadOptions
}

func (m *MagpieTtsCpp) Load(opts *pb.ModelOptions) error {
	model := opts.ModelFile
	if model == "" {
		model = opts.ModelPath
	}
	if !filepath.IsAbs(model) && opts.ModelPath != "" {
		model = filepath.Join(opts.ModelPath, model)
	}

	m.opts = parseOptions(opts.Options)

	if abi := CppAbiVersion(); abi != capiABIVersion {
		return fmt.Errorf("magpie-tts: C-API ABI mismatch: library reports v%d, backend built for v%d", abi, capiABIVersion)
	}

	xlog.Info("[magpie-tts-cpp] Load", "model", model)

	ctx := CppLoad(model)
	if ctx == 0 {
		// Load failures have no context to query last_error on; the C side
		// logs the reason to stderr.
		return fmt.Errorf("magpie-tts: failed to load model %q", model)
	}
	m.ctx = ctx
	return nil
}

// lastError surfaces the context's last error, falling back to a generic
// message when the C side left it empty.
func (m *MagpieTtsCpp) lastError() string {
	if m.ctx == 0 {
		return "no model loaded"
	}
	if e := CppLastError(m.ctx); e != "" {
		return e
	}
	return "unknown error"
}

// synthesize runs one C-API synthesis and copies the PCM out of C memory.
func (m *MagpieTtsCpp) synthesize(req *pb.TTSRequest) ([]float32, error) {
	if m.ctx == 0 {
		return nil, fmt.Errorf("magpie-tts: no model loaded")
	}
	if req.Text == "" {
		return nil, fmt.Errorf("magpie-tts: TTS requires text")
	}
	speaker, err := resolveSpeaker(req.Voice, m.opts.speaker)
	if err != nil {
		return nil, err
	}
	lang := resolveLanguage(req.Language, m.opts.language)

	var n int32
	ptr := CppSynthesize(m.ctx, req.Text, lang, speaker, unsafe.Pointer(&n)) // #nosec G103 -- out-param for the purego-bound C-API
	if ptr == 0 {
		return nil, fmt.Errorf("magpie-tts: synthesis failed: %s", m.lastError())
	}
	// Register the free as soon as we own a non-null buffer, so the n<=0 guard
	// below cannot leak it (defensive: the C contract returns NULL on failure).
	defer CppFreeAudio(ptr)
	if n <= 0 {
		return nil, fmt.Errorf("magpie-tts: synthesis produced no samples")
	}
	//nolint:govet // C-allocated PCM, copied out before free
	src := unsafe.Slice((*float32)(unsafe.Pointer(ptr)), int(n)) // #nosec G103 -- C-allocated PCM, copied out before free
	out := make([]float32, int(n))
	copy(out, src)
	return out, nil
}

func (m *MagpieTtsCpp) TTS(req *pb.TTSRequest) error {
	if req.Dst == "" {
		return fmt.Errorf("magpie-tts: TTS requires a destination path")
	}
	samples, err := m.synthesize(req)
	if err != nil {
		return err
	}
	return writeWAVMono(req.Dst, samples)
}

// TTSStream synthesizes one-shot (the magpie C-API has no streaming call) and
// then emits a self-describing mono WAV: a header chunk followed by the PCM in
// fixed-size slices, so the HTTP layer still receives a streamed WAV (the gRPC
// TTSStream path never sets Message, so the backend owns the header - see
// core/backend/tts.go:ModelTTSStream).
func (m *MagpieTtsCpp) TTSStream(req *pb.TTSRequest, results chan []byte) error {
	defer close(results)
	samples, err := m.synthesize(req)
	if err != nil {
		return err
	}
	results <- wavHeaderMono()
	const sampleChunk = 4096 // mono samples per emitted chunk
	for off := 0; off < len(samples); off += sampleChunk {
		end := off + sampleChunk
		if end > len(samples) {
			end = len(samples)
		}
		results <- floatToPCM16LE(samples[off:end])
	}
	return nil
}
