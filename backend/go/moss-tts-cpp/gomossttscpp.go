package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
)

var (
	// mtl_load(local_path, codec_path, tokenizer_path) int
	CppLoad func(localPath, codecPath, tokenizerPath string) int
	// mtl_tts(text, reference_wav, seed, out_n, out_sr) -> float*
	CppTTS func(text, referenceWav string, seed int, outN, outSR unsafe.Pointer) uintptr
	// mtl_pcm_free(ptr)
	CppPCMFree func(ptr uintptr)
	// mtl_unload()
	CppUnload func()
)

type MossTtsCpp struct {
	base.SingleThread
	opts loadOptions
	// audioPath is the model-config reference voice (tts.audio_path), the
	// default clone reference when a request omits an audio Voice.
	audioPath string
}

func (m *MossTtsCpp) Load(opts *pb.ModelOptions) error {
	model := opts.ModelFile
	if model == "" {
		model = opts.ModelPath
	}
	if !filepath.IsAbs(model) && opts.ModelPath != "" {
		model = filepath.Join(opts.ModelPath, model)
	}

	m.opts = parseOptions(opts.Options)
	dir := filepath.Dir(model)

	// Resolve the codec GGUF (MOSS-Audio-Tokenizer): explicit option, else
	// auto-discover an *audio*tokenizer*/codec sibling of the model.
	codec := resolveAux(m.opts.codecPath, dir)
	if codec == "" {
		codec = discoverCodec(dir, model)
	}
	if codec == "" {
		return fmt.Errorf("moss-tts: no codec GGUF found; set option 'codec:<file>'")
	}
	m.opts.codecPath = codec

	// Resolve the text tokenizer GGUF: explicit option, else auto-discover the
	// *tokenizer* sibling that is not the audio codec or the model.
	tokenizer := resolveAux(m.opts.tokenizerPath, dir)
	if tokenizer == "" {
		tokenizer = discoverTokenizer(dir, model, codec)
	}
	if tokenizer == "" {
		return fmt.Errorf("moss-tts: no tokenizer GGUF found; set option 'tokenizer:<file>'")
	}
	m.opts.tokenizerPath = tokenizer

	m.audioPath = opts.AudioPath
	if m.audioPath != "" && !filepath.IsAbs(m.audioPath) {
		m.audioPath = filepath.Join(dir, m.audioPath)
	}

	xlog.Info("[moss-tts-cpp] Load", "model", model, "codec", codec, "tokenizer", tokenizer)

	if rc := CppLoad(model, codec, tokenizer); rc != 0 {
		return fmt.Errorf("moss-tts: failed to load model (rc=%d)", rc)
	}
	return nil
}

// resolveAux resolves an explicitly configured auxiliary GGUF path relative to
// the model directory when it is not absolute. Empty stays empty.
func resolveAux(p, dir string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}

// isAudioCodecName reports whether a GGUF filename denotes the MOSS-Audio-
// Tokenizer codec (e.g. moss-audio-tokenizer-v2-f32.gguf) rather than the text
// tokenizer (moss-tokenizer-v1_5.gguf).
func isAudioCodecName(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "codec") ||
		(strings.Contains(n, "audio") && strings.Contains(n, "tokenizer"))
}

// discoverCodec returns the first codec GGUF in dir (excluding the model), or "".
func discoverCodec(dir, model string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	modelBase := filepath.Base(model)
	for _, e := range entries {
		name := e.Name()
		if name == modelBase || !strings.HasSuffix(strings.ToLower(name), ".gguf") {
			continue
		}
		if isAudioCodecName(name) {
			return filepath.Join(dir, name)
		}
	}
	return ""
}

// discoverTokenizer returns the first text-tokenizer GGUF in dir that is neither
// the model nor the audio codec, or "".
func discoverTokenizer(dir, model, codec string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	modelBase := filepath.Base(model)
	codecBase := filepath.Base(codec)
	for _, e := range entries {
		name := e.Name()
		lower := strings.ToLower(name)
		if name == modelBase || name == codecBase || !strings.HasSuffix(lower, ".gguf") {
			continue
		}
		if strings.Contains(lower, "tokenizer") && !isAudioCodecName(name) {
			return filepath.Join(dir, name)
		}
	}
	return ""
}

// resolveRequest derives the synthesis inputs from a TTSRequest: the optional
// clone-reference WAV path and the seed. MOSS-TTS-Local drives cloning purely
// from a reference-audio path (the engine decodes it), so there is no
// language / speaker / instruct plumbing here (unlike qwen3-tts-cpp).
func (m *MossTtsCpp) resolveRequest(req *pb.TTSRequest) (refPath string, seed int) {
	refPath = resolveVoice(req.Voice)
	if refPath == "" && m.audioPath != "" {
		// No per-request voice: fall back to the config clone reference.
		refPath = m.audioPath
	}
	if refPath != "" && !filepath.IsAbs(refPath) {
		refPath = filepath.Join(filepath.Dir(m.opts.codecPath), refPath)
	}

	seed = m.opts.seed
	if req.Params != nil {
		seed = parseInt(req.Params["seed"], seed)
	}
	return
}

func (m *MossTtsCpp) TTS(req *pb.TTSRequest) error {
	if req.Dst == "" {
		return fmt.Errorf("moss-tts: TTS requires a destination path")
	}
	if req.Text == "" {
		return fmt.Errorf("moss-tts: TTS requires text")
	}
	refPath, seed := m.resolveRequest(req)

	var n, sr int32
	ptr := CppTTS(req.Text, refPath, seed, unsafe.Pointer(&n), unsafe.Pointer(&sr))
	if ptr == 0 {
		return fmt.Errorf("moss-tts: synthesis failed")
	}
	// Register the free as soon as we own a non-null buffer, so the n<=0 guard
	// below cannot leak it (defensive: the C contract returns NULL on failure).
	defer CppPCMFree(ptr)
	if n <= 0 {
		return fmt.Errorf("moss-tts: synthesis produced no samples")
	}
	src := unsafe.Slice((*float32)(unsafe.Pointer(ptr)), int(n)) //nolint:govet // C-allocated PCM, copied out before free
	out := make([]float32, int(n))
	copy(out, src)
	return writeWAVStereo(req.Dst, out, int(sr))
}

// TTSStream synthesizes one-shot (MOSS-TTS-Local has no native streaming C-API)
// and then emits a self-describing stereo WAV: a header chunk followed by the
// interleaved PCM in fixed-size slices, so the HTTP layer still receives a
// streamed WAV (the gRPC TTSStream path never sets Message, so the backend owns
// the header - see core/backend/tts.go:ModelTTSStream).
func (m *MossTtsCpp) TTSStream(req *pb.TTSRequest, results chan []byte) error {
	defer close(results)
	if req.Text == "" {
		return fmt.Errorf("moss-tts: TTSStream requires text")
	}
	refPath, seed := m.resolveRequest(req)

	var n, sr int32
	ptr := CppTTS(req.Text, refPath, seed, unsafe.Pointer(&n), unsafe.Pointer(&sr))
	if ptr == 0 {
		return fmt.Errorf("moss-tts: synthesis failed")
	}
	defer CppPCMFree(ptr)
	if n <= 0 {
		return fmt.Errorf("moss-tts: synthesis produced no samples")
	}
	src := unsafe.Slice((*float32)(unsafe.Pointer(ptr)), int(n)) //nolint:govet // C-allocated PCM, copied out before free
	out := make([]float32, int(n))
	copy(out, src)

	results <- wavHeaderStereo(int(sr))
	const frameChunk = 4096 // interleaved stereo samples per emitted chunk
	for off := 0; off < len(out); off += frameChunk {
		end := off + frameChunk
		if end > len(out) {
			end = len(out)
		}
		results <- floatToPCM16LE(out[off:end])
	}
	return nil
}
