package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	laudio "github.com/mudler/LocalAI/pkg/audio"
	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// vibevoice.cpp synthesizes 24 kHz mono 16-bit PCM. Hardcoded - the
// model itself is fixed-rate; if the upstream ever changes this we'll
// pick it up via vv_capi_version().
const vibevoiceSampleRate = uint32(24000)

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

	// modelRoot is the directory we use to resolve relative paths
	// from Options[] and per-call overrides (TTSRequest.Voice).
	// Source of truth: opts.ModelPath; falls back to the dir of
	// the primary ModelFile when ModelPath is empty.
	modelRoot string

	ttsModel  string
	asrModel  string
	tokenizer string
	voice     string
}

// resolvePath joins a relative path onto `relTo`. The gallery
// convention is that Options[] carry paths relative to the LocalAI
// models dir (opts.ModelPath), so anything not absolute is treated
// as a sibling of the primary ModelFile - never CWD. Empty / already-
// absolute / no-relTo inputs pass through unchanged.
func resolvePath(p, relTo string) string {
	if p == "" || filepath.IsAbs(p) || relTo == "" {
		return p
	}
	return filepath.Join(relTo, p)
}

// parseOptions reads opts.Options[] and pulls out the per-role
// overrides documented in the gallery entries. Accepts both "key=value"
// (gallery YAML style) and "key:value" (Make-target / env-var style).
func (v *VibevoiceCpp) parseOptions(opts []string, relTo string) string {
	role := ""
	for _, raw := range opts {
		k, val, ok := strings.Cut(raw, "=")
		if !ok {
			k, val, ok = strings.Cut(raw, ":")
			if !ok {
				continue
			}
		}
		key := strings.TrimSpace(k)
		val = strings.TrimSpace(val)
		switch key {
		case "type":
			role = strings.ToLower(val)
		case "tokenizer":
			v.tokenizer = resolvePath(val, relTo)
		case "voice":
			v.voice = resolvePath(val, relTo)
		case "tts_model":
			v.ttsModel = resolvePath(val, relTo)
		case "asr_model":
			v.asrModel = resolvePath(val, relTo)
		}
	}
	return role
}

func (v *VibevoiceCpp) Load(opts *pb.ModelOptions) error {
	if opts.ModelFile == "" {
		return fmt.Errorf("vibevoice-cpp: ModelFile is required")
	}
	modelFile := opts.ModelFile
	if !filepath.IsAbs(modelFile) && opts.ModelPath != "" {
		modelFile = filepath.Join(opts.ModelPath, modelFile)
	}

	// ModelPath is the LocalAI core's models root, propagated over
	// gRPC. Use it as the resolution base for Options[] (and later
	// for TTSRequest.Voice) so gallery entries can reference paths
	// like "tokenizer=tokenizer.gguf" and have them resolved
	// against the same root the core used to drop the files.
	v.modelRoot = opts.ModelPath
	if v.modelRoot == "" {
		v.modelRoot = filepath.Dir(modelFile)
	}
	role := v.parseOptions(opts.Options, v.modelRoot)

	// ModelFile fills the "primary" role-slot determined by `type=`
	// in Options (defaults to tts). The other slot stays exactly as
	// Options set it - so a closed-loop config with ModelFile=tts.gguf
	// + Options[asr_model=asr.gguf] resolves correctly to both slots,
	// and an explicit `tts_model=` / `asr_model=` always wins over
	// ModelFile for its own slot.
	primaryIsASR := false
	switch role {
	case "asr", "transcript", "stt", "speech-to-text":
		primaryIsASR = true
	}
	if primaryIsASR {
		if v.asrModel == "" {
			v.asrModel = modelFile
		}
	} else if v.ttsModel == "" {
		v.ttsModel = modelFile
	}

	if v.ttsModel == "" && v.asrModel == "" {
		return fmt.Errorf("vibevoice-cpp: no TTS or ASR model resolved from ModelFile=%q + options", opts.ModelFile)
	}
	if v.tokenizer == "" {
		return fmt.Errorf("vibevoice-cpp: tokenizer is required - pass options: [tokenizer=<path>]")
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

	// req.Voice may be a bare filename (e.g. "voice-en-Emma.gguf") or an
	// absolute path. Resolve via the same modelRoot Load() used for
	// Options[] so a swap-voice request mirrors the gallery's layout.
	voice := resolvePath(req.Voice, v.modelRoot)

	if req.Language != nil && *req.Language != "" {
		fmt.Fprintf(os.Stderr,
			"[vibevoice-cpp] note: TTSRequest.language=%q ignored - vibevoice picks language from the voice prompt\n",
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

// TTSStream is the streaming counterpart to TTS. vibevoice's C ABI is
// file-only (vv_capi_tts writes a complete WAV), so we synthesize to
// a tempfile, then emit a streaming-WAV header followed by the PCM
// body in chunks. The main reason this exists at all is the gRPC
// server wrapper (pkg/grpc/server.go:TTSStream) blocks on a channel
// that only this method can close - if we leave the default Base
// stub in place, every TTSStream call hangs until the client
// deadline.
func (v *VibevoiceCpp) TTSStream(req *pb.TTSRequest, results chan []byte) error {
	defer close(results)
	if v.ttsModel == "" {
		return fmt.Errorf("vibevoice-cpp: TTSStream requested but no realtime model was loaded")
	}
	if req.Text == "" {
		return fmt.Errorf("vibevoice-cpp: TTSStream requires text")
	}

	tmp, err := os.CreateTemp("", "vibevoice-cpp-stream-*.wav")
	if err != nil {
		return fmt.Errorf("vibevoice-cpp: tempfile: %w", err)
	}
	dst := tmp.Name()
	_ = tmp.Close()
	defer func() { _ = os.Remove(dst) }()

	if err := v.TTS(&pb.TTSRequest{
		Text:     req.Text,
		Voice:    req.Voice,
		Dst:      dst,
		Language: req.Language,
	}); err != nil {
		return err
	}

	wav, err := os.ReadFile(dst)
	if err != nil {
		return fmt.Errorf("vibevoice-cpp: read tempfile: %w", err)
	}

	// Streaming WAV header: declare 0xFFFFFFFF for chunk sizes so HTTP
	// clients can start playback before they see the full PCM.
	const streamingSize = 0xFFFFFFFF
	hdr := laudio.NewWAVHeaderWithRate(streamingSize, vibevoiceSampleRate)
	hdr.ChunkSize = streamingSize
	hdrBuf := make([]byte, 0, laudio.WAVHeaderSize)
	w := newByteWriter(&hdrBuf)
	if err := hdr.Write(w); err != nil {
		return fmt.Errorf("vibevoice-cpp: write WAV header: %w", err)
	}
	results <- hdrBuf

	// PCM body: send in ~64 KB slices so the client gets multiple
	// reply chunks (e2e harness asserts >=2 frames).
	pcm := laudio.StripWAVHeader(wav)
	const chunkBytes = 64 * 1024
	for off := 0; off < len(pcm); off += chunkBytes {
		end := off + chunkBytes
		if end > len(pcm) {
			end = len(pcm)
		}
		chunk := make([]byte, end-off)
		copy(chunk, pcm[off:end])
		results <- chunk
	}
	return nil
}

// byteWriter adapts a *[]byte to io.Writer so we can hand it to
// laudio.WAVHeader.Write without allocating a bytes.Buffer.
type byteWriter struct{ buf *[]byte }

func newByteWriter(b *[]byte) *byteWriter { return &byteWriter{buf: b} }
func (w *byteWriter) Write(p []byte) (int, error) {
	*w.buf = append(*w.buf, p...)
	return len(p), nil
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

// AudioTranscriptionStream wraps AudioTranscription so the streaming
// gRPC endpoint (server.go:AudioTranscriptionStream) sees its channel
// close and the client doesn't sit waiting until deadline. vibevoice's
// ASR doesn't expose token-level streaming - vv_capi_asr decodes the
// whole audio and returns a JSON segment list - so we run the offline
// transcription, emit each segment's content as a delta, then close
// with a final_result whose Text equals the concatenated deltas (the
// e2e harness asserts those match).
func (v *VibevoiceCpp) AudioTranscriptionStream(req *pb.TranscriptRequest, results chan *pb.TranscriptStreamResponse) error {
	defer close(results)
	res, err := v.AudioTranscription(req)
	if err != nil {
		return err
	}
	var assembled strings.Builder
	for _, seg := range res.Segments {
		if seg == nil {
			continue
		}
		txt := strings.TrimSpace(seg.Text)
		if txt == "" {
			continue
		}
		delta := txt
		if assembled.Len() > 0 {
			delta = " " + txt
		}
		results <- &pb.TranscriptStreamResponse{Delta: delta}
		assembled.WriteString(delta)
	}
	final := pb.TranscriptResult{
		Segments: res.Segments,
		Duration: res.Duration,
		Language: res.Language,
		Text:     assembled.String(),
	}
	results <- &pb.TranscriptStreamResponse{FinalResult: &final}
	return nil
}
