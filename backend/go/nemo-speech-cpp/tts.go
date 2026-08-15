package main

import (
	"bytes"
	"math"
	"os"
	"runtime"
	"strconv"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	laudio "github.com/mudler/LocalAI/pkg/audio"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The backend-preference enum from include/nemo_speech/tts.h. The C type is an
// enum, which this toolchain lays out as int32, so the values are written here
// rather than inferred.
const (
	ttsBackendAuto int32 = 0
	ttsBackendCPU  int32 = 1
)

// maxWAVDataBytes is the largest PCM payload a RIFF WAV can describe.
//
// Both size fields in the header are uint32, so a longer payload would not
// merely be unusual, it would wrap and produce a file whose header disagrees
// with its contents. At 22.05 kHz mono 16-bit that ceiling is about 27 hours of
// speech, so nothing real is being refused.
//
// Typed int64 rather than left untyped so the comparison below is the same one
// on every architecture: an untyped constant this large does not fit in a
// 32-bit int and would not compile there at all.
const maxWAVDataBytes int64 = math.MaxUint32 - laudio.WAVHeaderSize

// wavStreamingSize is the placeholder both size fields carry while the total
// length is still unknown. Players read it as "stream until the socket closes".
const wavStreamingSize = 0xFFFFFFFF

// ttsSink receives one PCM chunk, already copied into Go memory.
//
// It returns false to cancel the synthesis in progress: that is the C
// callback's only way to stop work early, and the runtime turns it into
// NEMO_SPEECH_TTS_ERROR_CANCELLED.
type ttsSink func(pcm []byte) bool

// ttsSinkTable maps the user_data value handed to C back to the Go sink the
// chunk belongs to.
//
// A single "current sink" pointer would be enough for one model, since every
// RPC holds that model's engineMu for its whole body. It is not enough for the
// process: engineMu is per-NemoSpeech, one backend process can hold several
// loaded models, and the callback below is shared by all of them, so two TTS
// models synthesizing at once would overwrite each other's sink. The id is what
// keeps them apart.
//
// The id is an integer and never a Go pointer. user_data crosses into C as a
// void*, which the collector does not trace, so a Go pointer parked there would
// have exactly the lifetime problem cstr documents.
type ttsSinkTable struct {
	mu    sync.Mutex
	next  uintptr
	sinks map[uintptr]ttsSink
}

var ttsSinks = &ttsSinkTable{sinks: map[uintptr]ttsSink{}}

// register adds sink and returns its id together with the release the caller
// MUST defer. Ids start at 1 so a zeroed or stale user_data cannot resolve to
// somebody else's sink.
func (t *ttsSinkTable) register(sink ttsSink) (uintptr, func()) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.next++
	id := t.next
	t.sinks[id] = sink
	return id, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		delete(t.sinks, id)
	}
}

// lookup returns the sink for id, or nil once it has been released.
func (t *ttsSinkTable) lookup(id uintptr) ttsSink {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.sinks[id]
}

var (
	ttsCallbackOnce sync.Once
	ttsCallbackFn   uintptr
)

// ttsPCMCallback returns the C function pointer the runtime drives PCM through,
// compiling it on first use.
//
// Exactly one is ever created per process, and that is a hard requirement
// rather than a tidiness argument. purego.NewCallback writes into a fixed table
// of maxCB = 2000 entries (purego/syscall_sysv.go) and never releases an entry,
// so a callback compiled per request panics the whole backend process with
// "purego: the maximum number of callbacks has been reached" on the 2001st
// synthesis. Per model load is not safe either: a server that swaps models
// reaches the same ceiling, just later and even less predictably. Routing every
// synthesis through one callback plus a user_data id is what keeps the count at
// one for the life of the process.
func ttsPCMCallback() uintptr {
	ttsCallbackOnce.Do(func() { ttsCallbackFn = purego.NewCallback(ttsDeliverPCM) })
	return ttsCallbackFn
}

// ttsDeliverPCM is the body of that callback: nemo_speech_tts_pcm_callback,
// which the runtime invokes on its own thread for each chunk it produces.
//
// The bytes are copied rather than aliased. The pointer addresses a std::string
// the runtime owns and reuses for the next chunk (src/tts/c_api.cpp
// make_callback), so a slice over it would be rewritten under the consumer as
// soon as this returns.
func ttsDeliverPCM(pcm unsafe.Pointer, nBytes uint64, userData uintptr) bool {
	// The table's lock is released before the sink runs, which matters because
	// TTSStream's sink blocks on a channel send until its client drains it.
	// Holding the lock across that would stall every other model's callback
	// behind one slow consumer.
	sink := ttsSinks.lookup(userData)
	if sink == nil {
		// The request that registered this sink has already returned, so there
		// is nowhere to put the audio. false cancels rather than letting the
		// runtime synthesize to completion into a consumer that stopped
		// listening.
		return false
	}
	// c_api.cpp filters empty chunks before calling us, so this is belt and
	// braces: unsafe.Slice on a null pointer is what it protects against.
	if pcm == nil || nBytes == 0 {
		return true
	}

	buf := make([]byte, nBytes)
	// #nosec G103 -- pcm and nBytes are the C-owned buffer and its length from
	// one callback invocation, both null/zero-checked above. The slice is read
	// only, its length is the length the runtime declared for that buffer, and it
	// is copied into Go memory here and never retained past this return.
	copy(buf, unsafe.Slice((*byte)(pcm), nBytes))
	return sink(buf)
}

// synthesizer is the TTS half of the C API, narrowed to what the two RPCs use.
//
// It is an interface for the same reason asrSession and diarStream are: no
// MagpieTTS GGUF is small enough to keep in the tree, so the logic layered on
// top of the ABI (validation, the WAV framing, chunk ordering) would otherwise
// have no test at all. The seam is at the ABI, not at the model: a fake here
// scripts what the C API emits, it does not pretend to synthesize anything.
type synthesizer interface {
	// sampleRate is the rate the PCM chunks arrive at.
	sampleRate() int32
	// synthesize maps req onto the runtime's per-request options and runs one
	// synthesis, handing each chunk to sink as it is produced.
	synthesize(req *pb.TTSRequest, defaultLanguage string, sink ttsSink) error
}

// cSynthesizer is the real synthesizer, over one nemo_speech_tts_synthesizer.
type cSynthesizer struct {
	handle uintptr
}

func (s *cSynthesizer) sampleRate() int32 { return TTSSampleRate(s.handle) }

func (s *cSynthesizer) synthesize(req *pb.TTSRequest, defaultLanguage string, sink ttsSink) error {
	// Started from the runtime's own defaults, not from a zero struct: every
	// numeric field here is sentinel-sensitive (speaker/seed < 0, steps/top_k
	// <= 0 all mean "use the synthesizer's value"), and a zeroed struct would
	// read as speaker 0, seed 0 and zero decoding steps.
	opts := TTSSynthesisOptionsDefault()

	// A per-request language wins over the model-level default; both may be
	// empty, which the runtime resolves to the synthesizer's own default.
	language := req.GetLanguage()
	if language == "" {
		language = defaultLanguage
	}
	langP, freeLang := cstr(language)
	defer freeLang()
	opts.LanguageCode = langP

	speaker, voiceName := resolveSpeaker(req.GetVoice())
	opts.Speaker = speaker
	voiceP, freeVoice := cstr(voiceName)
	defer freeVoice()
	opts.VoiceName = voiceP

	applySynthesisParams(&opts, req.GetParams())

	id, release := ttsSinks.register(sink)
	defer release()

	// stats_out is NULL: nemo_speech_tts_synthesis_stats is 300-odd bytes of
	// timing detail with nowhere to go on either RPC, and the C API documents
	// NULL as the way to decline it.
	// #nosec G103 -- opts is a local POD struct borrowed for this call only. Its
	// two uintptr members (LanguageCode, VoiceName) are cstr allocations pinned
	// by the defers above, and this entry point is synchronous, so it returns
	// before those pins are released even though the callbacks run off-thread.
	st := TTSSynthesizeText(s.handle, unsafe.Pointer(&opts), req.GetText(), ttsPCMCallback(), id, nil)
	if st != 0 {
		// An unknown voice_name arrives here as INVALID_ARGUMENT
		// (src/tts/synthesizer.cpp throws std::invalid_argument, which
		// src/tts/c_api.cpp's guard maps to it), and a consumer that stopped
		// reading arrives as CANCELLED. Neither is this backend's failure, so
		// neither goes out as Internal.
		return statusErrorf(st, "nemo-speech-cpp: synthesize: %s", TTSLastError())
	}
	return nil
}

// resolveSpeaker splits a request's voice into the two fields the C API has for
// it: a speaker index and a voice name.
//
// nemo_speech_tts_synthesis_options.voice_name is documented as ignored
// whenever speaker >= 0, and src/tts/synthesizer.cpp only calls resolve_speaker
// when options.speaker is negative, so the two are alternatives and never a
// pair. A named voice must therefore leave the index at -1 or the name is
// silently dropped.
//
// The numeric split cannot change what the runtime picks: resolve_speaker parses
// a numeric voice_name itself, so anything this function passes through as a
// name and that happens to be a number lands on the same speaker anyway. What it
// must not do is let a NEGATIVE number through as an index. "-1" is not a
// speaker, it is the sentinel for "use the default", and treating it as an index
// would turn a request naming an invalid voice into one that quietly synthesizes
// in the default voice instead of being rejected.
func resolveSpeaker(voice string) (int32, string) {
	if voice == "" {
		return -1, ""
	}
	if idx, err := strconv.ParseInt(voice, 10, 32); err == nil && idx >= 0 {
		return int32(idx), ""
	}
	return -1, voice
}

// applySynthesisParams maps TTSRequest.params onto the runtime's per-request
// options.
//
// Only the five knobs nemo_speech_tts_synthesis_options actually has are read.
// An unset or unparseable value leaves the field alone rather than resetting it:
// the struct arrives carrying the runtime's defaults, and params is documented
// as "unset leaves the backend's configured defaults".
//
// The sentinels are the reason each write is guarded rather than unconditional.
// src/tts/magpietts/runtime.cpp takes the request's seed only when it is >= 0
// and its steps and top_k only when they are > 0, so writing a parsed 0 or a
// negative would not merely be ignored, it would erase the option's meaning for
// a caller who passed "0" expecting something.
//
// temperature and cfg_scale each need their override flag set as well. The
// runtime reads the float only when the flag is true and otherwise falls back to
// the synthesizer's config, so a temperature written without its flag is
// silently discarded.
func applySynthesisParams(o *cTTSSynthesisOptions, params map[string]string) {
	if len(params) == 0 {
		return
	}
	if v, ok := parseInt32Param(params["seed"]); ok && v >= 0 {
		o.Seed = v
	}
	if v, ok := parseInt32Param(params["steps"]); ok && v > 0 {
		o.Steps = v
	}
	if v, ok := parseInt32Param(params["top_k"]); ok && v > 0 {
		o.TopK = v
	}
	if v, ok := parseFloat32Param(params["temperature"]); ok {
		o.Temperature = v
		o.OverrideTemperature = true
	}
	if v, ok := parseFloat32Param(params["cfg_scale"]); ok {
		o.CFGScale = v
		o.OverrideCFGScale = true
	}
}

// parseInt32Param reads one params entry. ok is false for an absent or
// unparseable value, which the caller reads as "leave the default".
func parseInt32Param(v string) (int32, bool) {
	if v == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil {
		xlog.Warn("nemo-speech-cpp: ignoring unparseable TTS parameter", "value", v)
		return 0, false
	}
	return int32(n), true
}

func parseFloat32Param(v string) (float32, bool) {
	if v == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(v, 32)
	if err != nil {
		xlog.Warn("nemo-speech-cpp: ignoring unparseable TTS parameter", "value", v)
		return 0, false
	}
	return float32(f), true
}

// ttsModelConfig builds the create-time model config.
//
// Extracted from loadTTS and asserted field by field because three adjacent
// members of nemo_speech_tts_model_config are same-typed paths. Swapping two of
// them changes neither the struct's size nor any field's offset, so the layout
// assertions in abi_test.go cannot see it, and the failure it produces is the
// runtime loading the codec as the acoustic model.
//
// Every argument is a C pointer from cstr, not a Go string, and the caller owns
// the releases. tnDir may be null: text_normalizer_model_dir is optional and an
// empty one leaves the text unchanged.
func ttsModelConfig(magpieModel, codecModel, tokenizerDir, tnDir uintptr) cTTSModelConfig {
	return cTTSModelConfig{
		Size:                   unsafe.Sizeof(cTTSModelConfig{}),
		MagpieModel:            magpieModel,
		CodecModel:             codecModel,
		TokenizerModelDir:      tokenizerDir,
		TextNormalizerModelDir: tnDir,
	}
}

// ttsRuntimeBackend maps the backend's gpu option onto the TTS runtime's
// backend preference.
//
// nemo_speech_tts_runtime_config has no device index at all, only a three-way
// AUTO/CPU/CUDA preference, so a gpu option naming a particular device cannot be
// honoured and AUTO is the honest answer for it. A negative gpu is different: it
// is the option's documented "CPU" across this whole backend (asr.h: "-1 = CPU")
// and it is also the default, so it has to pin the preference rather than leave
// the runtime free to pick CUDA.
func ttsRuntimeBackend(gpu int32) int32 {
	if gpu < 0 {
		return ttsBackendCPU
	}
	return ttsBackendAuto
}

// loadTTS creates the MagpieTTS synthesizer.
//
// It runs after discoverTTSAssets, so codecModel and tokenizerDir are already
// resolved and non-empty; tnDir stays optional.
//
// This must not take engineMu: Load is its only caller and already holds it.
func (n *NemoSpeech) loadTTS(modelFile string) error {
	// nemo_speech_tts_create deep-copies every const char* into a std::string
	// (src/tts/c_api.cpp, via str_or_empty) and keeps no pointer afterwards, so
	// pinning across the create call is both necessary and sufficient.
	var pinner runtime.Pinner
	defer pinner.Unpin()

	magpieP, freeMagpie := cstr(modelFile)
	defer freeMagpie()
	codecP, freeCodec := cstr(n.opts.codecModel)
	defer freeCodec()
	tokenizerP, freeTokenizer := cstr(n.opts.tokenizerDir)
	defer freeTokenizer()
	tnP, freeTN := cstr(n.opts.tnDir)
	defer freeTN()

	model := ttsModelConfig(magpieP, codecP, tokenizerP, tnP)

	rt := TTSRuntimeConfigDefault()
	backend := ttsRuntimeBackend(n.opts.gpu)
	rt.LTBackend = backend
	rt.SamplingBackend = backend
	// The codec is a separate graph with its own placement, so a CPU-only
	// request has to say so here too or it would still try to run on the GPU.
	rt.CodecCPU = backend == ttsBackendCPU

	langP, freeLang := cstr(n.opts.languageCode)
	defer freeLang()

	cfg := cTTSSynthesizerConfig{
		Size:                unsafe.Sizeof(cTTSSynthesizerConfig{}),
		Model:               pinPtr(&pinner, &model),
		Runtime:             pinPtr(&pinner, &rt),
		DefaultLanguageCode: langP,
	}

	xlog.Info("nemo-speech-cpp: creating synthesizer",
		"gpu", n.opts.gpu,
		"codec", n.opts.codecModel,
		"tokenizer", n.opts.tokenizerDir,
		"text_normalizer", n.opts.tnDir != "")

	// Compiled before the handle exists so that a full callback table fails the
	// load, where the operator can see it, rather than the first synthesis.
	ttsPCMCallback()

	// #nosec G103 -- cfg is a local POD struct borrowed for this call only. Model
	// and Runtime are pinPtr addresses held by the pinner unpinned on return, the
	// paths they carry are cstr allocations freed by the defers above, and
	// nemo_speech_tts_create deep-copies every string it reads.
	if st := TTSCreate(unsafe.Pointer(&cfg), &n.synth); st != 0 {
		return statusErrorf(st, "nemo-speech-cpp: tts create: %s", TTSLastError())
	}
	return nil
}

// validateTTSRequest rejects what the runtime would reject, before anything
// crosses the ABI, and names the fields this backend drops.
//
// Empty text is checked here rather than left to the C side for the error code:
// src/tts/synthesizer.cpp throws "text is required", which arrives as a status
// this layer would otherwise report as Internal, and an empty prompt is a client
// mistake, not a backend failure.
//
// instructions is logged rather than rejected, for the reason the diarization
// path logs its own dropped fields: a caller that asked for an expressive style
// still wants the audio it can have, and a request naming something this backend
// silently ignores should say so where an operator can find it. There is nothing
// to map it onto, because MagpieTTS conditions on a speaker, not on a prose
// style description: nemo_speech_tts_synthesis_options has speaker and
// voice_name and no free-text field at all.
func validateTTSRequest(req *pb.TTSRequest) error {
	if req.GetText() == "" {
		return status.Error(codes.InvalidArgument, "nemo-speech-cpp: TTSRequest.text is required")
	}
	if req.GetInstructions() != "" {
		xlog.Warn("nemo-speech-cpp: ignoring TTSRequest.instructions, this model has no equivalent")
	}
	return nil
}

// outputSampleRate reads the rate the synthesizer emits at.
//
// A non-positive rate is refused rather than passed on. nemo_speech_tts_sample_rate
// answers 0 for a null handle, and a WAV header carrying 0 is not a slightly
// wrong file, it is one no player can decode and one whose duration is
// undefined.
func outputSampleRate(s synthesizer) (uint32, error) {
	rate := s.sampleRate()
	if rate <= 0 {
		return 0, status.Error(codes.Internal,
			"nemo-speech-cpp: the synthesizer reported no sample rate")
	}
	return uint32(rate), nil
}

// wavFile frames PCM as a complete WAV: a header with real sizes, then the
// samples.
//
// pcm is little-endian signed 16-bit mono, which is what the runtime's callback
// delivers, and is exactly what pkg/audio's header describes, so nothing is
// converted on the way through.
func wavFile(pcm []byte, sampleRate uint32) ([]byte, error) {
	if int64(len(pcm)) > maxWAVDataBytes {
		return nil, status.Errorf(codes.Internal,
			"nemo-speech-cpp: synthesis produced %d bytes, more than a WAV header can describe", len(pcm))
	}

	var buf bytes.Buffer
	// #nosec G115 -- len(pcm) is checked against maxWAVDataBytes (MaxUint32 minus
	// the header) immediately above, so the narrowing to uint32 cannot wrap.
	h := laudio.NewWAVHeaderWithRate(uint32(len(pcm)), sampleRate)
	if err := h.Write(&buf); err != nil {
		return nil, status.Errorf(codes.Internal, "nemo-speech-cpp: write WAV header: %v", err)
	}
	buf.Write(pcm)
	return buf.Bytes(), nil
}

// streamingWAVHeader is the first chunk of a streamed synthesis: the same header
// with both sizes left unknown, since the total length is not known until the
// synthesis ends.
//
// NewWAVHeaderWithRate derives ChunkSize from the payload length, so the RIFF
// size has to be overwritten as well: 36 + 0xFFFFFFFF wraps to 35, which is a
// smaller number than the header itself.
func streamingWAVHeader(sampleRate uint32) []byte {
	h := laudio.NewWAVHeaderWithRate(wavStreamingSize, sampleRate)
	h.ChunkSize = wavStreamingSize

	var buf bytes.Buffer
	// Write only fails on the writer, and bytes.Buffer does not fail.
	_ = h.Write(&buf)
	return buf.Bytes()
}

// synthesizeWAV runs one synthesis and writes the whole result to dst.
func synthesizeWAV(s synthesizer, req *pb.TTSRequest, defaultLanguage string) error {
	if err := validateTTSRequest(req); err != nil {
		return err
	}
	if req.GetDst() == "" {
		return status.Error(codes.InvalidArgument,
			"nemo-speech-cpp: TTSRequest.dst (output path) is required")
	}

	// Read before the synthesis rather than after: it is what the header is
	// built from, and failing on a bad handle here costs nothing, where failing
	// after costs the whole synthesis.
	rate, err := outputSampleRate(s)
	if err != nil {
		return err
	}

	var pcm []byte
	err = s.synthesize(req, defaultLanguage, func(chunk []byte) bool {
		pcm = append(pcm, chunk...)
		return true
	})
	if err != nil {
		return err
	}
	// A synthesis that returned OK having emitted nothing is a runtime bug, but
	// the file it would produce is a valid empty WAV, which reaches the user as
	// silence with no error anywhere.
	if len(pcm) == 0 {
		return status.Error(codes.Internal, "nemo-speech-cpp: synthesis produced no audio")
	}

	out, err := wavFile(pcm, rate)
	if err != nil {
		return err
	}
	if err := os.WriteFile(req.GetDst(), out, 0o600); err != nil {
		return status.Errorf(codes.Internal, "nemo-speech-cpp: write %q: %v", req.GetDst(), err)
	}
	return nil
}

// streamWAV runs one synthesis and emits a WAV header followed by each PCM
// chunk as the runtime produces it.
//
// The header is the backend's job, not the caller's: pkg/grpc/server.go only
// ever sets Reply.Audio on this path and never Reply.Message, and
// core/backend/tts.go's own header branch is keyed on Message, so a backend that
// emitted bare PCM would stream something no client could decode. sherpa-onnx
// and magpie-tts-cpp both do the same.
//
// out is not closed here. TTSStream owns it, and closing it in one of two places
// depending on how far the request got is how a stream ends up half-closed.
func streamWAV(s synthesizer, req *pb.TTSRequest, defaultLanguage string, out chan<- []byte) error {
	if err := validateTTSRequest(req); err != nil {
		return err
	}

	rate, err := outputSampleRate(s)
	if err != nil {
		return err
	}
	out <- streamingWAVHeader(rate)

	return s.synthesize(req, defaultLanguage, func(chunk []byte) bool {
		out <- chunk
		return true
	})
}

// TTS synthesizes req.Text and writes a WAV to req.Dst.
//
// The whole body runs inside withEngine, so the family check and the C calls
// that trust the handle happen under a single acquisition of engineMu. See the
// handoff notes at the bottom of nemospeech.go: Free runs without the backend
// lock, so anything that checks the family and then releases the lock before
// calling C can have the handle destroyed underneath it.
func (n *NemoSpeech) TTS(req *pb.TTSRequest) error {
	return n.withEngine(familyTTS, func() error {
		return synthesizeWAV(&cSynthesizer{handle: n.synth}, req, n.opts.languageCode)
	})
}

// TTSStream synthesizes req.Text and emits the audio on results as it is
// produced.
//
// results is closed on EVERY path, including the family rejection and a
// validation failure, and the close is deferred outside withEngine so that a
// rejected family still closes it. pkg/grpc/server.go drains this channel from a
// goroutine and then blocks on that goroutine finishing, so a channel left open
// does not fail the request, it hangs the RPC and, because the backend lock is
// still held, every request behind it.
//
// Holding engineMu for the whole stream is deliberate and is the consequence
// documented on the locking protocol: an unload waits for the stream to end
// rather than destroying the synthesizer underneath it. There is no unbounded
// wait here, because unlike the ASR streams this one is driven by the runtime
// and ends when the text does, not when a client decides to stop sending.
func (n *NemoSpeech) TTSStream(req *pb.TTSRequest, results chan []byte) error {
	defer close(results)

	return n.withEngine(familyTTS, func() error {
		return streamWAV(&cSynthesizer{handle: n.synth}, req, n.opts.languageCode, results)
	})
}
