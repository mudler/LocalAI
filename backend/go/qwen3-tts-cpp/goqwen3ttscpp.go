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
	// qt3_load(talker_path, codec_path, use_fa, clamp_fp16) int
	CppLoad func(talkerPath, codecPath string, useFA, clampFP16 int) int
	// qt3_tts(text, lang, instruct, speaker, ref_samples, ref_n, ref_text,
	//         seed, temperature, top_k, top_p, rep_pen, max_new, out_n) -> float*
	CppTTS func(text, lang, instruct, speaker string, refSamples unsafe.Pointer,
		refN int, refText string, seed int64, temperature float32, topK int,
		topP, repPen float32, maxNew int, outN unsafe.Pointer) uintptr
	// qt3_tts_stream(..., cb, user) int
	CppTTSStream func(text, lang, instruct, speaker string, refSamples unsafe.Pointer,
		refN int, refText string, seed int64, temperature float32, topK int,
		topP, repPen float32, maxNew int, cb uintptr, user uintptr) int
	CppPCMFree func(ptr uintptr)
	CppUnload  func()
)

type Qwen3TtsCpp struct {
	base.SingleThread
	opts loadOptions
	// audioPath is the model-config reference voice (tts.audio_path), the
	// default clone reference when a request omits an audio Voice.
	audioPath string
}

func (q *Qwen3TtsCpp) Load(opts *pb.ModelOptions) error {
	model := opts.ModelFile
	if model == "" {
		model = opts.ModelPath
	}
	if !filepath.IsAbs(model) && opts.ModelPath != "" {
		model = filepath.Join(opts.ModelPath, model)
	}

	q.opts = parseOptions(opts.Options)

	// Resolve the codec/tokenizer GGUF: explicit option, else auto-discover a
	// *tokenizer*.gguf sibling of the talker model.
	codec := q.opts.codecPath
	if codec != "" && !filepath.IsAbs(codec) {
		codec = filepath.Join(filepath.Dir(model), codec)
	}
	if codec == "" {
		codec = discoverTokenizer(filepath.Dir(model))
	}
	if codec == "" {
		return fmt.Errorf("qwen3-tts: no codec/tokenizer GGUF found; set option 'tokenizer:<file>'")
	}
	q.opts.codecPath = codec

	q.audioPath = opts.AudioPath
	if q.audioPath != "" && !filepath.IsAbs(q.audioPath) {
		q.audioPath = filepath.Join(filepath.Dir(model), q.audioPath)
	}

	useFA := boolToInt(q.opts.useFA)
	clamp := boolToInt(q.opts.clampFP16)

	fmt.Fprintf(os.Stderr, "[qwen3-tts-cpp] Load talker=%s codec=%s use_fa=%d clamp_fp16=%d\n",
		model, codec, useFA, clamp)

	if rc := CppLoad(model, codec, useFA, clamp); rc != 0 {
		return fmt.Errorf("qwen3-tts: failed to load model (rc=%d)", rc)
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

func optStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// resolveRequest derives the synthesis inputs from a TTSRequest:
// language, instruct, speaker, ref-audio samples, ref-text and sampling.
func (q *Qwen3TtsCpp) resolveRequest(req *pb.TTSRequest) (lang, instruct, speaker, refText string, ref []float32, s sampling, err error) {
	lang = normalizeLanguage(optStr(req.Language))
	instruct = optStr(req.Instructions)

	var refPath string
	speaker, refPath = resolveVoice(req.Voice)
	if refPath == "" && speaker == "" && q.audioPath != "" {
		// No per-request voice: fall back to the config clone reference.
		refPath = q.audioPath
	}
	if refPath != "" {
		ref, err = readWAVAsFloat(refPath)
		if err != nil {
			return
		}
	}

	if req.Params != nil {
		refText = req.Params["ref_text"]
	}
	s = parseSampling(req.Params, q.opts.seed)
	return
}

func (q *Qwen3TtsCpp) TTS(req *pb.TTSRequest) error {
	if req.Dst == "" {
		return fmt.Errorf("qwen3-tts: TTS requires a destination path")
	}
	if req.Text == "" {
		return fmt.Errorf("qwen3-tts: TTS requires text")
	}
	lang, instruct, speaker, refText, ref, s, err := q.resolveRequest(req)
	if err != nil {
		return err
	}
	var refPtr unsafe.Pointer
	if len(ref) > 0 {
		refPtr = unsafe.Pointer(&ref[0])
	}

	var n int32
	ptr := CppTTS(req.Text, lang, instruct, speaker, refPtr, len(ref), refText,
		s.seed, s.temperature, s.topK, s.topP, s.repPen, s.maxNew, unsafe.Pointer(&n))
	runtimeKeepAlive(ref)
	if ptr == 0 {
		return fmt.Errorf("qwen3-tts: synthesis failed")
	}
	// Register the free as soon as we own a non-null buffer, so the n<=0 guard
	// below cannot leak it (defensive: the C contract returns NULL on failure).
	defer CppPCMFree(ptr)
	if n <= 0 {
		return fmt.Errorf("qwen3-tts: synthesis produced no samples")
	}
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

func (q *Qwen3TtsCpp) TTSStream(req *pb.TTSRequest, results chan []byte) error {
	defer close(results)
	if req.Text == "" {
		return fmt.Errorf("qwen3-tts: TTSStream requires text")
	}

	streamCbOnce.Do(func() {
		streamCbPtr = purego.NewCallback(streamCallback)
	})

	lang, instruct, speaker, refText, ref, s, err := q.resolveRequest(req)
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
	rc := CppTTSStream(req.Text, lang, instruct, speaker, refPtr, len(ref), refText,
		s.seed, s.temperature, s.topK, s.topP, s.repPen, s.maxNew, streamCbPtr, 0)
	streamChan = nil
	streamMu.Unlock()
	runtimeKeepAlive(ref)

	if rc != 0 {
		return fmt.Errorf("qwen3-tts: streaming synthesis failed (rc=%d)", rc)
	}
	return nil
}
