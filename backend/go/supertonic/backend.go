package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	laudio "github.com/mudler/LocalAI/pkg/audio"
	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// onnxProvider is set via -ldflags "-X main.onnxProvider=cuda" by the
// CUDA build (later phase). Defaults to CPU.
var onnxProvider = "cpu"

// Per-model generation defaults, overridable via ModelOptions.Options:
//   supertonic.steps=<int>          denoising steps (quality), default 8
//   supertonic.speed=<float>        speech rate, default 1.05
//   supertonic.silence=<float>      inter-chunk silence seconds, default 0.3
//   supertonic.default_voice=<name> voice-style used when request omits voice
//   supertonic.default_lang=<lang>  language tag used when request omits it
const (
	optionSteps        = "supertonic.steps="
	optionSpeed        = "supertonic.speed="
	optionSilence      = "supertonic.silence="
	optionDefaultVoice = "supertonic.default_voice="
	optionDefaultLang  = "supertonic.default_lang="
)

type SupertonicBackend struct {
	base.SingleThread

	tts          *TextToSpeech
	cfg          Config
	modelDir     string
	defaultVoice string
	defaultLang  string
	steps        int
	speed        float32
	silence      float32

	styleMu sync.Mutex
	styles  map[string]*Style // voice name -> loaded style cache
}

func (s *SupertonicBackend) Load(opts *pb.ModelOptions) error {
	modelDir, err := resolveModelDir(opts.ModelFile)
	if err != nil {
		return err
	}
	s.modelDir = modelDir

	cfg, err := LoadCfgs(modelDir)
	if err != nil {
		return fmt.Errorf("loading tts.json from %s: %w", modelDir, err)
	}
	s.cfg = cfg

	tts, err := LoadTextToSpeech(modelDir, false, cfg)
	if err != nil {
		return fmt.Errorf("loading supertonic models from %s: %w", modelDir, err)
	}
	s.tts = tts

	s.steps = int(findOptionInt(opts, optionSteps, 8))
	s.speed = findOptionFloat(opts, optionSpeed, 1.05)
	s.silence = findOptionFloat(opts, optionSilence, 0.3)
	s.defaultVoice = findOptionValue(opts, optionDefaultVoice, "")
	s.defaultLang = findOptionValue(opts, optionDefaultLang, "na")
	s.styles = map[string]*Style{}
	return nil
}

func (s *SupertonicBackend) TTS(req *pb.TTSRequest) error {
	wav, sr, err := s.synthesize(req)
	if err != nil {
		return err
	}
	out := make([]float64, len(wav))
	for i, v := range wav {
		out[i] = float64(v)
	}
	if err := writeWavFile(req.Dst, out, sr); err != nil {
		return fmt.Errorf("writing wav to %s: %w", req.Dst, err)
	}
	return nil
}

func (s *SupertonicBackend) TTSStream(req *pb.TTSRequest, results chan []byte) error {
	defer close(results)

	wav, sr, err := s.synthesize(req)
	if err != nil {
		return err
	}

	results <- streamingWAVHeader(uint32(sr))

	const chunkSamples = 4096
	for off := 0; off < len(wav); off += chunkSamples {
		end := off + chunkSamples
		if end > len(wav) {
			end = len(wav)
		}
		results <- pcmFloatToInt16LE(wav[off:end])
	}
	return nil
}

// synthesize runs the full pipeline and returns the trimmed mono float32
// PCM and its sample rate.
func (s *SupertonicBackend) synthesize(req *pb.TTSRequest) ([]float32, int, error) {
	if s.tts == nil {
		return nil, 0, fmt.Errorf("supertonic model not loaded")
	}
	if strings.TrimSpace(req.Text) == "" {
		return nil, 0, fmt.Errorf("empty text")
	}

	style, err := s.loadStyle(s.voiceName(req.Voice))
	if err != nil {
		return nil, 0, err
	}

	lang := s.resolveLang("")
	if req.Language != nil {
		lang = s.resolveLang(*req.Language)
	}

	wav, dur, err := s.tts.Call(req.Text, lang, style, s.steps, s.speed, s.silence)
	if err != nil {
		return nil, 0, err
	}

	sr := s.tts.SampleRate
	// Call returns concatenated audio; trim to the reported duration.
	wavLen := int(float32(sr) * dur)
	if wavLen > len(wav) {
		wavLen = len(wav)
	}
	return wav[:wavLen], sr, nil
}

// voiceName picks the request voice, falling back to the model default.
func (s *SupertonicBackend) voiceName(reqVoice string) string {
	v := strings.TrimSpace(reqVoice)
	if v == "" {
		return s.defaultVoice
	}
	return v
}

// resolveLang validates against AvailableLangs, falling back to the model
// default (then "na").
func (s *SupertonicBackend) resolveLang(reqLang string) string {
	l := strings.TrimSpace(reqLang)
	if l != "" && isValidLang(l) {
		return l
	}
	if s.defaultLang != "" && isValidLang(s.defaultLang) {
		return s.defaultLang
	}
	return "na"
}

// loadStyle resolves and caches a voice-style. An empty name with no model
// default is an error (supertonic requires a style embedding).
func (s *SupertonicBackend) loadStyle(name string) (*Style, error) {
	if name == "" {
		return nil, fmt.Errorf("no voice specified and no supertonic.default_voice set")
	}
	s.styleMu.Lock()
	defer s.styleMu.Unlock()
	if st, ok := s.styles[name]; ok {
		return st, nil
	}
	path := s.voiceStylePath(name)
	st, err := LoadVoiceStyle([]string{path}, false)
	if err != nil {
		return nil, fmt.Errorf("loading voice style %q (%s): %w", name, path, err)
	}
	s.styles[name] = st
	return st, nil
}

// voiceStylePath maps a voice name to a JSON path. Absolute paths and
// explicit .json names are honored; bare names resolve under
// <modelDir>/voice_styles/<name>.json.
func (s *SupertonicBackend) voiceStylePath(name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	if !strings.HasSuffix(name, ".json") {
		name += ".json"
	}
	if strings.ContainsRune(name, filepath.Separator) {
		return filepath.Join(s.modelDir, name)
	}
	return filepath.Join(s.modelDir, "voice_styles", name)
}

// resolveModelDir accepts either a directory (used as-is) or a file (its
// parent dir is used).
func resolveModelDir(modelFile string) (string, error) {
	if modelFile == "" {
		return "", fmt.Errorf("empty model path")
	}
	info, err := os.Stat(modelFile)
	if err != nil {
		return "", fmt.Errorf("stat model path %s: %w", modelFile, err)
	}
	if info.IsDir() {
		return modelFile, nil
	}
	return filepath.Dir(modelFile), nil
}

// ---- option helpers (mirrors backend/go/sherpa-onnx/backend.go) ----

func findOptionValue(opts *pb.ModelOptions, prefix, def string) string {
	for _, o := range opts.Options {
		if strings.HasPrefix(o, prefix) {
			return strings.TrimPrefix(o, prefix)
		}
	}
	return def
}

func findOptionFloat(opts *pb.ModelOptions, prefix string, def float32) float32 {
	raw := findOptionValue(opts, prefix, "")
	if raw == "" {
		return def
	}
	v, err := strconv.ParseFloat(raw, 32)
	if err != nil {
		return def
	}
	return float32(v)
}

func findOptionInt(opts *pb.ModelOptions, prefix string, def int32) int32 {
	raw := findOptionValue(opts, prefix, "")
	if raw == "" {
		return def
	}
	v, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return def
	}
	return int32(v)
}

// ---- PCM helpers ----

func pcmFloatToInt16LE(samples []float32) []byte {
	buf := make([]byte, len(samples)*2)
	for i, f := range samples {
		v := int32(f * 32767)
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		binary.LittleEndian.PutUint16(buf[2*i:], uint16(int16(v)))
	}
	return buf
}

func streamingWAVHeader(sampleRate uint32) []byte {
	const streamingSize = 0xFFFFFFFF
	h := laudio.NewWAVHeaderWithRate(streamingSize, sampleRate)
	h.ChunkSize = streamingSize
	var buf bytes.Buffer
	_ = h.Write(&buf)
	return buf.Bytes()
}
