package main

import (
	"context"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unsafe"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// asrWord is one decoded word with its millisecond offsets and 1-based speaker
// tag (0 when diarization was not requested).
type asrWord struct {
	Text    string
	Start   int32
	End     int32
	Speaker int32
}

// pinPtr pins v for the lifetime of p and returns its address in the uintptr
// form the config structs carry.
//
// The config structs mirror C, so their pointer members are uintptr, which the
// collector does not trace. Everything reachable only through one of them is
// therefore invisible to the GC while C is reading it, exactly as described on
// cstr, and needs the same pin. runtime.KeepAlive would cover collection but
// says nothing about relocation, and the guarantee wanted here is that the
// address C holds stays the address of the object.
func pinPtr[T any](p *runtime.Pinner, v *T) uintptr {
	p.Pin(v)
	// #nosec G103 -- v is pinned into p on the previous line, so its address is
	// stable and traced for as long as p lives; every caller defers p.Unpin only
	// after the create call that reads it. One-way, like cstr: nothing converts
	// this uintptr back to a pointer.
	return uintptr(unsafe.Pointer(v))
}

// asrDiarConfig builds the config for the diarizer attached to a recognizer.
//
// Extracted from loadASR for the same reason diarModelConfig was extracted from
// loadDiarizer: the six frame counts are sentinel-sensitive and invisible to
// every other check in the tree. src/asr/c_api.cpp:151-165 applies five of them
// when they are > 0 but applies left_context_frames when it is >= 0, so a
// dropped -1 does not fall back to the model's own streaming geometry, it pins
// the left context to zero. The struct is the right shape either way, so the
// layout assertions in abi_test.go cannot see it and only a spec on this builder
// can.
//
// diarGeometryDefault is shared with the standalone diarizer rather than
// restated: it is the same sentinel, from the same rule, in the same runtime.
//
// modelPath is a C pointer from cstr, not a Go string, and the caller owns its
// release.
func asrDiarConfig(modelPath uintptr) cASRDiarConfig {
	return cASRDiarConfig{
		Size:               unsafe.Sizeof(cASRDiarConfig{}),
		ModelPath:          modelPath,
		ChunkFrames:        diarGeometryDefault,
		RightContextFrames: diarGeometryDefault,
		LeftContextFrames:  diarGeometryDefault,
		FIFOFrames:         diarGeometryDefault,
		SpkcacheFrames:     diarGeometryDefault,
		UpdatePeriodFrames: diarGeometryDefault,
	}
}

// loadASR creates the recognizer, attaching VAD, PnC, ITN and diarization when
// the corresponding options were set.
//
// Every field below is assigned by name against include/nemo_speech/asr.h. The
// sub-configs are optional pointers: a nil one means "library defaults", which
// is why each is populated only when its option was given rather than always
// being attached with empty strings.
//
// Each struct's Size is load-bearing, not decoration. The runtime decides a
// field is present with HAS_FIELD (src/asr/c_api.cpp), which tests the caller's
// size against offsetof(field) + sizeof(field), so a config sent with Size 0
// has every field ignored and the model silently loads with defaults.
//
// This must not take engineMu: Load is its only caller and already holds it.
func (n *NemoSpeech) loadASR(modelFile string) error {
	// nemo_speech_asr_create deep-copies every const char* into a std::string
	// (src/asr/c_api.cpp to_config, via str_or_empty) and retains no pointer
	// afterwards, so pinning for the duration of the create call is both
	// necessary and sufficient.
	var pinner runtime.Pinner
	defer pinner.Unpin()

	pathP, freePath := cstr(modelFile)
	defer freePath()

	model := cASRModelConfig{Size: unsafe.Sizeof(cASRModelConfig{}), Path: pathP}
	backend := cASRBackendConfig{Size: unsafe.Sizeof(cASRBackendConfig{}), GPU: n.opts.gpu}

	cfg := cASRRecognizerConfig{
		Size:    unsafe.Sizeof(cASRRecognizerConfig{}),
		Backend: pinPtr(&pinner, &backend),
		Model:   pinPtr(&pinner, &model),
	}

	var vad cASRVADConfig
	if n.opts.vadModel != "" {
		p, free := cstr(n.opts.vadModel)
		defer free()
		vad = cASRVADConfig{Size: unsafe.Sizeof(cASRVADConfig{}), ModelPath: p}
		cfg.VAD = pinPtr(&pinner, &vad)
	}

	var postproc cASRPostprocConfig
	if n.opts.itnDir != "" || n.opts.pncModel != "" {
		itnP, freeITN := cstr(n.opts.itnDir)
		defer freeITN()
		pncP, freePNC := cstr(n.opts.pncModel)
		defer freePNC()
		postproc = cASRPostprocConfig{
			Size:         unsafe.Sizeof(cASRPostprocConfig{}),
			ITNModelDir:  itnP,
			PNCModelPath: pncP,
		}
		cfg.Postproc = pinPtr(&pinner, &postproc)
	}

	var diar cASRDiarConfig
	if n.opts.diarModel != "" {
		p, free := cstr(n.opts.diarModel)
		defer free()
		diar = asrDiarConfig(p)
		cfg.Diar = pinPtr(&pinner, &diar)
	}

	xlog.Info("nemo-speech-cpp: creating recognizer",
		"gpu", n.opts.gpu,
		"vad", n.opts.vadModel != "",
		"pnc", n.opts.pncModel != "",
		"itn", n.opts.itnDir != "",
		"diarization", n.opts.diarModel != "")

	// #nosec G103 -- cfg is a local POD struct passed as a pointer for the
	// duration of this call only; every uintptr member it carries is either a
	// cstr allocation or a pinPtr address, all pinned above and released by the
	// defers, and nemo_speech_asr_create deep-copies and retains nothing.
	if st := ASRCreate(unsafe.Pointer(&cfg), &n.recognizer); st != 0 {
		return statusErrorf(st, "nemo-speech-cpp: asr create: %s", ASRLastError())
	}
	return nil
}

// recognizeF32 runs one offline decode and returns the result handle, which the
// caller must destroy.
//
// The empty-input guard is here rather than at the call site because &pcm[0]
// panics on a zero-length slice: Go never reaches the C side's own "empty
// audio" rejection. A silent clip or a truncated upload decodes to zero
// samples, which is ordinary input, not an exotic one.
//
// The caller must hold engineMu.
func recognizeF32(recognizer uintptr, opts *cASRRecognitionOptions, pcm []float32, sampleRate int32) (uintptr, error) {
	if len(pcm) == 0 {
		return 0, status.Error(codes.InvalidArgument, "nemo-speech-cpp: empty audio")
	}

	var result uintptr
	// #nosec G103 -- opts is the caller's live struct, borrowed for this call
	// only; its LanguageCode is a cstr allocation the caller keeps pinned across
	// it. &pcm[0] is guarded by the empty check above and the length handed over
	// is exactly len(pcm), so the runtime cannot read past the slice.
	if st := ASRRecognizeF32(recognizer, unsafe.Pointer(opts),
		&pcm[0], uint64(len(pcm)), sampleRate, &result); st != 0 {
		return 0, statusErrorf(st, "nemo-speech-cpp: recognize: %s", ASRLastError())
	}
	return result, nil
}

// msToNanos converts a runtime word offset to the wire unit. The runtime
// reports milliseconds (src/asr/types.h); TranscriptSegment.start/end and
// TranscriptWord.start/end are int64 nanoseconds, which core/backend reads
// straight into a time.Duration.
func msToNanos(ms int32) int64 {
	return int64(ms) * int64(time.Millisecond)
}

// extractWords pulls the top alternative's words out of a result handle.
func extractWords(result uintptr) []asrWord {
	if ASRResultAlternativeCount(result) == 0 {
		return nil
	}
	count := ASRResultWordCount(result, 0)
	words := make([]asrWord, 0, count)
	for i := uint64(0); i < count; i++ {
		words = append(words, asrWord{
			Text:    ASRResultWordText(result, 0, i),
			Start:   ASRResultWordStartTime(result, 0, i),
			End:     ASRResultWordEndTime(result, 0, i),
			Speaker: ASRResultWordSpeakerTag(result, 0, i),
		})
	}
	return words
}

// wordsRequested reports whether the caller asked for word-level timestamps.
// The OpenAI transcription API gates word timings behind
// timestamp_granularities[] containing "word" and defaults to segment level
// otherwise; every backend here follows that contract (see
// backend/go/parakeet-cpp).
func wordsRequested(granularities []string) bool {
	for _, g := range granularities {
		if strings.EqualFold(strings.TrimSpace(g), "word") {
			return true
		}
	}
	return false
}

// wordsToSegments groups words into one segment per consecutive speaker run.
// Without diarization every word carries speaker 0, so this collapses to a
// single segment.
//
// The boundary is a CHANGE of speaker, not the first appearance of one: a
// conversation that returns to an earlier speaker has to start a new turn
// rather than reopen the old one.
//
// withWords additionally attaches the per-word timings that
// core/backend/transcript.go turns into the response's word list. It is off by
// default because the OpenAI contract asks for word timestamps explicitly, and
// a long transcript pays for every word twice otherwise.
func wordsToSegments(words []asrWord, withWords bool) []*pb.TranscriptSegment {
	if len(words) == 0 {
		return nil
	}

	var segs []*pb.TranscriptSegment
	start := 0
	flush := func(end int) {
		run := words[start:end]
		texts := make([]string, 0, len(run))
		for _, w := range run {
			texts = append(texts, w.Text)
		}
		seg := &pb.TranscriptSegment{
			// #nosec G115 -- TranscriptSegment.Id is int32 on the wire, and segs
			// holds one entry per speaker run over the words of a single decode
			// result, which exhausts memory long before it reaches 2^31.
			Id:    int32(len(segs)),
			Text:  strings.Join(texts, " "),
			Start: msToNanos(run[0].Start),
			End:   msToNanos(run[len(run)-1].End),
		}
		// The speaker tag is 1-based with 0 meaning untagged, so an undiarized
		// run must stay unlabelled rather than be attributed to a speaker "0".
		if run[0].Speaker > 0 {
			seg.Speaker = strconv.Itoa(int(run[0].Speaker))
		}
		if withWords {
			seg.Words = wordsToProto(run)
		}
		segs = append(segs, seg)
	}

	for i := 1; i < len(words); i++ {
		if words[i].Speaker != words[start].Speaker {
			flush(i)
			start = i
		}
	}
	flush(len(words))
	return segs
}

// AudioTranscription decodes the audio at req.Dst and returns one offline
// transcription.
//
// The whole body runs inside withEngine, so the family check and the C calls
// that trust the handle happen under a single acquisition of engineMu. Decoding
// the audio is in there too: pkg/grpc/server.go already serialises RPCs on this
// backend through base.SingleThread, so the lock costs no concurrency, and the
// alternative (check, unlock, decode, relock) is the exact gap Free can land in.
func (n *NemoSpeech) AudioTranscription(ctx context.Context, req *pb.TranscriptRequest) (pb.TranscriptResult, error) {
	var out *pb.TranscriptResult
	if err := n.withEngine(familyASR, func() error {
		r, err := n.transcribe(req)
		out = r
		return err
	}); err != nil {
		return pb.TranscriptResult{}, err
	}
	// transcribe returns a non-nil result whenever it returns a nil error, so
	// this cannot fire today. It is a guard rather than a comment because the
	// alternative to stating the invariant is a nil dereference in an RPC
	// handler if a later edit ever adds a success path that forgets to set it.
	if out == nil {
		return pb.TranscriptResult{}, status.Error(codes.Internal,
			"nemo-speech-cpp: transcription produced no result")
	}

	// Assembled field by field rather than dereferenced: the RPC signature
	// returns the proto message by value, but the message embeds a mutex, so
	// copying the struct is a copylocks violation. Every backend in this tree
	// gets around it the same way, by only ever returning a composite literal.
	return pb.TranscriptResult{
		Text:     out.Text,
		Segments: out.Segments,
		Language: out.Language,
		Duration: out.Duration,
	}, nil
}

// transcribe is AudioTranscription's body. The caller must hold engineMu.
func (n *NemoSpeech) transcribe(req *pb.TranscriptRequest) (*pb.TranscriptResult, error) {
	if req.GetDst() == "" {
		return nil, status.Error(codes.InvalidArgument,
			"nemo-speech-cpp: TranscriptRequest.dst (audio path) is required")
	}

	pcm, sampleRate, err := decodeAudioMono16k(req.GetDst())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument,
			"nemo-speech-cpp: read audio: %v", err)
	}
	// Rejected here, before anything crosses the ABI, and not only inside
	// recognizeF32: a silent or truncated upload decodes to zero samples, and
	// there is no point building options and pinning strings for a request
	// that cannot produce a transcript. recognizeF32 keeps its own guard as a
	// precondition on the function.
	if len(pcm) == 0 {
		return nil, status.Error(codes.InvalidArgument, "nemo-speech-cpp: empty audio")
	}

	// A per-request language wins over the model-level default; both may be
	// empty, which the runtime reads as auto/model default.
	language := req.GetLanguage()
	if language == "" {
		language = n.opts.languageCode
	}
	langP, freeLang := cstr(language)
	defer freeLang()

	opts := ASRRecognitionOptionsDef()
	opts.LanguageCode = langP
	// Segments are built out of word offsets, so they are always asked for.
	opts.EnableWordTimeOffsets = true
	// Keyed on the recognizer owning a diar model, not on req.Diarize: asr.h
	// documents that a request asking for diarization from a recognizer created
	// without one fails with INVALID_ARGUMENT, and setting diar_model is already
	// the operator's opt-in.
	opts.EnableSpeakerDiarization = n.opts.diarModel != ""

	result, err := recognizeF32(n.recognizer, &opts, pcm, sampleRate)
	if err != nil {
		return nil, err
	}
	defer ASRResultDestroy(result)

	out := &pb.TranscriptResult{
		Text: ASRResultTranscript(result, 0),
		Segments: wordsToSegments(extractWords(result),
			wordsRequested(req.GetTimestampGranularities())),
	}
	// Multilingual models report what they decided the audio was; monolingual
	// ones report nothing, and an empty language is better than echoing back
	// whatever the caller guessed.
	if ASRResultLanguageCount(result, 0) > 0 {
		out.Language = ASRResultLanguageCode(result, 0, 0)
	}
	if sampleRate > 0 {
		out.Duration = float32(len(pcm)) / float32(sampleRate)
	}
	return out, nil
}
