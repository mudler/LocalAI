package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	gguf "github.com/gpustack/gguf-parser-go"
	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/utils"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	CppLoadModel       func(modelPath string, threads int, backendName string) int
	CppSetCodecPath    func(path string) int
	CppLoadModelVAD    func(modelPath string) int
	CppVAD             func(pcmf32 []float32, pcmf32Size uintptr, segsOut unsafe.Pointer, segsOutLen unsafe.Pointer) int
	CppTranscribe      func(threads uint32, lang string, translate bool, diarize bool, pcmf32 []float32, pcmf32Len uintptr, segsOutLen unsafe.Pointer, prompt string) int
	CppGetSegmentText  func(i int) string
	CppGetSegmentStart func(i int) int64
	CppGetSegmentEnd   func(i int) int64
	CppGetBackend      func() string
	CppSetAbort        func(v int)
	CppTTSSynthesize   func(text string, outNSamples unsafe.Pointer) uintptr
	CppTTSFree         func(ptr uintptr)
	CppTTSSetVoice     func(name string) int
	CppTTSSetVoiceFile func(path string, refText string) int

	// Word-level timestamp accessors (session-based, per-segment)
	CppGetWordCount func(segI int) int
	CppGetWordText  func(segI int, wordI int) string
	CppGetWordT0    func(segI int, wordI int) int64
	CppGetWordT1    func(segI int, wordI int) int64

	// Parakeet-specific word accessors (global, no segment index)
	CppGetParakeetWordCount func() int
	CppGetParakeetWordText  func(wordI int) string
	CppGetParakeetWordT0    func(wordI int) int64
	CppGetParakeetWordT1    func(wordI int) int64
)

type CrispASR struct {
	base.SingleThread
	// sampleRate is the output rate (Hz) of the loaded TTS engine's PCM, used to
	// write a correct WAV header. Most CrispASR TTS backends emit 24 kHz, but
	// piper returns its model's native rate (16 kHz for x_low/low voices,
	// 22.05 kHz for medium/high), so it is read from the GGUF metadata at Load.
	sampleRate int
}

// defaultTTSSampleRate is the output rate assumed for CrispASR TTS engines that
// don't advertise one in GGUF metadata (vibevoice/orpheus/chatterbox/qwen3-tts
// all emit 24 kHz). piper is the exception and carries piper.sample_rate.
const defaultTTSSampleRate = 24000

// piperSampleRate reads the piper.sample_rate metadata key from a GGUF model.
// CrispASR's piper backend returns PCM at the model's native rate without
// resampling, so the WAV header must match it. Returns ok=false for non-piper
// models (key absent) or an unreadable file, letting the caller fall back to
// defaultTTSSampleRate.
func piperSampleRate(modelPath string) (int, bool) {
	// Only scalar architecture keys are read, so skip the large array metadata
	// (phoneme map) and mmap the header - same rationale as pkg/vram's reader.
	f, err := gguf.ParseGGUFFile(modelPath, gguf.UseMMap(), gguf.SkipLargeMetadata())
	if err != nil {
		return 0, false
	}
	kv, ok := f.Header.MetadataKV.Get("piper.sample_rate")
	if !ok || kv.ValueType != gguf.GGUFMetadataValueTypeUint32 {
		return 0, false
	}
	rate := int(kv.ValueUint32())
	if rate <= 0 {
		return 0, false
	}
	return rate, true
}

// splitOption splits a "prefix:value" model option into its key and value,
// matching the convention used by other backends (see sherpa-onnx). It returns
// ok=false when the option carries no ':' separator.
func splitOption(oo string) (key, value string, ok bool) {
	parts := strings.SplitN(oo, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func (w *CrispASR) Load(opts *pb.ModelOptions) error {
	vadOnly := false
	backendName := ""
	codecPath := ""
	speakerName := ""
	voicePath := ""
	voiceRefText := ""

	for _, oo := range opts.Options {
		if oo == "vad_only" {
			vadOnly = true
			continue
		}
		switch key, value, ok := splitOption(oo); {
		case ok && key == "backend":
			backendName = value
		case ok && key == "codec":
			codecPath = value
		case ok && key == "speaker":
			speakerName = value
		case ok && key == "voice":
			voicePath = value
		case ok && key == "voice_text":
			voiceRefText = value
		default:
			fmt.Fprintf(os.Stderr, "Unrecognized option: %v\n", oo)
		}
	}

	if vadOnly {
		if ret := CppLoadModelVAD(opts.ModelFile); ret != 0 {
			return fmt.Errorf("Failed to load CrispASR VAD model")
		}

		return nil
	}

	// Resolve a relative companion path against the model directory so a config
	// can reference a sibling codec/tokenizer file by name alone.
	if codecPath != "" && !filepath.IsAbs(codecPath) {
		codecPath = filepath.Join(filepath.Dir(opts.ModelFile), codecPath)
	}

	// A voice file (.gguf pack or .wav prompt) is resolved against the model
	// directory just like the codec, so a config can reference a sibling file.
	if voicePath != "" && !filepath.IsAbs(voicePath) {
		voicePath = filepath.Join(filepath.Dir(opts.ModelFile), voicePath)
	}

	if ret := CppLoadModel(opts.ModelFile, int(opts.Threads), backendName); ret != 0 {
		return fmt.Errorf("Failed to load CrispASR transcription model")
	}

	// Determine the TTS output sample rate for the WAV header. piper voices
	// carry their native rate in GGUF metadata and CrispASR does not resample;
	// every other engine emits the 24 kHz default.
	w.sampleRate = defaultTTSSampleRate
	if rate, ok := piperSampleRate(opts.ModelFile); ok {
		w.sampleRate = rate
	}

	// Load the companion file (codec/tokenizer/s3gen) after the session is open.
	// rc==0 means success or "not applicable" for the active backend; only a
	// negative code is fatal.
	if codecPath != "" {
		if rc := CppSetCodecPath(codecPath); rc < 0 {
			return fmt.Errorf("crispasr: failed to load companion file %q (rc=%d)", codecPath, rc)
		}
		fmt.Fprintf(os.Stderr, "CrispASR companion file loaded: %s\n", codecPath)
	}

	// Apply the Load-time default voice. A baked speaker (speaker:) is selected
	// by name and is best-effort: a backend that can't honor it is logged, not
	// fatal. A voice file (voice:) is a hard requirement once configured, so a
	// negative rc fails Load.
	if speakerName != "" {
		if rc := CppTTSSetVoice(speakerName); rc != 0 {
			fmt.Fprintf(os.Stderr, "crispasr: speaker %q not applied (rc=%d)\n", speakerName, rc)
		}
	}
	if voicePath != "" {
		if rc := CppTTSSetVoiceFile(voicePath, voiceRefText); rc < 0 {
			return fmt.Errorf("crispasr: failed to load voice %q (rc=%d)", voicePath, rc)
		}
		fmt.Fprintf(os.Stderr, "CrispASR voice loaded: %s\n", voicePath)
	}

	fmt.Fprintf(os.Stderr, "CrispASR backend selected: %s\n", CppGetBackend())

	return nil
}

func (w *CrispASR) VAD(req *pb.VADRequest) (pb.VADResponse, error) {
	audio := req.Audio
	// We expect 0xdeadbeef to be overwritten and if we see it in a stack trace we know it wasn't
	segsPtr, segsLen := uintptr(0xdeadbeef), uintptr(0xdeadbeef)
	segsPtrPtr, segsLenPtr := unsafe.Pointer(&segsPtr), unsafe.Pointer(&segsLen)

	if ret := CppVAD(audio, uintptr(len(audio)), segsPtrPtr, segsLenPtr); ret != 0 {
		return pb.VADResponse{}, fmt.Errorf("Failed VAD")
	}

	// Happens when CPP vector has not had any elements pushed to it
	if segsPtr == 0 {
		return pb.VADResponse{
			Segments: []*pb.VADSegment{},
		}, nil
	}

	// unsafeptr warning is caused by segsPtr being on the stack and therefor being subject to stack copying AFAICT
	// however the stack shouldn't have grown between setting segsPtr and now, also the memory pointed to is allocated by C++
	segs := unsafe.Slice((*float32)(unsafe.Pointer(segsPtr)), segsLen) //nolint:govet // segsPtr addresses C++-owned heap memory passed back through the cgo-free purego boundary; the uintptr->Pointer round-trip is intentional and the buffer outlives this read.

	vadSegments := []*pb.VADSegment{}
	for i := range len(segs) >> 1 {
		s := segs[2*i] / 100
		t := segs[2*i+1] / 100
		vadSegments = append(vadSegments, &pb.VADSegment{
			Start: s,
			End:   t,
		})
	}

	return pb.VADResponse{
		Segments: vadSegments,
	}, nil
}

// isValidWord reports whether a TranscriptWord contains recognisable speech
// content. The parakeet-specific word accessors can return stale initialisation
// data (model name, binary blobs) when a segment has no real speech. A word is
// considered valid only when:
//   - the text is non-empty after trimming,
//   - it contains no U+FFFD replacement characters (from binary data scrubbing),
//   - both timestamps are non-negative,
//   - the word has positive duration (end > start).
func isValidWord(w *pb.TranscriptWord) bool {
	txt := strings.TrimSpace(w.Text)
	if txt == "" {
		return false
	}
	if strings.ContainsRune(txt, '\uFFFD') {
		return false
	}
	if w.Start < 0 || w.End < 0 || w.End <= w.Start {
		return false
	}
	return true
}

func (w *CrispASR) AudioTranscription(ctx context.Context, opts *pb.TranscriptRequest) (pb.TranscriptResult, error) {
	if err := ctx.Err(); err != nil {
		return pb.TranscriptResult{}, status.Error(codes.Canceled, "transcription cancelled")
	}

	dir, err := os.MkdirTemp("", "crispasr")
	if err != nil {
		return pb.TranscriptResult{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	convertedPath := filepath.Join(dir, "converted.wav")

	if err := utils.AudioToWav(opts.Dst, convertedPath); err != nil {
		return pb.TranscriptResult{}, err
	}

	fh, err := os.Open(convertedPath)
	if err != nil {
		return pb.TranscriptResult{}, err
	}
	defer func() { _ = fh.Close() }()

	d := wav.NewDecoder(fh)
	buf, err := d.FullPCMBuffer()
	if err != nil {
		return pb.TranscriptResult{}, err
	}

	data := buf.AsFloat32Buffer().Data
	var duration float32
	if buf.Format != nil && buf.Format.SampleRate > 0 {
		duration = float32(len(data)) / float32(buf.Format.SampleRate)
	}
	segsLen := uintptr(0xdeadbeef)
	segsLenPtr := unsafe.Pointer(&segsLen)

	// Watcher: flips the C-side abort flag when ctx is cancelled. The
	// goroutine is joined synchronously (close(done) signals it to exit,
	// wg.Wait() blocks until it has) so a late CppSetAbort(1) cannot fire
	// after the function returns and corrupt the next transcription call.
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case <-ctx.Done():
			CppSetAbort(1)
		case <-done:
		}
	}()
	defer func() {
		close(done)
		wg.Wait()
	}()

	ret := CppTranscribe(opts.Threads, opts.Language, opts.Translate, opts.Diarize, data, uintptr(len(data)), segsLenPtr, opts.Prompt)
	if ret == 2 {
		return pb.TranscriptResult{}, status.Error(codes.Canceled, "transcription cancelled")
	}
	if ret != 0 {
		return pb.TranscriptResult{}, fmt.Errorf("Failed Transcribe")
	}

	segments := []*pb.TranscriptSegment{}
	text := ""
	for i := range int(segsLen) {
		// segment start/end conversion factor taken from https://github.com/ggml-org/whisper.cpp/blob/master/examples/cli/cli.cpp#L895
		s := CppGetSegmentStart(i) * (10000000)
		t := CppGetSegmentEnd(i) * (10000000)
		// The session result can emit bytes that aren't valid UTF-8 (e.g. a
		// multibyte codepoint split across token boundaries); protobuf string
		// fields reject those at marshal time. Scrub before the value escapes
		// cgo. The session result is segment+word based and exposes no token
		// IDs, so Tokens is left empty.
		txt := strings.ToValidUTF8(strings.Clone(CppGetSegmentText(i)), "�")

		// Populate word-level timestamps. Try session-based functions first
		// (per-segment); fall back to parakeet-specific functions (global word
		// list with no segment index — only populated on the first segment to
		// avoid duplication).
		words := []*pb.TranscriptWord{}
		wordCount := CppGetWordCount(i)
		if wordCount == 0 && i == 0 {
			wordCount = CppGetParakeetWordCount()
			for j := 0; j < wordCount; j++ {
				w := &pb.TranscriptWord{
					Start: CppGetParakeetWordT0(j) * (10000000),
					End:   CppGetParakeetWordT1(j) * (10000000),
					Text:  strings.ToValidUTF8(strings.Clone(CppGetParakeetWordText(j)), "�"),
				}
				if isValidWord(w) {
					words = append(words, w)
				}
			}
		} else {
			for j := 0; j < wordCount; j++ {
				w := &pb.TranscriptWord{
					Start: CppGetWordT0(i, j) * (10000000),
					End:   CppGetWordT1(i, j) * (10000000),
					Text:  strings.ToValidUTF8(strings.Clone(CppGetWordText(i, j)), "�"),
				}
				if isValidWord(w) {
					words = append(words, w)
				}
			}
		}

		// Skip empty segments with no recognisable content (e.g. trailing
		// silence segments that parakeet emits with stale init data).
		trimmed := strings.TrimSpace(txt)
		if trimmed == "" && len(words) == 0 {
			continue
		}

		segment := &pb.TranscriptSegment{
			Id:    int32(i),
			Text:  txt,
			Start: s, End: t,
			Words: words,
		}

		segments = append(segments, segment)

		text += " " + trimmed
	}

	return pb.TranscriptResult{
		Segments: segments,
		Text:     strings.TrimSpace(text),
		Language: opts.Language,
		Duration: duration,
	}, nil
}

// AudioTranscriptionStream runs the session transcribe to completion and then
// emits one delta per non-empty segment, followed by a final TranscriptResult.
// Progressive/real-time streaming isn't available via the session API (there
// is no per-decode callback), so deltas are emitted per-segment after the
// blocking decode returns rather than as segments are produced. The offline
// AudioTranscription is unchanged; both paths share the session and the
// SingleThread concurrency model.
func (w *CrispASR) AudioTranscriptionStream(ctx context.Context, opts *pb.TranscriptRequest, results chan *pb.TranscriptStreamResponse) error {
	defer close(results)

	if err := ctx.Err(); err != nil {
		return status.Error(codes.Canceled, "transcription cancelled")
	}

	dir, err := os.MkdirTemp("", "crispasr")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	convertedPath := filepath.Join(dir, "converted.wav")
	if err := utils.AudioToWav(opts.Dst, convertedPath); err != nil {
		return err
	}

	fh, err := os.Open(convertedPath)
	if err != nil {
		return err
	}
	defer func() { _ = fh.Close() }()

	d := wav.NewDecoder(fh)
	buf, err := d.FullPCMBuffer()
	if err != nil {
		return err
	}
	data := buf.AsFloat32Buffer().Data
	var duration float32
	if buf.Format != nil && buf.Format.SampleRate > 0 {
		duration = float32(len(data)) / float32(buf.Format.SampleRate)
	}

	// Same abort-watcher pattern as AudioTranscription. Joined synchronously
	// so a late CppSetAbort(1) cannot fire after this function returns.
	// Best-effort only: the session transcribe is blocking with no abort hook.
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case <-ctx.Done():
			CppSetAbort(1)
		case <-done:
		}
	}()
	defer func() {
		close(done)
		wg.Wait()
	}()

	segsLen := uintptr(0xdeadbeef)
	segsLenPtr := unsafe.Pointer(&segsLen)
	ret := CppTranscribe(opts.Threads, opts.Language, opts.Translate, opts.Diarize, data, uintptr(len(data)), segsLenPtr, opts.Prompt)
	if ret == 2 {
		return status.Error(codes.Canceled, "transcription cancelled")
	}
	if ret != 0 {
		return fmt.Errorf("Failed Transcribe")
	}

	// Walk the segments once: emit a delta per non-empty segment and build the
	// final TranscriptResult.Segments alongside. The first delta has no leading
	// space and subsequent ones are prefixed with a single space, so
	// concat(deltas) == final.Text exactly, matching the e2e contract.
	segments := []*pb.TranscriptSegment{}
	var assembled strings.Builder
	for i := range int(segsLen) {
		s := CppGetSegmentStart(i) * 10000000
		t := CppGetSegmentEnd(i) * 10000000
		txt := strings.ToValidUTF8(strings.Clone(CppGetSegmentText(i)), "�")

		// Skip empty segments (e.g. trailing silence that parakeet emits
		// with stale init data).
		trimmed := strings.TrimSpace(txt)
		if trimmed == "" && s == t {
			continue
		}

		segments = append(segments, &pb.TranscriptSegment{
			Id:    int32(i),
			Text:  txt,
			Start: s, End: t,
		})

		if trimmed == "" {
			continue
		}
		var delta string
		if assembled.Len() == 0 {
			delta = trimmed
		} else {
			delta = " " + trimmed
		}
		results <- &pb.TranscriptStreamResponse{Delta: delta}
		assembled.WriteString(delta)
	}

	final := &pb.TranscriptResult{
		Segments: segments,
		Text:     assembled.String(),
		Language: opts.Language,
		Duration: duration,
	}
	results <- &pb.TranscriptStreamResponse{FinalResult: final}
	return nil
}

// synthesize returns 24 kHz mono float32 PCM for text via the open session.
func (w *CrispASR) synthesize(text string) ([]float32, error) {
	if text == "" {
		return nil, fmt.Errorf("crispasr: TTS requires non-empty text")
	}
	var n int32
	ptr := CppTTSSynthesize(text, unsafe.Pointer(&n))
	if ptr == 0 || n <= 0 {
		return nil, fmt.Errorf("crispasr: synthesis failed (the loaded model may not be a supported TTS backend, or needs extra config e.g. orpheus SNAC codec)")
	}
	defer CppTTSFree(ptr)
	src := unsafe.Slice((*float32)(unsafe.Pointer(ptr)), int(n)) //nolint:govet // ptr addresses C-allocated PCM returned across the purego boundary; copied out immediately below, before tts_free.
	out := make([]float32, int(n))                               // copy out of C memory before free
	copy(out, src)
	return out, nil
}

// setVoice applies a per-call speaker/voice override (best effort). CrispASR
// returns a negative code when the active backend can't honor the name; we log
// it rather than fail, so an unknown voice falls back to the default speaker.
func setVoice(voice string) {
	v := strings.TrimSpace(voice)
	if v == "" {
		return
	}
	if rc := CppTTSSetVoice(v); rc != 0 {
		fmt.Fprintf(os.Stderr, "crispasr: voice %q not applied by the active TTS backend (rc=%d); using default\n", v, rc)
	}
}

func (w *CrispASR) TTS(req *pb.TTSRequest) error {
	if req.Dst == "" {
		return fmt.Errorf("crispasr: TTS requires a destination path")
	}
	setVoice(req.Voice)
	pcm, err := w.synthesize(req.Text)
	if err != nil {
		return err
	}
	return writeWAV(req.Dst, pcm, w.sampleRate)
}

// TTSStream is the streaming counterpart to TTS. CrispASR has no progressive
// (native streaming) synth, so we synthesize the whole utterance, encode it to
// a 24 kHz WAV, and emit the encoded bytes as a single chunk. The gRPC server
// wrapper (pkg/grpc/server.go:TTSStream) ranges over the channel until it is
// closed, so this method owns the close - mirrors vibevoice-cpp's TTSStream.
func (w *CrispASR) TTSStream(req *pb.TTSRequest, results chan []byte) error {
	defer close(results)

	if req.Text == "" {
		return fmt.Errorf("crispasr: TTSStream requires text")
	}
	setVoice(req.Voice)
	pcm, err := w.synthesize(req.Text)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp("", "crispasr-tts-stream-*.wav")
	if err != nil {
		return fmt.Errorf("crispasr: tempfile: %w", err)
	}
	dst := tmp.Name()
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("crispasr: close tempfile: %w", err)
	}
	defer func() { _ = os.Remove(dst) }()

	if err := writeWAV(dst, pcm, w.sampleRate); err != nil {
		return err
	}

	encoded, err := os.ReadFile(dst)
	if err != nil {
		return fmt.Errorf("crispasr: read tempfile: %w", err)
	}
	results <- encoded
	return nil
}

// writeWAV writes pcm as a sampleRate Hz, mono, 16-bit PCM WAV at dst.
func writeWAV(dst string, pcm []float32, sampleRate int) error {
	f, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("crispasr: create %q: %w", dst, err)
	}

	enc := wav.NewEncoder(f, sampleRate, 16, 1, 1)
	ints := make([]int, len(pcm))
	for i, s := range pcm {
		if s > 1 {
			s = 1
		} else if s < -1 {
			s = -1
		}
		ints[i] = int(s * 32767)
	}
	buf := &audio.IntBuffer{
		Format:         &audio.Format{NumChannels: 1, SampleRate: sampleRate},
		Data:           ints,
		SourceBitDepth: 16,
	}
	if err := enc.Write(buf); err != nil {
		_ = enc.Close()
		_ = f.Close()
		return fmt.Errorf("crispasr: encode WAV: %w", err)
	}
	if err := enc.Close(); err != nil {
		_ = f.Close()
		return fmt.Errorf("crispasr: finalize WAV: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("crispasr: close %q: %w", dst, err)
	}
	return nil
}
