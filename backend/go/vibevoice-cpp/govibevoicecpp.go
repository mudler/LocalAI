package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// purego-bound entry points from libgovibevoicecpp.
var (
	CppLoad func(ttsModel, asrModel, tokenizer, voice string, threads int32) int32
	CppTTS  func(text, voicePath, dstWav string,
		nSteps int32, cfgScale float32, maxSpeechFrames int32, seed uint32) int32
	CppASR func(srcWav string, outJSON []byte, capacity uint64,
		maxNewTokens int32) int32
	CppUnload  func()
	CppVersion func() string
)

// VibevoiceCpp speaks gRPC against vibevoice.cpp's flat C ABI. The
// engine is a single global, so we serialize calls through SingleThread.
type VibevoiceCpp struct {
	base.SingleThread
	threads int

	modelDir  string
	ttsModel  string
	asrModel  string
	tokenizer string
	voice     string
}

// firstMatch returns the first regular file in dir whose name matches
// any of the given glob patterns (relative to dir). Empty string if
// nothing matched.
func firstMatch(dir string, patterns ...string) string {
	for _, p := range patterns {
		matches, _ := filepath.Glob(filepath.Join(dir, p))
		sort.Strings(matches)
		for _, m := range matches {
			if info, err := os.Stat(m); err == nil && !info.IsDir() {
				return m
			}
		}
	}
	return ""
}

// resolveOption returns absPath if it's already absolute or names an
// existing file; otherwise treats it as a name relative to modelDir.
func (v *VibevoiceCpp) resolveOption(p string) string {
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return p
	}
	if _, err := os.Stat(p); err == nil {
		abs, _ := filepath.Abs(p)
		return abs
	}
	if v.modelDir != "" {
		return filepath.Join(v.modelDir, p)
	}
	return p
}

// applyOptionOverrides walks opts.Options[] and lets users override
// individual file paths at load time. Mirrors how other LocalAI
// backends consume `vibevoice.<key>=<value>` (e.g. sherpa-onnx).
func (v *VibevoiceCpp) applyOptionOverrides(opts []string) {
	for _, raw := range opts {
		k, val, ok := strings.Cut(raw, "=")
		if !ok {
			continue
		}
		key := strings.TrimSpace(k)
		val = strings.TrimSpace(val)
		switch key {
		case "vibevoice.tts_model", "tts_model":
			v.ttsModel = v.resolveOption(val)
		case "vibevoice.asr_model", "asr_model":
			v.asrModel = v.resolveOption(val)
		case "vibevoice.tokenizer", "tokenizer":
			v.tokenizer = v.resolveOption(val)
		case "vibevoice.voice", "voice":
			v.voice = v.resolveOption(val)
		}
	}
}

func (v *VibevoiceCpp) Load(opts *pb.ModelOptions) error {
	modelDir := opts.ModelFile
	if modelDir == "" {
		modelDir = opts.ModelPath
	}
	if !filepath.IsAbs(modelDir) && opts.ModelPath != "" {
		modelDir = filepath.Join(opts.ModelPath, modelDir)
	}
	if modelDir == "" {
		return fmt.Errorf("vibevoice-cpp: ModelFile must point at a directory containing the GGUFs")
	}

	info, err := os.Stat(modelDir)
	if err != nil {
		return fmt.Errorf("vibevoice-cpp: cannot stat ModelFile %q: %w", modelDir, err)
	}

	if info.IsDir() {
		v.modelDir = modelDir
		// Conventional names published in mudler/vibevoice.cpp-models.
		v.ttsModel = firstMatch(modelDir,
			"vibevoice-realtime-*-q8_0.gguf",
			"vibevoice-realtime-*-q4_k.gguf",
			"vibevoice-realtime-*.gguf")
		v.asrModel = firstMatch(modelDir,
			"vibevoice-asr-q4_k.gguf",
			"vibevoice-asr-q8_0.gguf",
			"vibevoice-asr-*.gguf")
		v.tokenizer = firstMatch(modelDir, "tokenizer.gguf", "*tokenizer*.gguf")
		v.voice = firstMatch(modelDir, "voice-en-*.gguf", "voice-*.gguf", "*.voice.gguf")
	} else {
		// A single file - assume it's the TTS model and look for
		// neighbours in its parent dir.
		v.modelDir = filepath.Dir(modelDir)
		v.ttsModel = modelDir
		v.asrModel = firstMatch(v.modelDir, "vibevoice-asr-*.gguf")
		v.tokenizer = firstMatch(v.modelDir, "tokenizer.gguf", "*tokenizer*.gguf")
		v.voice = firstMatch(v.modelDir, "voice-*.gguf")
	}

	v.applyOptionOverrides(opts.Options)

	if v.ttsModel == "" && v.asrModel == "" {
		return fmt.Errorf("vibevoice-cpp: no TTS or ASR gguf found in %s", v.modelDir)
	}
	if v.tokenizer == "" {
		return fmt.Errorf("vibevoice-cpp: tokenizer.gguf not found in %s", v.modelDir)
	}

	threads := int(opts.Threads)
	if threads <= 0 {
		threads = 4
	}
	v.threads = threads

	fmt.Fprintf(os.Stderr,
		"[vibevoice-cpp] Loading: tts=%q asr=%q tokenizer=%q voice=%q threads=%d\n",
		v.ttsModel, v.asrModel, v.tokenizer, v.voice, threads)

	if rc := CppLoad(v.ttsModel, v.asrModel, v.tokenizer, v.voice, int32(threads)); rc != 0 {
		return fmt.Errorf("vibevoice-cpp: vv_capi_load failed (rc=%d)", rc)
	}
	return nil
}

func (v *VibevoiceCpp) TTS(req *pb.TTSRequest) error {
	if v.ttsModel == "" {
		return fmt.Errorf("vibevoice-cpp: TTS requested but no realtime model was loaded")
	}
	text := req.Text
	dst := req.Dst
	if text == "" || dst == "" {
		return fmt.Errorf("vibevoice-cpp: TTS requires both text and dst")
	}

	voice := v.resolveOption(req.Voice)

	if req.Language != nil && *req.Language != "" {
		fmt.Fprintf(os.Stderr,
			"[vibevoice-cpp] note: TTSRequest.language=%q ignored — vibevoice picks language from the voice prompt\n",
			*req.Language)
	}

	const (
		defaultSteps     = 20
		defaultMaxFrames = 200
	)
	defaultCfg := float32(1.3)
	if rc := CppTTS(text, voice, dst,
		int32(defaultSteps), defaultCfg, int32(defaultMaxFrames), 0); rc != 0 {
		return fmt.Errorf("vibevoice-cpp: vv_capi_tts failed (rc=%d)", rc)
	}
	return nil
}

// asrSegment matches vibevoice's JSON output:
//
//	[{"Start":0.0,"End":2.8,"Speaker":0,"Content":"…"}, ...]
type asrSegment struct {
	Start   float64 `json:"Start"`
	End     float64 `json:"End"`
	Speaker int     `json:"Speaker"`
	Content string  `json:"Content"`
}

// callASR invokes vv_capi_asr with a buffer that grows on demand.
// vv_capi_asr returns: >0 bytes written, 0 no transcript, <0 error or
// -required_size. We honor the resize protocol once before giving up.
func (v *VibevoiceCpp) callASR(srcWav string, maxNewTokens int32) (string, error) {
	const startCap = 256 * 1024
	buf := make([]byte, startCap)
	rc := CppASR(srcWav, buf, uint64(len(buf)), maxNewTokens)
	if rc < 0 {
		need := -int(rc)
		if need > 0 && need < (16<<20) && need > len(buf) {
			buf = make([]byte, need+64)
			rc = CppASR(srcWav, buf, uint64(len(buf)), maxNewTokens)
		}
	}
	if rc < 0 {
		return "", fmt.Errorf("vibevoice-cpp: vv_capi_asr failed (rc=%d)", rc)
	}
	if rc == 0 {
		return "", nil
	}
	return string(buf[:rc]), nil
}

func (v *VibevoiceCpp) AudioTranscription(req *pb.TranscriptRequest) (pb.TranscriptResult, error) {
	if v.asrModel == "" {
		return pb.TranscriptResult{}, fmt.Errorf("vibevoice-cpp: AudioTranscription requested but no ASR model was loaded")
	}
	if req.Dst == "" {
		return pb.TranscriptResult{}, fmt.Errorf("vibevoice-cpp: TranscriptRequest.dst (audio path) is required")
	}

	out, err := v.callASR(req.Dst, 0)
	if err != nil {
		return pb.TranscriptResult{}, err
	}
	if out == "" {
		return pb.TranscriptResult{}, nil
	}

	var segs []asrSegment
	if err := json.Unmarshal([]byte(out), &segs); err != nil {
		// Some builds may emit a bare string instead of JSON. Treat it
		// as a single segment so the caller still sees the transcript.
		fmt.Fprintf(os.Stderr,
			"[vibevoice-cpp] WARNING: vv_capi_asr returned non-JSON, falling back to single segment: %v\n", err)
		return pb.TranscriptResult{
			Segments: []*pb.TranscriptSegment{{Id: 0, Text: strings.TrimSpace(out)}},
			Text:     strings.TrimSpace(out),
		}, nil
	}

	segments := make([]*pb.TranscriptSegment, 0, len(segs))
	parts := make([]string, 0, len(segs))
	var duration float32
	for i, s := range segs {
		// LocalAI's whisper backend uses int64 100ns ticks for
		// Start/End (seconds * 1e7); follow the same convention so
		// consumers can mix vibevoice and whisper transcripts.
		segments = append(segments, &pb.TranscriptSegment{
			Id:      int32(i),
			Text:    s.Content,
			Start:   int64(s.Start * 1e7),
			End:     int64(s.End * 1e7),
			Speaker: fmt.Sprintf("%d", s.Speaker),
		})
		parts = append(parts, strings.TrimSpace(s.Content))
		if float32(s.End) > duration {
			duration = float32(s.End)
		}
	}
	return pb.TranscriptResult{
		Segments: segments,
		Text:     strings.TrimSpace(strings.Join(parts, " ")),
		Duration: duration,
	}, nil
}
