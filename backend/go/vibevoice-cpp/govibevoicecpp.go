package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"github.com/ebitengine/purego"
	laudio "github.com/mudler/LocalAI/pkg/audio"
	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// vv_capi_asr loads audio with load_wav_24k_mono — a 24 kHz mono s16le
// WAV is the format the model was trained on. Inputs already in that
// format pass through; everything else is converted via ffmpeg, which
// is therefore a runtime requirement only when callers upload non-WAV
// (or non-24 kHz mono s16le WAV) audio. Skipping ffmpeg on the happy
// path matters for the e2e-backends test container, which does not
// ship ffmpeg but feeds the backend pre-cooked 24 kHz mono WAVs.
const vibevoiceASRSampleRate = 24000

// prepareWavInput resolves `src` to a 24 kHz mono s16le WAV path that
// vv_capi_asr's load_wav_24k_mono accepts. Returns the resolved path
// plus a cleanup func; both must be honoured by the caller.
//
// Pass-through happens when `src` already has the right WAV format —
// no ffmpeg required. Otherwise we shell out to ffmpeg into a temp
// dir; if ffmpeg isn't on PATH we surface a clear error mentioning the
// underlying format mismatch.
func prepareWavInput(src string) (string, func(), error) {
	if src == "" {
		return "", func() {}, fmt.Errorf("empty audio path")
	}
	if isVibevoiceCompatibleWav(src) {
		return src, func() {}, nil
	}

	dir, err := os.MkdirTemp("", "vibevoice-asr")
	if err != nil {
		return "", func() {}, fmt.Errorf("mkdtemp: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	wavPath := filepath.Join(dir, "input.wav")

	// -y: overwrite, -ar 24000: target sample rate, -ac 1: mono,
	// -acodec pcm_s16le: signed 16-bit little-endian PCM (load_wav_24k_mono
	// only accepts s16le).
	cmd := exec.Command("ffmpeg",
		"-y", "-i", src,
		"-ar", fmt.Sprintf("%d", vibevoiceASRSampleRate),
		"-ac", "1",
		"-acodec", "pcm_s16le",
		wavPath,
	)
	cmd.Env = []string{}
	if out, err := cmd.CombinedOutput(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("ffmpeg convert to 24k mono wav: %w (output: %s)", err, string(out))
	}
	return wavPath, cleanup, nil
}

// isVibevoiceCompatibleWav returns true when `src` carries the RIFF/WAVE
// magic bytes. vibevoice's load_wav_24k_mono uses drwav under the hood,
// which accepts any PCM/IEEE-float WAV at any sample rate and downmixes
// multi-channel input to mono on its own — so any valid WAV passes
// through to the C side without conversion. Anything else (MP3, OGG,
// FLAC, ...) needs ffmpeg.
func isVibevoiceCompatibleWav(src string) bool {
	f, err := os.Open(src)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	// 0..3 = "RIFF", 8..11 = "WAVE".
	var hdr [12]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return false
	}
	return string(hdr[0:4]) == "RIFF" && string(hdr[8:12]) == "WAVE"
}

// asrMaxNewTokens caps the ASR generation budget. The C ABI defaults to
// 256 when 0 is passed — far too small for anything past ~10s of speech.
// Vibevoice generates ~30 tokens per second of audio, so 16 384 covers
// roughly 9 minutes of dialogue, well past any normal /v1/audio/diarization
// upload. Going higher costs little since generation stops at EOS.
const asrMaxNewTokens = 16384

// vibevoice.cpp synthesizes 24 kHz mono 16-bit PCM. Hardcoded - the
// model itself is fixed-rate; if the upstream ever changes this we'll
// pick it up via vv_capi_version().
const vibevoiceSampleRate = uint32(24000)

// purego-bound entry points from libgovibevoicecpp.
//
// vv_capi_tts takes a `const char* const* ref_audio_paths` array (used
// by the 1.5B variant for runtime voice cloning; the realtime-0.5B
// path leaves it NULL and uses voice_path instead). purego marshals a
// Go []*byte slice as **char by passing the underlying array's address.
// A nil/empty slice marshals to NULL, which matches the C contract for
// "no reference audio".
var (
	CppLoad func(ttsModel, asrModel, tokenizer, voice string, threads int32) int32
	CppTTS  func(text, voicePath string,
		refAudioPaths []*byte, nRefAudioPaths int32,
		dstWav string,
		nSteps int32, cfgScale float32, maxSpeechFrames int32, seed uint32) int32
	// CppTTSStream drives vv_capi_tts_stream: it synthesizes `text` and
	// invokes the C callback `cb` once per decoded PCM window instead of
	// writing a file. `cb` is the address of a purego callback (see
	// streamCB); `user` is an opaque pointer handed back to every
	// callback invocation - we route via the package-level activeStream
	// instead, so it is always nil here.
	CppTTSStream func(text, voicePath string,
		nSteps int32, cfgScale float32, maxFrames int32, seed uint32,
		cb uintptr, user unsafe.Pointer) int32
	CppASR func(srcWav string, outJSON []byte, capacity uint64,
		maxNewTokens int32) int32
	CppUnload  func()
	CppVersion func() string
)

// streamState carries the destination channel for one in-flight
// TTSStream call. vibevoice's engine is a single process-global, and
// backend calls are serialized through base.SingleThread, so a single
// package-level pointer is safe: only one TTSStream runs at a time.
type streamState struct {
	results chan []byte
}

// activeStream points at the streamState for the currently-running
// TTSStream. The C callback (streamCB) and the deliverPCMForTest hook
// read it to find the channel. Guarded by base.SingleThread
// serialization; TTSStream sets it and clears it in a defer.
var activeStream *streamState

// pushPCM copies a transient int16 PCM window into a fresh little-endian
// []byte and pushes it onto the active stream. The C buffer handed to
// the callback is only valid for the duration of the call, so we must
// copy before returning. A nil/empty input or a missing active stream
// is a no-op.
func pushPCM(pcm []int16) {
	s := activeStream
	if s == nil || len(pcm) == 0 {
		return
	}
	buf := make([]byte, len(pcm)*2)
	for i, v := range pcm {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(v))
	}
	s.results <- buf
}

// streamCB is the ONE reusable purego callback bound to the C ABI's
// vv_pcm_cb. purego cannot free callbacks and enforces a process-global
// limit, so we create exactly one at package init and reuse it for every
// TTSStream call - the per-call state lives in activeStream, not here.
// purego marshals the C `const int16_t*` first argument straight into a
// Go *int16, so we can unsafe.Slice it without a uintptr round-trip
// (keeps go vet clean); pushPCM copies the transient buffer out and
// returns 0 to keep synthesizing.
var streamCB = purego.NewCallback(func(samples *int16, n int32, _ uintptr) uintptr {
	if activeStream == nil || samples == nil || n <= 0 {
		return 0
	}
	pcm := unsafe.Slice(samples, int(n))
	pushPCM(pcm)
	return 0
})

// deliverPCMForTest exercises the exact copy-and-push path streamCB runs
// against activeStream, but from a Go []int16 - so unit tests can
// validate the callback -> channel framing without the C library.
func deliverPCMForTest(samples []int16) {
	pushPCM(samples)
}

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

	// refAudio is the load-time default list of reference WAVs used by
	// the 1.5B model (one per speaker). Sourced from
	// ModelOptions.AudioPath (config_file's `audio_path:`) — comma-
	// separated for multi-speaker. Per-call TTSRequest.Voice can
	// override it. Empty for the realtime-0.5B path, which conditions
	// on a pre-baked voice gguf via `voice` instead.
	refAudio []string
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

// parseRefAudio splits a comma-separated audio_path value into a
// resolved list of WAVs. The 1.5B model uses one WAV per speaker;
// callers that only need a single reference set audio_path to a single
// path. Empty / whitespace-only entries are skipped.
func parseRefAudio(audioPath, relTo string) []string {
	if audioPath == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(audioPath, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, resolvePath(p, relTo))
	}
	return out
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

	// 1.5B reference WAVs ride on ModelOptions.AudioPath (config_file's
	// `audio_path:` key) — same convention other audio backends already
	// follow. Single-speaker = single path; multi-speaker = comma list,
	// one WAV per Speaker N: tag in TTSRequest.text.
	v.refAudio = parseRefAudio(opts.AudioPath, v.modelRoot)

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
		"[vibevoice-cpp] Loading: tts=%q asr=%q tokenizer=%q voice=%q ref_audio=%v threads=%d\n",
		v.ttsModel, v.asrModel, v.tokenizer, v.voice, v.refAudio, threads)

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

	// TTSRequest.Voice carries the per-call override. Routing depends
	// on the loaded model variant:
	//   * realtime-0.5B → expects a baked voice .gguf (single path).
	//   * 1.5B          → expects one or more raw 24 kHz mono .wav
	//                     reference clips for runtime voice cloning;
	//                     comma-separated to address multi-speaker
	//                     dialogs (Speaker 0..n-1 follow the order).
	// We pick the branch by extension / shape of the override; if no
	// override is given, fall back to the load-time defaults.
	voice := ""
	var refAudio []string
	if reqVoice := strings.TrimSpace(req.Voice); reqVoice != "" {
		if isRefAudioOverride(reqVoice) {
			for _, p := range strings.Split(reqVoice, ",") {
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}
				refAudio = append(refAudio, resolvePath(p, v.modelRoot))
			}
		} else {
			voice = resolvePath(reqVoice, v.modelRoot)
		}
	} else {
		// No per-call override. v.voice already went to vv_capi_load
		// for realtime-0.5B; ref_audio is per-call only on the C ABI,
		// so the gallery's `ref_audio:` defaults are re-passed here.
		refAudio = append(refAudio, v.refAudio...)
	}

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

	refPtrs, refKeep := newCStringArray(refAudio)
	rc := CppTTS(text, voice, refPtrs, int32(len(refPtrs)), dst,
		int32(defaultSteps), defaultCfg, int32(defaultMaxFrames), 0)
	// Hold the backing buffers past the cgo call. purego marshals
	// []*byte by handing the C side the underlying array address; the
	// pointed-to NUL-terminated bytes must outlive the call.
	runtime.KeepAlive(refKeep)
	runtime.KeepAlive(refPtrs)
	if rc != 0 {
		return fmt.Errorf("vibevoice-cpp: vv_capi_tts failed (rc=%d)", rc)
	}
	return nil
}

// isRefAudioOverride decides whether a TTSRequest.Voice override should
// be routed to ref_audio_paths (1.5B path) instead of voice_path
// (realtime-0.5B). Either a comma-separated list (multi-speaker) or a
// single .wav clip qualifies; a bare voice .gguf falls through.
func isRefAudioOverride(s string) bool {
	if strings.Contains(s, ",") {
		return true
	}
	return strings.HasSuffix(strings.ToLower(s), ".wav")
}

// newCStringArray builds the **char array vv_capi_tts expects, plus the
// keep-alive slice the caller must runtime.KeepAlive across the cgo
// call. A nil/empty input returns (nil, nil) which purego marshals to
// the C NULL pointer.
func newCStringArray(in []string) ([]*byte, [][]byte) {
	if len(in) == 0 {
		return nil, nil
	}
	keep := make([][]byte, len(in))
	ptrs := make([]*byte, len(in))
	for i, s := range in {
		b := make([]byte, len(s)+1)
		copy(b, s)
		keep[i] = b
		ptrs[i] = &b[0]
	}
	return ptrs, keep
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

// TTSStream is the streaming counterpart to TTS. It drives
// vv_capi_tts_stream, which synthesizes `text` and invokes our C
// callback (streamCB) once per decoded PCM window instead of writing a
// file - so the client starts receiving audio while the model is still
// generating. We first emit a streaming-WAV header, install the results
// channel as the active stream, then let the callback push each PCM
// window (copied to little-endian bytes) onto that channel. The gRPC
// server wrapper (pkg/grpc/server.go:TTSStream) blocks on the channel
// until this method closes it, so `defer close(results)` is mandatory
// even on the error paths.
func (v *VibevoiceCpp) TTSStream(req *pb.TTSRequest, results chan []byte) error {
	defer close(results)
	if v.ttsModel == "" {
		return fmt.Errorf("vibevoice-cpp: TTSStream requested but no realtime model was loaded")
	}
	if req.Text == "" {
		return fmt.Errorf("vibevoice-cpp: TTSStream requires text")
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

	// vv_capi_tts_stream takes a single voice_path (realtime-0.5B path);
	// unlike vv_capi_tts it has no ref_audio array. Resolve the per-call
	// override when it names a voice gguf, otherwise fall back to the
	// load-time default that already went to vv_capi_load.
	voice := v.voice
	if reqVoice := strings.TrimSpace(req.Voice); reqVoice != "" && !isRefAudioOverride(reqVoice) {
		voice = resolvePath(reqVoice, v.modelRoot)
	}

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

	// Serialized by base.SingleThread, so a single package-level
	// activeStream is race-free: exactly one TTSStream runs at a time.
	// The callback reads it to find `results`; clear it on the way out.
	activeStream = &streamState{results: results}
	defer func() { activeStream = nil }()

	rc := CppTTSStream(req.Text, voice,
		int32(defaultSteps), defaultCfg, int32(defaultMaxFrames), 0,
		streamCB, nil)
	if rc != 0 {
		return fmt.Errorf("vibevoice-cpp: vv_capi_tts_stream failed (rc=%d)", rc)
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

func (v *VibevoiceCpp) AudioTranscription(_ context.Context, req *pb.TranscriptRequest) (pb.TranscriptResult, error) {
	if v.asrModel == "" {
		return pb.TranscriptResult{}, fmt.Errorf("vibevoice-cpp: AudioTranscription requested but no ASR model was loaded")
	}
	if req.Dst == "" {
		return pb.TranscriptResult{}, fmt.Errorf("vibevoice-cpp: TranscriptRequest.dst (audio path) is required")
	}

	wavPath, cleanup, err := prepareWavInput(req.Dst)
	if err != nil {
		return pb.TranscriptResult{}, fmt.Errorf("vibevoice-cpp: %w", err)
	}
	defer cleanup()

	out, err := v.callASR(wavPath, asrMaxNewTokens)
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

// Diarize runs vibevoice's ASR and projects the speaker-labelled segment
// list it returns natively. vibevoice.cpp's ASR prompt asks the model to
// emit `[{"Start":..,"End":..,"Speaker":..,"Content":..}]`, so diarization
// is a by-product of the same pass — we reuse callASR and re-shape.
//
// Speaker hints (num_speakers/min/max/threshold) and min_duration_on/off are
// not actionable here: vibevoice's model picks the speaker count itself and
// has no clustering knob. The HTTP layer documents this; we accept the
// fields for API symmetry and ignore them.
func (v *VibevoiceCpp) Diarize(req *pb.DiarizeRequest) (pb.DiarizeResponse, error) {
	if v.asrModel == "" {
		return pb.DiarizeResponse{}, fmt.Errorf("vibevoice-cpp: Diarize requires an ASR model (load options: type=asr)")
	}
	if req.Dst == "" {
		return pb.DiarizeResponse{}, fmt.Errorf("vibevoice-cpp: DiarizeRequest.dst (audio path) is required")
	}

	wavPath, cleanup, err := prepareWavInput(req.Dst)
	if err != nil {
		return pb.DiarizeResponse{}, fmt.Errorf("vibevoice-cpp: %w", err)
	}
	defer cleanup()

	out, err := v.callASR(wavPath, asrMaxNewTokens)
	if err != nil {
		return pb.DiarizeResponse{}, err
	}
	if out == "" {
		return pb.DiarizeResponse{}, nil
	}

	var segs []asrSegment
	if err := json.Unmarshal([]byte(out), &segs); err != nil {
		// Mirror AudioTranscription's fallback: vibevoice's ASR sometimes
		// emits free-form text instead of JSON for short or unusual audio.
		// Surface a single unknown-speaker segment carrying the full text
		// (when include_text is set) so the caller still gets coverage of
		// the whole clip rather than a hard failure.
		fmt.Fprintf(os.Stderr,
			"[vibevoice-cpp] WARNING: vv_capi_asr returned non-JSON for diarization, falling back to single segment: %v\n", err)
		text := strings.TrimSpace(out)
		seg := &pb.DiarizeSegment{Id: 0, Speaker: "0"}
		if req.IncludeText {
			seg.Text = text
		}
		return pb.DiarizeResponse{
			Segments:    []*pb.DiarizeSegment{seg},
			NumSpeakers: 1,
		}, nil
	}

	speakers := make(map[int]struct{})
	segments := make([]*pb.DiarizeSegment, 0, len(segs))
	var duration float32
	for i, s := range segs {
		ds := &pb.DiarizeSegment{
			Id:      int32(i),
			Start:   float32(s.Start),
			End:     float32(s.End),
			Speaker: fmt.Sprintf("%d", s.Speaker),
		}
		if req.IncludeText {
			ds.Text = strings.TrimSpace(s.Content)
		}
		segments = append(segments, ds)
		speakers[s.Speaker] = struct{}{}
		if float32(s.End) > duration {
			duration = float32(s.End)
		}
	}
	return pb.DiarizeResponse{
		Segments:    segments,
		NumSpeakers: int32(len(speakers)),
		Duration:    duration,
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
func (v *VibevoiceCpp) AudioTranscriptionStream(ctx context.Context, req *pb.TranscriptRequest, results chan *pb.TranscriptStreamResponse) error {
	defer close(results)
	res, err := v.AudioTranscription(ctx, req)
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
