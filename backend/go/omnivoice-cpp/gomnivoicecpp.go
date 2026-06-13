package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

var (
	// omni_load(model_path, codec_path, use_fa, clamp_fp16) int
	CppLoad func(modelPath, codecPath string, useFA, clampFP16 int) int
	// omni_tts(text, lang, instruct, ref_samples, ref_n, ref_text, seed, denoise, out_n) -> float* (uintptr)
	CppTTS func(text, lang, instruct string, refSamples unsafe.Pointer, refN int,
		refText string, seed int64, denoise int, outN unsafe.Pointer) uintptr
	// omni_tts_stream(text, lang, instruct, ref_samples, ref_n, ref_text, seed, denoise, cb, user) int
	CppTTSStream func(text, lang, instruct string, refSamples unsafe.Pointer, refN int,
		refText string, seed int64, denoise int, cb uintptr, user uintptr) int
	CppPCMFree func(ptr uintptr)
	CppUnload  func()
)

type OmnivoiceCpp struct {
	base.SingleThread
	opts loadOptions
}

func (o *OmnivoiceCpp) Load(opts *pb.ModelOptions) error {
	model := opts.ModelFile
	if model == "" {
		model = opts.ModelPath
	}
	if !filepath.IsAbs(model) && opts.ModelPath != "" {
		model = filepath.Join(opts.ModelPath, model)
	}

	o.opts = parseOptions(opts.Options)

	// Resolve the codec/tokenizer GGUF: explicit option, else auto-discover a
	// *tokenizer*.gguf sibling of the base model.
	codec := o.opts.codecPath
	if codec != "" && !filepath.IsAbs(codec) {
		codec = filepath.Join(filepath.Dir(model), codec)
	}
	if codec == "" {
		codec = discoverTokenizer(filepath.Dir(model))
	}
	if codec == "" {
		return fmt.Errorf("omnivoice: no codec/tokenizer GGUF found; set option 'tokenizer:<file>'")
	}
	o.opts.codecPath = codec

	useFA := boolToInt(o.opts.useFA)
	clamp := boolToInt(o.opts.clampFP16)

	fmt.Fprintf(os.Stderr, "[omnivoice-cpp] Load model=%s codec=%s use_fa=%d clamp_fp16=%d\n",
		model, codec, useFA, clamp)

	if rc := CppLoad(model, codec, useFA, clamp); rc != 0 {
		return fmt.Errorf("omnivoice: failed to load model (rc=%d)", rc)
	}
	return nil
}

// discoverTokenizer returns the first *tokenizer*.gguf in dir, or "".
func discoverTokenizer(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		name := strings.ToLower(e.Name())
		if strings.Contains(name, "tokenizer") && strings.HasSuffix(name, ".gguf") {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// refAudio loads the reference WAV (voice cloning) if voice points to a file.
// Returns nil if no cloning (empty or non-path - voice design uses Instructions).
func (o *OmnivoiceCpp) refAudio(voice string) ([]float32, error) {
	v := strings.TrimSpace(voice)
	if v == "" {
		return nil, nil
	}
	if _, err := os.Stat(v); err != nil {
		return nil, nil
	}
	return readWAVAsFloat(v)
}

func reqParam(req *pb.TTSRequest, key string) string {
	if req.Params == nil {
		return ""
	}
	return req.Params[key]
}

func (o *OmnivoiceCpp) seedFor(req *pb.TTSRequest) int64 {
	if s := reqParam(req, "seed"); s != "" {
		var n int64
		if _, err := fmt.Sscan(s, &n); err == nil {
			return n
		}
	}
	return o.opts.seed
}

func optStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (o *OmnivoiceCpp) TTS(req *pb.TTSRequest) error {
	if req.Dst == "" {
		return fmt.Errorf("omnivoice: TTS requires a destination path")
	}
	lang := normalizeLanguage(optStr(req.Language))
	instruct := optStr(req.Instructions)
	refText := reqParam(req, "ref_text")
	seed := o.seedFor(req)

	ref, err := o.refAudio(req.Voice)
	if err != nil {
		return err
	}
	var refPtr unsafe.Pointer
	if len(ref) > 0 {
		refPtr = unsafe.Pointer(&ref[0])
	}

	var n int32
	ptr := CppTTS(req.Text, lang, instruct, refPtr, len(ref), refText, seed,
		boolToInt(o.opts.denoise), unsafe.Pointer(&n))
	runtimeKeepAlive(ref)
	if ptr == 0 || n <= 0 {
		return fmt.Errorf("omnivoice: synthesis failed")
	}
	defer CppPCMFree(ptr)
	src := unsafe.Slice((*float32)(unsafe.Pointer(ptr)), int(n)) //nolint:govet // C-allocated PCM, copied out before free
	out := make([]float32, int(n))
	copy(out, src)
	return writeWAV24k(req.Dst, out)
}

// streamState carries the active TTSStream channel to the single shared C
// callback. base.SingleThread serializes TTS/TTSStream, so one global slot is
// safe and avoids leaking a purego callback per request (purego callbacks
// cannot be freed and are capped).
var (
	streamMu     sync.Mutex
	streamChan   chan []byte
	streamCbOnce sync.Once
	streamCbPtr  uintptr
)

// streamCallback is registered once and forwards each PCM chunk to streamChan.
func streamCallback(samples *float32, nSamples int32, _ uintptr) uintptr {
	if nSamples <= 0 || samples == nil || streamChan == nil {
		return 1 // continue
	}
	src := unsafe.Slice(samples, int(nSamples))
	cp := make([]float32, int(nSamples)) // copy out of C memory before returning
	copy(cp, src)
	streamChan <- floatToPCM16LE(cp)
	return 1 // continue
}

func (o *OmnivoiceCpp) TTSStream(req *pb.TTSRequest, results chan []byte) error {
	defer close(results)
	if req.Text == "" {
		return fmt.Errorf("omnivoice: TTSStream requires text")
	}

	streamCbOnce.Do(func() {
		streamCbPtr = purego.NewCallback(streamCallback)
	})

	lang := normalizeLanguage(optStr(req.Language))
	instruct := optStr(req.Instructions)
	refText := reqParam(req, "ref_text")
	seed := o.seedFor(req)

	ref, err := o.refAudio(req.Voice)
	if err != nil {
		return err
	}
	var refPtr unsafe.Pointer
	if len(ref) > 0 {
		refPtr = unsafe.Pointer(&ref[0])
	}

	// Emit the WAV header first so the HTTP layer gets a self-describing stream.
	results <- wavHeader24k()

	streamMu.Lock()
	streamChan = results
	rc := CppTTSStream(req.Text, lang, instruct, refPtr, len(ref), refText, seed,
		boolToInt(o.opts.denoise), streamCbPtr, 0)
	streamChan = nil
	streamMu.Unlock()
	runtimeKeepAlive(ref)

	if rc != 0 {
		return fmt.Errorf("omnivoice: streaming synthesis failed (rc=%d)", rc)
	}
	return nil
}
