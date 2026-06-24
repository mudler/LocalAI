package main

// purego binds by name at runtime and the config structs cross the ABI by
// pointer, so neither a renamed symbol nor a mis-laid-out mirror struct is
// visible to the compiler or the linker. Everything here is transcribed from
// sources/NeMo-Speech.cpp/include/nemo_speech/{asr,diar,tts,nmt}.h, and
// abi_test.go asserts it against the real shared objects.

import (
	"fmt"
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	asrLib uintptr
	ttsLib uintptr
	nmtLib uintptr
)

// ---- ASR ----

var (
	ASRCreate                func(cfg unsafe.Pointer, out *uintptr) int32
	ASRDestroy               func(recognizer uintptr)
	ASRRecognizeF32          func(recognizer uintptr, options unsafe.Pointer, samples *float32, nSamples uint64, sampleRate int32, out *uintptr) int32
	ASRStreamingRecognize    func(recognizer uintptr, options unsafe.Pointer, out *uintptr) int32
	ASRStreamPushF32         func(stream uintptr, samples *float32, nSamples uint64, sampleRate int32) int32
	ASRStreamForceEndpoint   func(stream uintptr) int32
	ASRStreamFinish          func(stream uintptr) int32
	ASRStreamNext            func(stream uintptr, out *uintptr) int32
	ASRStreamClose           func(stream uintptr)
	ASRRecognitionOptionsDef func() cASRRecognitionOptions

	ASRResultIsFinal          func(result uintptr) bool
	ASRResultAudioProcessed   func(result uintptr) float32
	ASRResultAlternativeCount func(result uintptr) uint64
	ASRResultTranscript       func(result uintptr, alt uint64) string
	ASRResultConfidence       func(result uintptr, alt uint64) float32
	ASRResultWordCount        func(result uintptr, alt uint64) uint64
	ASRResultWordText         func(result uintptr, alt, i uint64) string
	ASRResultWordStartTime    func(result uintptr, alt, i uint64) int32
	ASRResultWordEndTime      func(result uintptr, alt, i uint64) int32
	ASRResultWordConfidence   func(result uintptr, alt, i uint64) float32
	ASRResultWordSpeakerTag   func(result uintptr, alt, i uint64) int32
	ASRResultLanguageCount    func(result uintptr, alt uint64) uint64
	ASRResultLanguageCode     func(result uintptr, alt, i uint64) string
	ASRResultDestroy          func(result uintptr)

	ASRLastError func() string
	ASRVersion   func() string
)

// ---- Diarization (exported from the ASR library) ----

var (
	DiarCreate          func(cfg unsafe.Pointer, out *uintptr) int32
	DiarDestroy         func(model uintptr)
	DiarNumSpeakers     func(model uintptr) int32
	DiarSecondsPerFrame func(model uintptr) float64
	DiarStreamOpen      func(model uintptr, out *uintptr) int32
	DiarStreamPushF32   func(stream uintptr, samples *float32, nSamples uint64, sampleRate int32) int32
	DiarStreamFinish    func(stream uintptr) int32
	DiarStreamClose     func(stream uintptr)
	// cfg is the optional nemo_speech_diar_segmentation_config (NULL = library
	// defaults). The two-call count-then-fill pattern is documented on the C
	// declaration in diar.h.
	DiarSegments func(stream uintptr, cfg unsafe.Pointer, out unsafe.Pointer, capacity uint64, count *uint64) int32
)

// ---- TTS ----

var (
	TTSCreate                  func(cfg unsafe.Pointer, out *uintptr) int32
	TTSDestroy                 func(synthesizer uintptr)
	TTSSampleRate              func(synthesizer uintptr) int32
	TTSSpeakerCount            func(synthesizer uintptr) int32
	TTSSpeakerName             func(synthesizer uintptr, i uint64) string
	TTSSynthesizeText          func(synthesizer uintptr, options unsafe.Pointer, text string, callback uintptr, userData uintptr, statsOut unsafe.Pointer) int32
	TTSRuntimeConfigDefault    func() cTTSRuntimeConfig
	TTSSynthesisOptionsDefault func() cTTSSynthesisOptions
	TTSLastError               func() string
	TTSVersion                 func() string
)

// ---- NMT ----

var (
	NMTCreate         func(cfg unsafe.Pointer, out *uintptr) int32
	NMTDestroy        func(translator uintptr)
	NMTTranslate      func(translator uintptr, texts *uintptr, nTexts uint64, source, target string, out *uintptr) int32
	NMTResultCount    func(result uintptr) uint64
	NMTResultText     func(result uintptr, i uint64) string
	NMTResultLanguage func(result uintptr, i uint64) string
	NMTResultDestroy  func(result uintptr)
	NMTLastError      func() string
	NMTVersion        func() string
)

// ---- C struct mirrors ----
//
// Each mirrors a struct in include/nemo_speech/*.h field for field. The leading
// Size field is the C `size_t size` the runtime validates against its own
// sizeof, which is what makes a layout mismatch detectable at runtime instead
// of silently corrupting memory. Blank fields are System V AMD64 / AAPCS64
// padding: C inserts it implicitly, Go does not, so it has to be written out.
// See abi_test.go, which pins both every total size and every field offset.

type cASRBackendConfig struct {
	Size uintptr
	GPU  int32
	_    [4]byte // trailing pad to the struct's 8-byte alignment
}

type cASRModelConfig struct {
	Size uintptr
	Path uintptr
	Name uintptr
}

type cASRVADConfig struct {
	Size          uintptr
	ModelPath     uintptr
	EnableMasking bool
	_             [3]byte
	Onset         float32
	Offset        float32
	_             [4]byte
}

type cASRPostprocConfig struct {
	Size              uintptr
	ProfanityListPath uintptr
	ITNModelDir       uintptr
	PNCModelPath      uintptr
}

type cASRDiarConfig struct {
	Size               uintptr
	ModelPath          uintptr
	ChunkFrames        int32
	RightContextFrames int32
	LeftContextFrames  int32
	FIFOFrames         int32
	SpkcacheFrames     int32
	UpdatePeriodFrames int32
}

type cASRRecognizerConfig struct {
	Size        uintptr
	Backend     uintptr
	Model       uintptr
	Streaming   uintptr
	Decoder     uintptr
	VAD         uintptr
	Endpointing uintptr
	Postproc    uintptr
	Diar        uintptr
	Batching    uintptr
}

type cASRRecognitionOptions struct {
	Size                       uintptr
	RequestID                  uintptr
	LanguageCode               uintptr
	InterimResults             bool
	EnableWordTimeOffsets      bool
	EnableAutomaticPunctuation bool
	VerbatimTranscripts        bool
	ProfanityFilter            bool
	_                          [3]byte
	StopHistoryEouMs           int32
	_                          [4]byte
	SpeechContexts             uintptr
	SpeechContextCount         uintptr
	MaxAlternatives            int32
	EnableSpeakerDiarization   bool
	_                          [3]byte
	MaxSpeakerCount            int32
	_                          [4]byte
}

// cDiarModelConfig mirrors nemo_speech_diar_model_config (diar.h). This is the
// standalone Sortformer pipeline's own config and is NOT cASRDiarConfig, which
// is the diarizer attached to a recognizer: this one carries gpu and preset,
// that one does not.
//
// The six frame counts are sentinel-sensitive. src/asr/c_api.cpp applies each
// one only when it is > 0, EXCEPT left_context_frames, which it applies when it
// is >= 0. A zero-valued struct would therefore pin the left context to 0
// rather than leave the preset's value alone, so loadDiarizer writes -1 into
// all six.
type cDiarModelConfig struct {
	Size      uintptr
	ModelPath uintptr
	GPU       int32
	_         [4]byte // pad to the alignment of the pointer that follows
	Preset    uintptr
	// Encoder-frame geometry overrides, applied on top of the preset.
	ChunkFrames        int32
	RightContextFrames int32
	LeftContextFrames  int32
	FIFOFrames         int32
	SpkcacheFrames     int32
	UpdatePeriodFrames int32
}

// cDiarSegmentationConfig mirrors nemo_speech_diar_segmentation_config
// (diar.h): the NeMo ts_vad postprocessing applied when turning per-frame
// speaker probabilities into segments.
//
// onset and offset are float, the four durations are double. That mixture is
// the whole reason this mirror needs its offsets pinned: writing all six as
// float32 or all six as float64 both produce a struct C would read shifted.
type cDiarSegmentationConfig struct {
	Size           uintptr
	Onset          float32
	Offset         float32
	PadOnsetSec    float64
	PadOffsetSec   float64
	MinGapSec      float64
	MinDurationSec float64
}

// cDiarSegment mirrors nemo_speech_diar_segment (diar.h), the element type
// nemo_speech_diar_segments fills.
//
// It has no leading size field: unlike the config structs it travels from C to
// Go, so there is no caller-declared size for the runtime to validate against.
// The times are already SECONDS (double), not frame indices, so nothing here
// needs the model's seconds-per-frame to be interpreted. Speaker is 1-based,
// matching WordInfo.speaker_tag on the ASR surface.
type cDiarSegment struct {
	StartTime float64
	EndTime   float64
	Speaker   int32
	_         [4]byte // trailing pad to the struct's 8-byte alignment
}

type cTTSModelConfig struct {
	Size                   uintptr
	MagpieModel            uintptr
	CodecModel             uintptr
	TokenizerModelDir      uintptr
	TextNormalizerModelDir uintptr
}

// cTTSRuntimeConfig mirrors nemo_speech_tts_runtime_config. The four backend /
// mode fields are C enums, which this toolchain lays out as int32.
type cTTSRuntimeConfig struct {
	Size                uintptr
	Speaker             int32
	Threads             int32
	CodecThreads        int32
	Seed                int32
	Steps               int32
	TopK                int32
	ChunkFrames         int32
	CodecQueueDepth     int32
	CodecHistoryFrames  int32
	CodecFutureFrames   int32
	WindowMs            int32
	Temperature         float32
	OverrideTemperature bool
	_                   [3]byte
	CFGScale            float32
	OverrideCFGScale    bool
	UseCFG              bool
	UseLocalTransformer bool
	UseKVCache          bool
	UseStatefulCodec    bool
	CodecCPU            bool
	FlushPartialChunk   bool
	Verbose             bool
	LTBackend           int32
	SamplingBackend     int32
	UMAMode             int32
	LongformMode        int32
	LTFP32              bool
	_                   [7]byte
}

type cTTSSynthesizerConfig struct {
	Size                uintptr
	Model               uintptr
	Runtime             uintptr
	DefaultLanguageCode uintptr
	DefaultVoiceName    uintptr
}

type cTTSSynthesisOptions struct {
	Size                uintptr
	RequestID           uintptr
	LanguageCode        uintptr
	Speaker             int32
	Seed                int32
	Steps               int32
	TopK                int32
	Temperature         float32
	OverrideTemperature bool
	_                   [3]byte
	CFGScale            float32
	OverrideCFGScale    bool
	_                   [3]byte
	VoiceName           uintptr
	OutputSampleRate    int32
	_                   [4]byte
}

type cNMTBackendConfig struct {
	Size uintptr
	GPU  int32
	_    [4]byte
}

type cNMTModelConfig struct {
	Size uintptr
	Path uintptr
	NCtx int32
	_    [4]byte
}

type cNMTTranslatorConfig struct {
	Size       uintptr
	Backend    uintptr
	Model      uintptr
	Generation uintptr
	Pool       uintptr
}

// symbol pairs a Go function pointer with its exported C name. Keeping the
// name next to the var means `nm -D libnemo_speech_asr_c.so.1 | grep nemo_speech`
// is enough to spot drift after a pin bump.
type symbol struct {
	fn   any
	name string
	lib  *uintptr
}

func symbols() []symbol {
	return []symbol{
		{&ASRCreate, "nemo_speech_asr_create", &asrLib},
		{&ASRDestroy, "nemo_speech_asr_destroy", &asrLib},
		{&ASRRecognizeF32, "nemo_speech_asr_recognize_f32", &asrLib},
		{&ASRStreamingRecognize, "nemo_speech_asr_streaming_recognize", &asrLib},
		{&ASRStreamPushF32, "nemo_speech_asr_stream_push_f32", &asrLib},
		{&ASRStreamForceEndpoint, "nemo_speech_asr_stream_force_endpoint", &asrLib},
		{&ASRStreamFinish, "nemo_speech_asr_stream_finish", &asrLib},
		{&ASRStreamNext, "nemo_speech_asr_stream_next", &asrLib},
		{&ASRStreamClose, "nemo_speech_asr_stream_close", &asrLib},
		{&ASRRecognitionOptionsDef, "nemo_speech_asr_recognition_options_default", &asrLib},
		{&ASRResultIsFinal, "nemo_speech_asr_result_is_final", &asrLib},
		{&ASRResultAudioProcessed, "nemo_speech_asr_result_audio_processed", &asrLib},
		{&ASRResultAlternativeCount, "nemo_speech_asr_result_alternative_count", &asrLib},
		{&ASRResultTranscript, "nemo_speech_asr_result_transcript", &asrLib},
		{&ASRResultConfidence, "nemo_speech_asr_result_confidence", &asrLib},
		{&ASRResultWordCount, "nemo_speech_asr_result_word_count", &asrLib},
		{&ASRResultWordText, "nemo_speech_asr_result_word_text", &asrLib},
		{&ASRResultWordStartTime, "nemo_speech_asr_result_word_start_time", &asrLib},
		{&ASRResultWordEndTime, "nemo_speech_asr_result_word_end_time", &asrLib},
		{&ASRResultWordConfidence, "nemo_speech_asr_result_word_confidence", &asrLib},
		{&ASRResultWordSpeakerTag, "nemo_speech_asr_result_word_speaker_tag", &asrLib},
		{&ASRResultLanguageCount, "nemo_speech_asr_result_language_count", &asrLib},
		{&ASRResultLanguageCode, "nemo_speech_asr_result_language_code", &asrLib},
		{&ASRResultDestroy, "nemo_speech_asr_result_destroy", &asrLib},
		{&ASRLastError, "nemo_speech_asr_last_error", &asrLib},
		{&ASRVersion, "nemo_speech_asr_version", &asrLib},

		{&DiarCreate, "nemo_speech_diar_create", &asrLib},
		{&DiarDestroy, "nemo_speech_diar_destroy", &asrLib},
		{&DiarNumSpeakers, "nemo_speech_diar_num_speakers", &asrLib},
		{&DiarSecondsPerFrame, "nemo_speech_diar_seconds_per_frame", &asrLib},
		{&DiarStreamOpen, "nemo_speech_diar_stream_open", &asrLib},
		{&DiarStreamPushF32, "nemo_speech_diar_stream_push_f32", &asrLib},
		{&DiarStreamFinish, "nemo_speech_diar_stream_finish", &asrLib},
		{&DiarStreamClose, "nemo_speech_diar_stream_close", &asrLib},
		{&DiarSegments, "nemo_speech_diar_segments", &asrLib},

		{&TTSCreate, "nemo_speech_tts_create", &ttsLib},
		{&TTSDestroy, "nemo_speech_tts_destroy", &ttsLib},
		{&TTSSampleRate, "nemo_speech_tts_sample_rate", &ttsLib},
		{&TTSSpeakerCount, "nemo_speech_tts_speaker_count", &ttsLib},
		{&TTSSpeakerName, "nemo_speech_tts_speaker_name", &ttsLib},
		{&TTSSynthesizeText, "nemo_speech_tts_synthesize_text", &ttsLib},
		{&TTSRuntimeConfigDefault, "nemo_speech_tts_runtime_config_default", &ttsLib},
		{&TTSSynthesisOptionsDefault, "nemo_speech_tts_synthesis_options_default", &ttsLib},
		{&TTSLastError, "nemo_speech_tts_last_error", &ttsLib},
		{&TTSVersion, "nemo_speech_tts_version", &ttsLib},

		{&NMTCreate, "nemo_speech_nmt_create", &nmtLib},
		{&NMTDestroy, "nemo_speech_nmt_destroy", &nmtLib},
		{&NMTTranslate, "nemo_speech_nmt_translate", &nmtLib},
		{&NMTResultCount, "nemo_speech_nmt_result_count", &nmtLib},
		{&NMTResultText, "nemo_speech_nmt_result_text", &nmtLib},
		{&NMTResultLanguage, "nemo_speech_nmt_result_language", &nmtLib},
		{&NMTResultDestroy, "nemo_speech_nmt_result_destroy", &nmtLib},
		{&NMTLastError, "nemo_speech_nmt_last_error", &nmtLib},
		{&NMTVersion, "nemo_speech_nmt_version", &nmtLib},
	}
}

// registerSymbols binds every entry point. purego panics on a missing symbol,
// so this recovers and returns the offending name: after an upstream pin bump a
// rename must fail loudly at startup, not at first inference.
func registerSymbols() error {
	for _, s := range symbols() {
		if err := registerOne(s); err != nil {
			return err
		}
	}
	return nil
}

func registerOne(s symbol) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("nemo-speech-cpp: binding %q: %v", s.name, r)
		}
	}()
	purego.RegisterLibFunc(s.fn, *s.lib, s.name)
	return nil
}
